package embedspawn

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"os/user"
	"path/filepath"
	"reflect"
	"syscall"
	"time"
	"unsafe"

	"ask.systems/daemon/portal/gate"

	"golang.org/x/sys/windows"
)

type platformSpecificChildrenInfo struct {
	jobObject      windows.Handle
	successfulInit bool
}

func (p *platformSpecificChildrenInfo) Init(c *children) {
	if *dontKillChildren {
		return
	}
	var err error
	p.jobObject, err = windows.CreateJobObject(nil, nil)
	if err != nil {
		log.Print("Failed to CreateJobObject for killing children: ", err)
		windows.CloseHandle(p.jobObject)
		return
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	_, err = windows.SetInformationJobObject(
		p.jobObject,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)))
	if err != nil {
		log.Print("Failed to set the JobObject to kill children: ", err)
		windows.CloseHandle(p.jobObject)
		return
	}
	go func() {
		<-c.quit
		windows.CloseHandle(p.jobObject)
	}()
	p.successfulInit = true
}

func (c *children) addProcToJobObject(proc *os.Process) error {
	handleField := reflect.ValueOf(proc).Elem().FieldByName("handle")
	if !handleField.IsValid() {
		return errors.New("Go has changed and we cannot get the windows child process handle anymore.")
	}
	childHandle := windows.Handle(handleField.Uint())
	err := windows.AssignProcessToJobObject(
		c.platform.jobObject,
		childHandle)
	if err != nil {
		return fmt.Errorf("Failed to AssignProcessToJobObject: %w", err)
	}
	return nil
}

func (children *children) StartProgram(cmd *Command) error {
	if len(cmd.Binary) == 0 {
		return fmt.Errorf("Binary is required")
	}
	if cmd.AutoTlsCerts {
		return fmt.Errorf("Spawn cannot automatically refresh files on windows. Just have portal load the certs directly.")
	}
	if !cmd.NoChroot {
		return fmt.Errorf("Windows does not have a chroot feature.")
	}
	if cmd.User != "" {
		return fmt.Errorf("Windows has no way to start processes as other users.")
	}
	u, err := user.Current()
	if err != nil {
		return err
	}
	name := cmd.FullName()
	attr := &os.ProcAttr{
		// Note: on windows we need to pass in the existing environment variables
		// because there are some important system variables that are needed to
		// start the process
		Env: append(os.Environ(), []string{
			fmt.Sprintf("SPAWN_FILES=%v", len(cmd.Files)),
			fmt.Sprintf("SPAWN_PORTS=%v", len(cmd.Ports)),
		}...),
	}
	err = gate.ResolveFlags()
	if err == nil {
		attr.Env = append(attr.Env, fmt.Sprintf("PORTAL_ADDR=%v", *gate.Address))
		attr.Env = append(attr.Env, fmt.Sprintf("PORTAL_TOKEN=%v", *gate.Token))
	}
	log.Print("Starting ", name)

	workingDir := cmd.WorkingDir
	if workingDir == "" {
		workingDir = u.HomeDir
	}

	// Allow overwriting the binary file on windows because we only clean it up if
	// ReportDown is called, so if spawn gets killed too fast it can't clean it up
	binary, err := copyBinary(cmd.Binary, filepath.Join(workingDir, name+".exe"), false /*exclusive*/, 0, 0)
	if err != nil {
		return fmt.Errorf("Failed to setup the binary to run: %w", err)
	}
	// This only works on windows if the process was not started
	defer func() {
		if binary != "" {
			_ = os.Remove(binary)
		}
	}()

	quitChild := make(chan struct{})
	attr.Files, err = children.setupChildFiles(cmd, quitChild)
	defer func() {
		for _, file := range attr.Files {
			file.Close()
		}
	}()
	if err != nil {
		close(quitChild)
		return err
	}

	if workingDir != "./" {
		attr.Dir = workingDir
	}
	argv := append([]string{binary}, cmd.Args...)

	// Start the process
	proc, err := os.StartProcess(binary, argv, attr)
	c := &child{
		Cmd:       cmd,
		Proc:      proc,
		Name:      name,
		Binary:    binary,
		quitChild: quitChild,
	}
	if err != nil {
		msg := fmt.Errorf("failed starting process: %v", err)
		if proc != nil && proc.Pid > 0 {
			c.Up = false
			c.Message = msg
			close(quitChild)
			children.Store(c)
		}
		return msg
	}
	c.Up = true
	children.Store(c)
	log.Printf("Started process: %v; pid: %v", name, proc.Pid)
	log.Printf("Args: %v", argv)

	if !*dontKillChildren && children.platform.successfulInit {
		err := children.addProcToJobObject(proc)
		if err != nil {
			log.Printf("Failed to setup killing child %v (pid: %v). %v",
				name, proc.Pid, err)
		}
	}

	if filepath.Base(cmd.Binary) == "portal" {
		log.Print("Waiting for portal API token...")
		token, err := children.waitForPortalToken()
		if err != nil {
			log.Printf("Did not receive portal token: %v", err)
		} else {
			gate.Token = &token
			log.Print("Token received.")
		}
	}
	log.Printf("Waiting %v...", *spawningDelay)
	time.Sleep(*spawningDelay)
	return nil
}

func (c *children) MonitorDeaths(quit chan struct{}) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-quit:
			return
		case <-ticker.C:
		}
		var downPids []int
		c.Lock()
		for pid, _ := range c.ByPID {
			handle, err := syscall.OpenProcess(syscall.SYNCHRONIZE, false, uint32(pid))
			if err != nil {
				log.Printf("Warning: couldn't get handle for pid %v: %v", pid, err)
				continue
			}
			event, err := syscall.WaitForSingleObject(handle, 0 /*milliseconds*/)
			if err != nil {
				log.Printf("Warning: couldn't check status of pid %v: %v", pid, err)
				continue
			}
			if event != syscall.WAIT_OBJECT_0 {
				continue // Process is still running
			}
			downPids = append(downPids, pid)
		}
		c.Unlock()
		// Has to be after Unlock because ReportDown locks again
		for _, pid := range downPids {
			c.ReportDown(pid, fmt.Errorf("Process died."))
		}
	}
}

func libraryPaths() []string {
	return nil
}

// On windows chown doesn't work so just make the directory
func makeOwnedDir(dir string, uid, gid uint32) error {
	err := os.Mkdir(dir, 0750)
	if err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	return nil
}

// Returns the new filepath of the binary (empty string if the file was not created)
// This is different on Windows because chown doesn't exist.
func copyFile(oldName string, newName string, uid, gid uint32, exclusive bool) (string, error) {
	oldf, err := os.Open(oldName)
	if err != nil {
		return "", err
	}
	defer oldf.Close()
	flags := os.O_RDWR | os.O_CREATE
	if exclusive {
		flags |= os.O_EXCL
	}
	newf, err := os.OpenFile(newName, flags, 0550)
	if err != nil {
		newf.Close()
		return newName, err
	}
	if _, err := io.Copy(newf, oldf); err != nil {
		newf.Close()
		return newName, err
	}
	// On windows chown doesn't work so just copy the file
	return newName, newf.Close()
}

// Windows doesn't support watching files on spawn
// TODO: try windows.FindFirstChangeNotification or windows.ReadDirectoryChanges
// thanks https://stackoverflow.com/a/17376701/428740
func openOrRefreshFiles(cmd *Command, quitFileRefresh <-chan struct{}) ([]*os.File, error) {
	if len(cmd.Files) == 0 {
		return nil, nil
	}
	return openFiles(cmd.Files)
}
