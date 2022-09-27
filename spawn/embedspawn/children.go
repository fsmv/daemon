package embedspawn

import (
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"ask.systems/daemon/tools"
)

type Child struct {
	Up              bool
	Message         error
	Name            string
	Cmd             *Command
	Proc            *os.Process
	quitFileRefresh chan struct{}
}

type Children struct {
	*sync.Mutex
	*logHandler
	// Note the PID map will contain all old instances of servers
	ByPID  map[int]*Child
	ByName map[string]*Child
}

func NewChildren(quit chan struct{}) *Children {
	c := &Children{
		&sync.Mutex{},
		NewLogHandler(quit),
		make(map[int]*Child),
		make(map[string]*Child),
	}
	r, w := io.Pipe()
	log.SetOutput(io.MultiWriter(log.Writer(), tools.NewTimestampWriter(w)))
	go c.HandleLogs(r, kLogsTag)
	return c
}

func (c *Children) StartPrograms(programs []*Command) (errCnt int) {
	errCnt = 0
	for i, cmd := range programs {
		err := c.StartProgram(cmd)
		if err != nil {
			log.Printf("Error in Command #%v: %v", i, err)
			errCnt++
			continue
		}
	}
	return errCnt
}

func (children *Children) StartProgram(cmd *Command) error {
	if len(cmd.Binary) == 0 {
		return fmt.Errorf("Binary is required")
	}
	name := cmd.FullName()
	log.Print("Starting ", name)
	// Set up stdout and stderr piping
	r, w, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("failed to create pipe: %v", err)
	}
	files := []*os.File{nil, w, w}
	// Takes a pointer so we can append to it and this will still see everything
	defer func(filesPtr *[]*os.File) {
		for _, file := range *filesPtr {
			file.Close()
		}
	}(&files)
	if len(cmd.Ports) != 0 {
		socketFiles, err := listenPortsTCP(cmd.Ports)
		if err != nil {
			return err
		}
		files = append(files, socketFiles...)
	}
	var quitFileRefresh chan struct{}
	if len(cmd.Files) != 0 {
		var openedFiles []*os.File
		var filesErr error
		if cmd.AutoTlsCerts {
			quitFileRefresh = make(chan struct{})
			refreshFiles, err := startFileRefresh(cmd.Files, quitFileRefresh)
			if err != nil {
				return err
			}
			files = append(files, refreshFiles...)
		} else {
			openedFiles, filesErr = openFiles(cmd.Files)
		}
		if filesErr != nil {
			return filesErr
		}
		files = append(files, openedFiles...)
	}
	attr := &os.ProcAttr{
		Env:   []string{""},
		Files: files,
	}

	if len(cmd.User) == 0 {
		return fmt.Errorf("you must specify a user to run as.")
	}
	if cmd.User == "root" {
		return fmt.Errorf("running as root is not allowed.")
	}
	// Set the user, group, and home dir if we're switching users
	u, err := user.Lookup(cmd.User)
	if err != nil {
		return fmt.Errorf("error while looking up user %v, message: %v",
			cmd.User, err)
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return fmt.Errorf("Uid string not an integer. Uid string: %v", u.Uid)
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return fmt.Errorf("Gid string not an integer. Gid string: %v", u.Gid)
	}
	groupsStr, err := u.GroupIds()
	if err != nil {
		return fmt.Errorf("Failed to lookup groups: %v", err)
	}
	var groups []uint32
	for i, group := range groupsStr {
		id, err := strconv.Atoi(group)
		if err != nil {
			return fmt.Errorf("Supplimental gid #%v string not an integer. Gid string: %v", i, id)
		}
		groups = append(groups, uint32(id))
	}
	attr.Sys = &syscall.SysProcAttr{
		Credential: &syscall.Credential{
			Uid:    uint32(uid),
			Gid:    uint32(gid),
			Groups: groups,
		},
	}
	if !*dontKillChildren {
		attr.Sys.Pdeathsig = syscall.SIGHUP
	}
	workingDir := cmd.WorkingDir
	if workingDir == "/" {
		// If we did allow it we would delete /etc/localtime!
		return fmt.Errorf("working_dir: \"/\" is not allowed.")
	}
	if workingDir == "" {
		workingDir = u.HomeDir
	}
	// Copy the binary into the home dir and give the user access
	binaryCopy, err := copyFile(cmd.Binary, filepath.Join(workingDir, name), uid, gid)
	// Don't leave a dangling binary copy
	defer func() {
		if binaryCopy != "" {
			_ = os.Remove(binaryCopy)
		}
	}()
	if err != nil {
		return fmt.Errorf("Failed to copy the binary into the working dir: %v", err)
	}
	var binpath string
	if cmd.NoChroot {
		attr.Dir = workingDir
		// The copy we'll run is at ~/binary
		binpath = binaryCopy
	} else { // Do a chroot
		attr.Dir = "/"
		attr.Sys.Chroot = workingDir
		// The copy we'll run is at /binary in the chroot
		binpath = "/" + filepath.Base(binaryCopy)
	}
	// Finalize the argv
	argv := append([]string{binpath}, cmd.Args...)

	// For chroots copy timezone info into the home dir and give the user access
	if !cmd.NoChroot {
		// TODO: There should be some kind of config for which files we copy in
		// Also maybe we should use symbolic or hard links
		err := os.Mkdir(filepath.Join(workingDir, "/etc/"), 0777)
		if err != nil {
			log.Printf("Failed to mkdir for /etc/localtime: %v", err)
		} else {
			timezoneFile, err := copyFile("/etc/localtime",
				filepath.Join(workingDir, "/etc/localtime"), uid, gid)
			// Don't leave a dangling file
			go func() {
				time.Sleep(30 * time.Second) // Give the binary time to load it
				if timezoneFile != "" {
					_ = os.Remove(timezoneFile)               // remove the file
					_ = os.Remove(filepath.Dir(timezoneFile)) // remove the etc folder
				}
			}()
			if err != nil {
				// Not worth returning over this, it will just be UTC log times
				log.Printf("Failed to copy /etc/localtime into the chroot dir: %v", err)
			}
		}
	}

	// Start the process
	proc, err := os.StartProcess(binpath, argv, attr)
	c := &Child{
		Cmd:             cmd,
		Proc:            proc,
		Name:            name,
		quitFileRefresh: quitFileRefresh,
	}
	go children.HandleLogs(r, name)
	if err != nil {
		msg := fmt.Errorf("failed starting process: %v", err)
		if proc != nil && proc.Pid > 0 {
			c.Up = false
			c.Message = msg
			close(quitFileRefresh)
			children.Store(c)
		}
		return msg
	}
	c.Up = true
	children.Store(c)
	log.Printf("Started process: %v; pid: %v", name, proc.Pid)
	log.Printf("Args: %v", argv)
	log.Printf("Waiting %v...", *spawningDelay)
	time.Sleep(*spawningDelay)
	return nil
}

func (c *Children) RestartChild(name string) {
	c.Lock()

	child, ok := c.ByName[name]
	if !ok {
		log.Print("Can't restart a child that was never started! Name: ", name)
		c.Unlock()
		return
	}
	proc := child.Proc
	if proc != nil {
		log.Print("Killing ", name)
		proc.Signal(syscall.SIGTERM)
	}
	cmd := child.Cmd // technically we should copy but we don't modify it

	c.Unlock()

	if proc != nil {
		proc.Wait()
		c.ReportDown(proc.Pid, fmt.Errorf("Killed for restart"))
		log.Print("Down after being killed: ", name)
	}
	err := c.StartProgram(cmd)
	if err != nil {
		log.Print("Failed to restart child: ", err)
	}
}

func (c *Children) ReloadConfig() {
	commands, err := ReadConfig(*configFilename)
	if err != nil {
		log.Print("Failed to reload config: ", err)
		return
	}
	c.Lock()
	defer c.Unlock()
	for _, cmd := range commands {
		name := cmd.FullName()
		child, ok := c.ByName[name]
		if ok {
			child.Cmd = cmd
		} else {
			log.Print("New server: ", name)
			c.unsafeStore(&Child{
				Cmd:  cmd,
				Name: name,
				Up:   false,
			})
		}
	}
}

func (c *Children) ReportDown(pid int, message error) {
	c.Lock()
	defer c.Unlock() // need it the whole time we modify child
	child, ok := c.ByPID[pid]
	if !ok {
		log.Printf("Got death message for unregistered child: %v", message)
		return
	}
	if !child.Up {
		log.Printf("Got death message for already dead child")
		return
	}
	child.Up = false
	child.Message = message
	if child.quitFileRefresh != nil {
		close(child.quitFileRefresh)
	}
	log.Printf("%v (pid: %v)\n\n%v", child.Cmd.Binary, pid, message)
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

func (c *Children) MonitorDeaths(quit chan struct{}) {
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
				if !status.Exited() {
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

func (c *Children) unsafeStore(child *Child) {
	c.ByPID[child.Proc.Pid] = child
	c.ByName[child.Name] = child
}

func (c *Children) Store(child *Child) {
	c.Lock()
	c.unsafeStore(child)
	c.Unlock()
}

func listenPortsTCP(ports []uint32) ([]*os.File, error) {
	var ret []*os.File
	for _, port := range ports {
		l, err := net.ListenTCP("tcp", &net.TCPAddr{Port: int(port)})
		if err != nil {
			return nil, fmt.Errorf("error listening on port (%v): %v",
				port, err)
		}
		f, err := l.File()
		if err != nil {
			return nil, fmt.Errorf("error listening on port (%v): %v",
				port, err)
		}
		ret = append(ret, f)
		l.Close()
	}
	return ret, nil
}

func openFiles(files []string) ([]*os.File, error) {
	var ret []*os.File
	for _, fileName := range files {
		f, err := os.Open(fileName)
		if err != nil {
			return nil, fmt.Errorf("error opening file %#v: %w", fileName, err)
		}
		ret = append(ret, f)
	}
	return ret, nil
}

// Returns the new filepath of the binary (empty string if the file was not created)
func copyFile(oldName string, newName string, uid, gid int) (string, error) {
	oldf, err := os.Open(oldName)
	if err != nil {
		return "", err
	}
	defer oldf.Close()
	newf, err := os.OpenFile(newName, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0550)
	if err != nil {
		newf.Close()
		return newName, err
	}
	if _, err := io.Copy(newf, oldf); err != nil {
		newf.Close()
		return newName, err
	}
	if err := newf.Chown(uid, gid); err != nil {
		newf.Close()
		return newName, err
	}
	return newName, newf.Close()
}
