//go:build unix

package embedspawn

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"ask.systems/daemon/portal/gate"
)

// Wrap os.StartProcess for linux only to make child killing work right
//
// If the dont_kill_children flag isn't set, on Linux we use pdeathsig to
// kill child processes when spawn exits. The way pdeathsig works is that if
// the thread, not the process, that started the child exits then the child
// is sent the signal. So we have to keep the thread alive.
// See https://github.com/golang/go/issues/27505
//
// I have tested it and FreeBSD does not share this behavior.
func startProcess(name string, argv []string, attr *os.ProcAttr) (*os.Process, error) {
	if runtime.GOOS != "linux" {
		return os.StartProcess(name, argv, attr)
	}

	procChan := make(chan *os.Process, 0)
	errChan := make(chan error, 0)
	defer close(procChan)
	defer close(errChan)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		proc, err := os.StartProcess(name, argv, attr)
		if err != nil {
			errChan <- err
			return
		}
		procChan <- proc
		proc.Wait()
	}()
	select {
	case proc := <-procChan:
		return proc, nil
	case err := <-errChan:
		return nil, err
	}
}

func (children *children) StartProgram(cmd *Command) error {
	if len(cmd.Binary) == 0 {
		return fmt.Errorf("Binary is required")
	}
	isPortal := filepath.Base(cmd.Binary) == "portal"
	name := cmd.FullName()
	attr := &os.ProcAttr{
		Env: []string{
			fmt.Sprintf("SPAWN_FILES=%v", len(cmd.Files)),
			fmt.Sprintf("SPAWN_PORTS=%v", len(cmd.Ports)),
		},
	}
	err := gate.ResolveFlags()
	if err == nil {
		attr.Env = append(attr.Env, fmt.Sprintf("PORTAL_ADDR=%v", *gate.Address))
		attr.Env = append(attr.Env, fmt.Sprintf("PORTAL_TOKEN=%v", *gate.Token))
	}
	if !cmd.NoChroot {
		// TODO: we don't put all libraries in /lib/ anymore. We use the paths we
		// found them in. Is this even necessary?
		attr.Env = append(attr.Env, "LD_LIBRARY_PATH=/lib/")
	}
	log.Print("Starting ", name)
	attr.Sys = &syscall.SysProcAttr{}

	creds, u, err := lookupUser(cmd.User)
	if err != nil {
		return err
	}
	if u.Username == "root" {
		return fmt.Errorf("running as root is not allowed.")
	}
	if cmd.User != "" {
		attr.Sys.Credential = creds
	}

	if !*dontKillChildren {
		maybeSetDeathSig(attr)
	}
	workingDir := cmd.WorkingDir
	if workingDir == "/" {
		// If we did allow it we would delete /etc/localtime!
		return fmt.Errorf("working_dir: \"/\" is not allowed.")
	}
	if workingDir == "" {
		workingDir = u.HomeDir
	}

	binary, err := copyBinary(cmd.Binary, filepath.Join(workingDir, name), true /*exclusive*/, creds.Uid, creds.Gid)
	if err != nil {
		return fmt.Errorf("Failed to setup the binary to run: %w", err)
	}

	if cmd.NoChroot {
		// Just remove the binary, we don't copy over anything extra in this case
		defer func() {
			if binary != "" {
				_ = os.Remove(binary)
			}
		}()
	} else {
		// Setup the dynamic libs in the chroot
		var libFiles []string

		libs, interp, err := requiredLibs(children.libPaths, binary)
		if err != nil {
			_ = os.Remove(binary)
			return fmt.Errorf("Failed to lookup dynamic libraries: %w", err)
		}
		for lib, _ := range libs {
			libFiles = append(libFiles, lib)
		}
		for lib, _ := range interp {
			libFiles = append(libFiles, lib)
		}

		copiedLibs, libErr := chrootFiles(creds.Uid, creds.Gid, workingDir, libFiles)

		// Don't leave dangling chroot files
		defer func() {
			if binary != "" {
				_ = os.Remove(binary)
			}
			for _, lib := range copiedLibs {
				_ = os.Remove(lib)
			}
		}()

		// Wait to return so we delete any libs that didn't error
		if libErr != nil {
			return fmt.Errorf("Failed to copy libraries for %v: %w", name, libErr)
		}
	}

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

	var binpath string
	if cmd.NoChroot {
		if workingDir != "./" {
			attr.Dir = workingDir
		}
		// The copy we'll run is at ~/binary
		binpath = binary
	} else { // Do a chroot
		attr.Dir = "/"
		attr.Sys.Chroot = workingDir
		// The copy we'll run is at /binary in the chroot
		binpath = "/" + filepath.Base(binary)
	}
	// Finalize the argv
	argv := append([]string{binpath}, cmd.Args...)

	// For chroots copy timezone info into the home dir and give the user access
	var chrootedFiles []string
	if !cmd.NoChroot {
		// TODO: There should be some kind of config for which files we copy in
		chrootedFiles, err = chrootFiles(creds.Uid, creds.Gid, workingDir, []string{
			"/etc/localtime",
			"/etc/resolv.conf",
			"/etc/ssl/",
		})
		if err != nil {
			log.Print("Warning, some chroot files were not successful:\n", err)
		}
	}

	// Start the process
	proc, err := startProcess(binpath, argv, attr)
	c := &child{
		Cmd:         cmd,
		Proc:        proc,
		Name:        name,
		ChrootFiles: chrootedFiles,
		quitChild:   quitChild,
		Binary:      binary,
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

	if isPortal {
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

func makeOwnedDir(dir string, uid, gid uint32) error {
	err := os.Mkdir(dir, 0750)
	if err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	err = os.Chown(dir, int(uid), int(gid))
	if err != nil {
		os.Remove(dir)
		return err
	}
	return nil
}

// Returns the new filepath of the binary (empty string if the file was not created)
func copyFile(oldName string, newName string, uid, gid uint32, exclusive bool) (string, error) {
	// TODO: we could call os.Link to hardlink the files but it breaks if the
	// source file is a symbolic link
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
	if err := newf.Chown(int(uid), int(gid)); err != nil {
		newf.Close()
		return newName, err
	}
	return newName, newf.Close()
}

func requiredLibs(paths []string, filename string) (libs map[string]struct{}, interp map[string]struct{}, err error) {
	libs = make(map[string]struct{})
	interp = make(map[string]struct{})
	err = requiredLibsImpl(paths, filename, libs, interp)
	return
}

func lookupUser(username string) (*syscall.Credential, *user.User, error) {
	// Set the user, group, and home dir if we're switching users
	var u *user.User
	var err error
	if username == "" {
		u, err = user.Current()
	} else {
		u, err = user.Lookup(username)
	}
	if err != nil {
		return nil, u, fmt.Errorf("error while looking up user %v, message: %v",
			username, err)
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return nil, u, fmt.Errorf("Uid string not an integer. Uid string: %v", u.Uid)
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return nil, u, fmt.Errorf("Gid string not an integer. Gid string: %v", u.Gid)
	}
	groupsStr, err := u.GroupIds()
	if err != nil {
		return nil, u, fmt.Errorf("Failed to lookup groups: %v", err)
	}
	var groups []uint32
	for i, group := range groupsStr {
		id, err := strconv.Atoi(group)
		if err != nil {
			return nil, u, fmt.Errorf("Supplemental gid #%v string not an integer. Gid string: %v", i, id)
		}
		groups = append(groups, uint32(id))
	}
	return &syscall.Credential{
		Uid:    uint32(uid),
		Gid:    uint32(gid),
		Groups: limitGroupsForMac(groups),
	}, u, nil
}

func openOrRefreshFiles(cmd *Command, quitFileRefresh <-chan struct{}) ([]*os.File, error) {
	if len(cmd.Files) == 0 {
		return nil, nil
	}
	var openedFiles []*os.File
	var err error
	isPortal := filepath.Base(cmd.Binary) == "portal"
	if cmd.AutoTlsCerts || isPortal {
		openedFiles, err = startFileRefresh(cmd.Files, quitFileRefresh)
	} else {
		openedFiles, err = openFiles(cmd.Files)
	}
	return openedFiles, err
}

func makeDeadChildMessage(status syscall.WaitStatus,
	resUsage syscall.Rusage) error {
	return fmt.Errorf(
		"Process died.\n"+
			"CPU Time: %d.%03d s (user); %d.%03d s (system)\n"+
			"Max Resident Set Size: %v Kb\n"+
			"Page faults: %v\n"+
			"Context Switches: %v (voluntary); %v (involuntary)\n",
		resUsage.Utime.Sec, resUsage.Utime.Usec/1000,
		resUsage.Stime.Sec, resUsage.Stime.Usec/1000,
		resUsage.Maxrss, resUsage.Majflt,
		resUsage.Nvcsw, resUsage.Nivcsw)
}

func (c *children) MonitorDeaths(quit chan struct{}) {
	child := make(chan os.Signal, 16)
	signal.Notify(child, syscall.SIGCHLD)
	for {
		select {
		case <-child:
			var status syscall.WaitStatus
			var resUsage syscall.Rusage
			for {
				pid, err := syscall.Wait4(-1, &status, syscall.WNOHANG, &resUsage)
				if pid == 0 || err == syscall.ECHILD || err == syscall.EINTR {
					// ECHILD means we have no children
					// EINTR means an interrupt handler happened while we were waiting
					break
				}
				if err != nil {
					log.Printf("Error checking child status: pid = %v; error = %v", pid, err)
					continue
				}
				c.ReportDown(pid, makeDeadChildMessage(status, resUsage))
			}
		case <-quit:
			signal.Stop(child)
			close(child)
			return
		}
	}
}
