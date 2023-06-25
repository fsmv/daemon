package embedspawn

import (
	"debug/elf"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"os"
	"os/signal"
	"os/user"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	_ "ask.systems/daemon/portal/flags"
	"ask.systems/daemon/portal/gate"
	"ask.systems/daemon/tools"
)

// MegabinaryCommands is the list of commands that spawn should use as
// sub-commands of the binary running spawn. The main [ask.systems/daemon]
// binary sets this so spawn can run commands from it.
var MegabinaryCommands []string

type child struct {
	Up              bool
	Message         error
	Name            string
	Cmd             *Command
	Proc            *os.Process
	quitFileRefresh chan struct{}
}

type children struct {
	*sync.Mutex
	*logHandler
	// Note the PID map will contain all old instances of servers
	ByPID  map[int]*child
	ByName map[string]*child
}

func newChildren(quit chan struct{}) *children {
	c := &children{
		&sync.Mutex{},
		newLogHandler(quit),
		make(map[int]*child),
		make(map[string]*child),
	}
	r, w := io.Pipe()
	log.SetOutput(io.MultiWriter(log.Writer(), tools.NewTimestampWriter(w)))
	go c.HandleLogs(r, kLogsTag)
	return c
}

func (c *children) StartPrograms(programs []*Command) (errCnt int) {
	// Make portal first if it exists
	portalIdx := 0
	for i, cmd := range programs {
		if path.Base(cmd.Binary) != "portal" {
			continue
		}
		portalIdx = i
		break
	}
	programs[0], programs[portalIdx] = programs[portalIdx], programs[0]

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

func (children *children) StartProgram(cmd *Command) error {
	if len(cmd.Binary) == 0 {
		return fmt.Errorf("Binary is required")
	}
	isPortal := path.Base(cmd.Binary) == "portal"
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
	var binary string
	var openerr error
	if cmd.NoChroot {
		binf, err := os.Open(cmd.Binary)
		binf.Close()
		openerr = err
		binary = cmd.Binary
	} else {
		// Copy the binary into the home dir and give the user access
		binary, openerr = chrootFile(cmd.Binary, filepath.Join(workingDir, name), creds.Uid, creds.Gid)
	}
	// If it's a megabinary, and there wasn't a user-provided binary load the
	// command from the megabinary if it's there
	subcommand := ""
	if openerr != nil && len(MegabinaryCommands) > 0 && errors.Is(openerr, fs.ErrNotExist) {
		testCommand := filepath.Base(cmd.Binary)
		for _, supported := range MegabinaryCommands {
			if testCommand != supported {
				continue
			}
			subcommand = testCommand
			if cmd.NoChroot {
				binary = os.Args[0]
				openerr = nil
			} else {
				binary, openerr = chrootFile(os.Args[0], filepath.Join(workingDir, name), creds.Uid, creds.Gid)
			}
			break
		}
	}
	if openerr != nil {
		return fmt.Errorf("Failed to setup the binary to run: %w", openerr)
	}

	// Setup the dynamic libs in the chroot
	if !cmd.NoChroot {
		var libErr error
		var copiedLibs []string
		libs, interp, err := requiredLibs(binary)
		if err != nil {
			//_ = os.Remove(binary)
			return fmt.Errorf("Failed to lookup dynamic libraries: %w", err)
		}
		for lib, _ := range libs {
			newLib, err := chrootFile(lib,
				filepath.Join(workingDir, "lib", filepath.Base(lib)),
				creds.Uid, creds.Gid)
			if err != nil {
				libErr = fmt.Errorf("Failed to make %#v available in chroot: %w", lib, err)
				break
			}
			copiedLibs = append(copiedLibs, newLib)
		}
		if libErr == nil {
			for lib, _ := range interp {
				newLib, err := chrootFile(lib,
					filepath.Join(workingDir, lib),
					creds.Uid, creds.Gid)
				if err != nil {
					/*if errors.Is(err, fs.ErrExist) {
						continue
					}*/
					libErr = fmt.Errorf("Failed to make %#v available in chroot: %w", lib, err)
					break
				}
				copiedLibs = append(copiedLibs, newLib)
			}
		}

		// Don't leave dangling chroot files
		defer func() {
			if binary != "" {
				_ = os.Remove(binary)
			}
			for _, lib := range copiedLibs {
				_ = os.Remove(lib)
			}
			_ = os.Remove(filepath.Join(workingDir, "lib"))
			// Remove all parent directories of interp libs, there might be multiple
			for lib, _ := range interp {
				for path := filepath.Dir(lib); path != "/"; path = filepath.Dir(path) {
					_ = os.Remove(filepath.Join(workingDir, path))
				}
			}
		}()

		// Wait to return so we delete any libs that didn't error
		if libErr != nil {
			return libErr
		}
	}
	// Set up stdout and stderr piping
	r, w, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("failed to create pipe: %v", err)
	}
	// Setup the file descriptors we will pass to the child
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
		if cmd.AutoTlsCerts || isPortal {
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
	attr.Files = files

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
	var startArgs []string
	if subcommand != "" && cmd.NoChroot {
		// if we are using a subcommand and we didn't copy the binary, then we need
		// to specify the subcommand on the commandline. In chroot mode we just
		// change the name of the binary to the subcommand and it checks argv[0].
		// TODO: this could be cleaned up, maybe ditch the argv[0] thing
		startArgs = []string{binpath, subcommand}
	} else {
		startArgs = []string{binpath}
	}
	argv := append(startArgs, cmd.Args...)

	// For chroots copy timezone info into the home dir and give the user access
	if !cmd.NoChroot {
		// TODO: There should be some kind of config for which files we copy in
		// Also maybe we should use symbolic or hard links.
		// I need to standardize the time it stays there too somehow. Maybe it can
		// stay forever with hard links? Not sure what the best thing is.
		// We probably at least need /etc/resolv.conf as well.
		err := os.Mkdir(filepath.Join(workingDir, "/etc/"), 0777)
		if err != nil {
			log.Printf("Warning: failed to mkdir for /etc/localtime: %v", err)
		}
		timezoneFile, err := chrootFile("/etc/localtime",
			filepath.Join(workingDir, "/etc/localtime"), creds.Uid, creds.Gid)
		if err != nil {
			// Not worth returning over this, it will just be UTC log times
			log.Printf("Warning: failed to copy /etc/localtime into the chroot dir: %v", err)
		}
		// Don't leave a dangling file
		go func() {
			time.Sleep(30 * time.Second) // Give the binary time to load it
			if timezoneFile != "" {
				err = os.Remove(timezoneFile) // remove the file
				if err != nil {
					log.Printf("Warning: failed to remove /etc/localtime file in chroot: %v", err)
				}
				err = os.Remove(filepath.Dir(timezoneFile)) // remove the etc folder
				if err != nil {
					log.Printf("Warning: failed to remove /etc/ dir in chroot: %v", err)
				}
			}
		}()
	}

	// Start the process
	go children.HandleLogs(r, name)
	proc, err := os.StartProcess(binpath, argv, attr)
	c := &child{
		Cmd:             cmd,
		Proc:            proc,
		Name:            name,
		quitFileRefresh: quitFileRefresh,
	}
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

	if isPortal {
		log.Print("Waiting for portal API token...")
		token, err := children.waitForPortalToken()
		if err != nil {
			log.Printf("Did not receive portal token: %v", err)
		} else {
			gate.Token = &token
			log.Print("Token received.")
		}
	} else {
		log.Printf("Waiting %v...", *spawningDelay)
		time.Sleep(*spawningDelay)
	}
	return nil
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
			return nil, u, fmt.Errorf("Supplimental gid #%v string not an integer. Gid string: %v", i, id)
		}
		groups = append(groups, uint32(id))
	}
	return &syscall.Credential{
		Uid:    uint32(uid),
		Gid:    uint32(gid),
		Groups: groups,
	}, u, nil
}

func (c *children) waitForPortalToken() (string, error) {
	logs, cancel := c.StreamLogs()
	defer cancel()
	ttl := time.After(10 * time.Second)
	for {
		select {
		case <-ttl:
			return "", errors.New("Deadline exceeded")
		case line := <-logs:
			if line.Tag != "portal" {
				continue
			}
			const prefix = "Portal API token: "
			if idx := strings.Index(line.Line, prefix); idx == -1 {
				continue
			} else {
				token := line.Line[idx+len(prefix):]
				endIdx := strings.IndexRune(token, ' ')
				if endIdx == -1 {
					return "", fmt.Errorf(
						"Failed to parse portal token line: %#v", line.Line)
				}
				token = token[:endIdx]
				return token, nil
			}
		}
	}
}

func (cmd *Command) FullName() string {
	name := filepath.Base(cmd.Binary)
	if cmd.Name != "" {
		name = fmt.Sprintf("%v-%v", name, cmd.Name)
	}
	return name
}

func (c *children) RestartChild(name string) {
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
	}
	err := c.StartProgram(cmd)
	if err != nil {
		log.Print("Failed to restart child: ", err)
	}
}

func (c *children) ReloadConfig() {
	commands, err := ReadConfig(*configFilename)
	if err != nil {
		log.Print("Failed to reload config: ", err)
		return
	}
	c.Lock()
	defer c.Unlock()
	for _, cmd := range commands {
		name := cmd.FullName()
		cmdChild, ok := c.ByName[name]
		if ok {
			cmdChild.Cmd = cmd
		} else {
			log.Print("New server: ", name)
			c.unsafeStore(&child{
				Cmd:  cmd,
				Name: name,
				Up:   false,
			})
		}
	}
}

func (c *children) ReportDown(pid int, message error) {
	c.Lock()
	defer c.Unlock() // need it the whole time we modify child
	child, ok := c.ByPID[pid]
	if !ok {
		log.Printf("Got death message for unregistered child: %v", message)
		return
	}
	// Don't accumulate old Child structs in the ByPID map forever, we will still
	// have it in the ByName map until it gets reloaded then the GC will delete it
	delete(c.ByPID, pid)
	child.Up = false
	child.Message = message
	if child.quitFileRefresh != nil {
		close(child.quitFileRefresh)
	}
	if strings.Index(message.Error(), "\n") != -1 {
		log.Printf("%v (pid: %v) died:\n\n%v", child.Cmd.Binary, pid, message)
	} else {
		log.Printf("%v (pid: %v) died: %v", child.Cmd.Binary, pid, message)
	}
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

func (c *children) unsafeStore(child *child) {
	if child.Proc != nil {
		c.ByPID[child.Proc.Pid] = child
	}
	c.ByName[child.Name] = child
}

func (c *children) Store(child *child) {
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
func chrootFile(oldName string, newName string, uid, gid uint32) (string, error) {
	dir := filepath.Dir(newName)
	err := os.MkdirAll(dir, 0750)
	if err != nil {
		return "", err
	}
	for path := dir; path != "/"; path = filepath.Dir(path) {
		err = os.Chown(path, int(uid), int(gid))
		if err != nil {
			return "", err
		}
	}
	// TODO: we could call os.Link to hardlink the files but it breaks if the
	// source file is a symbolic link
	log.Printf("Failed to hardlink %#v into chroot trying to copy instead.", oldName)
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
	if err := newf.Chown(int(uid), int(gid)); err != nil {
		newf.Close()
		return newName, err
	}
	return newName, newf.Close()
}

func requiredLibs(filename string) (libs map[string]struct{}, interp map[string]struct{}, err error) {
	libs = make(map[string]struct{})
	interp = make(map[string]struct{})
	err = requiredLibsImpl(filename, libs, interp)
	return
}

func requiredLibsImpl(filename string, libs map[string]struct{}, interp map[string]struct{}) error {
	f, err := os.Open(filename)
	if err != nil {
		return err
	}
	bin, err := elf.NewFile(f)
	if err != nil {
		return err
	}

	// Read the path to the ld shared library which is also needed
	for _, prog := range bin.Progs {
		if prog.Type != elf.PT_INTERP {
			continue
		}
		interpData := make([]byte, prog.Filesz-1) // -1 to cut off the \0 on the end
		_, err := prog.ReadAt(interpData, 0)
		if err != nil {
			return fmt.Errorf("Failed to read interp data from elf: %w", err)
		}
		interp[string(interpData)] = struct{}{}
		break
	}

	// Read the libraries used by the binary (loaded by the interp)
	imports, err := bin.ImportedLibraries()
	if err != nil {
		return err
	}
	for _, lib := range imports {
		rootLib := filepath.Join("/lib", lib)
		usrLib := filepath.Join("/usr/lib", lib)
		if _, ok := libs[rootLib]; ok {
			continue
		}
		if _, ok := libs[usrLib]; ok {
			continue
		}
		foundLib := rootLib
		err = requiredLibsImpl(rootLib, libs, interp)
		if errors.Is(err, fs.ErrNotExist) {
			foundLib = usrLib
			err = requiredLibsImpl(usrLib, libs, interp)
		}
		if err != nil {
			return err
		}
		libs[foundLib] = struct{}{}
	}
	return nil
}
