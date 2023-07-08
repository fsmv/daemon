package embedspawn

import (
	"bufio"
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
	"sort"
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
	ChrootFiles     []string
	quitFileRefresh chan struct{}
}

type children struct {
	*sync.Mutex
	*logHandler
	// Note the PID map will contain all old instances of servers
	ByPID  map[int]*child
	ByName map[string]*child

	libPaths []string
}

func newChildren(quit chan struct{}) *children {
	c := &children{
		&sync.Mutex{},
		newLogHandler(quit),
		make(map[int]*child),
		make(map[string]*child),
		libraryPaths(),
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

	// Copy the binary into the home dir and give the user access.
	// Do it even if not in a chroot so we make sure it is accessable to the user.
	binary, openerr := copyFile(cmd.Binary, filepath.Join(workingDir, name), creds.Uid, creds.Gid, true /*exclusive*/)

	// If it's a megabinary, and there wasn't a user-provided binary load the
	// command from the megabinary if it's there
	if openerr != nil && len(MegabinaryCommands) > 0 && errors.Is(openerr, fs.ErrNotExist) {
		testCommand := filepath.Base(cmd.Binary)
		for _, supported := range MegabinaryCommands {
			if testCommand != supported {
				continue
			}
			megabinary, err := os.Executable()
			if err != nil {
				return fmt.Errorf("Could not find current executable: %v", err)
			}
			binary, openerr = copyFile(megabinary, filepath.Join(workingDir, name), creds.Uid, creds.Gid, true /*exclusive*/)
			break
		}
	}

	// TODO: in the edge case that cmd.Binary and workingDir/name are the exact
	// same file, we want to allow the copy the fail (or skip the copy) and not
	// delete the binary.
	//
	// For example: this can happen if you use spawn and portal as separate
	// binaries and use working_dir: "./" for portal (i.e. the example config).
	//
	// The work-around would be to use a sub-directory for the binaries
	if openerr != nil {
		return fmt.Errorf("Failed to setup the binary to run: %w", openerr)
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
	go children.HandleLogs(r, name)
	proc, err := os.StartProcess(binpath, argv, attr)
	c := &child{
		Cmd:             cmd,
		Proc:            proc,
		Name:            name,
		ChrootFiles:     chrootedFiles,
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
	logs, cancel := c.StreamLogs(true /*includeHistory*/)
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

	for _, file := range child.ChrootFiles {
		os.Remove(file)
	}
	child.ChrootFiles = nil

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

func chrootFiles(uid, gid uint32, root string, filenames []string) ([]string, error) {
	newFilesMap := make(map[string]struct{})
	var rerr error
	for _, filename := range filenames {
		stat, err := os.Stat(filename)
		if err != nil {
			rerr = errors.Join(rerr, err)
			continue
		}

		// Make all the parent directories inside the chroot
		var dirs []string
		for dir := filepath.Dir(filename); dir != "/" && dir != "."; dir = filepath.Dir(dir) {
			newDir := filepath.Join(root, dir)
			dirs = append(dirs, newDir)
		}
		var direrr error
		// We have to go in reverse order so we make parent dirs first
		for i := len(dirs) - 1; i >= 0; i-- {
			newDir := dirs[i]
			direrr = makeOwnedDir(newDir, uid, gid)
			if direrr != nil {
				rerr = errors.Join(rerr, direrr)
				break
			}
			newFilesMap[newDir] = struct{}{}
		}
		if direrr != nil {
			continue
		}

		if stat.IsDir() {
			err := copyDirectory(filename, filepath.Join(root, filename), uid, gid, newFilesMap)
			if err != nil {
				rerr = errors.Join(rerr, err)
				continue
			}
		} else { // Just chroot a single file
			newName, err := copyFile(filename, filepath.Join(root, filename), uid, gid, false /*exclusive*/)
			if newName != "" {
				newFilesMap[newName] = struct{}{}
			}
			if err != nil {
				rerr = errors.Join(rerr, err)
				continue
			}
		}
	}
	newFiles := make([]string, 0, len(newFilesMap))
	for newFile, _ := range newFilesMap {
		newFiles = append(newFiles, newFile)
	}
	// Sort so we remove files before directories
	sort.Slice(newFiles, func(i, j int) bool {
		return newFiles[i] > newFiles[j]
	})
	return newFiles, rerr
}

func copyDirectory(dir, newDir string, uid, gid uint32, copiedFiles map[string]struct{}) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var rerr error
	for _, entry := range entries {
		if entry.IsDir() {
			name := entry.Name()
			newSubdir := filepath.Join(newDir, name)
			err := makeOwnedDir(newSubdir, uid, gid)
			if err != nil {
				rerr = errors.Join(rerr, err)
				continue
			}
			copiedFiles[newSubdir] = struct{}{}
			err = copyDirectory(filepath.Join(dir, name), newSubdir, uid, gid, copiedFiles)
			if err != nil {
				rerr = errors.Join(rerr, err)
			}
			continue
		}
		name := entry.Name()
		newName, err := copyFile(
			filepath.Join(dir, name),
			filepath.Join(newDir, name),
			uid, gid, false /*exclusive*/)
		if newName != "" {
			copiedFiles[newName] = struct{}{}
		}
		if err != nil {
			rerr = errors.Join(rerr, err)
			continue
		}
	}
	return rerr
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

func requiredLibs(paths []string, filename string) (libs map[string]struct{}, interp map[string]struct{}, err error) {
	libs = make(map[string]struct{})
	interp = make(map[string]struct{})
	err = requiredLibsImpl(paths, filename, libs, interp)
	return
}

func readLibPaths(filename string) ([]string, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	var ret []string
	lines := bufio.NewScanner(f)
	for lines.Scan() {
		line := strings.TrimSpace(lines.Text())

		if strings.HasPrefix(line, "/") {
			if comment := strings.IndexRune(line, '#'); comment != -1 {
				line = strings.TrimSpace(line[:comment])
			}
			ret = append(ret, line)
			continue
		}

		const include = "include "
		if strings.HasPrefix(line, include) {
			confs, err := filepath.Glob(line[len(include):])
			if err != nil {
				return ret, err
			}
			for _, conf := range confs {
				paths, err := readLibPaths(conf)
				if err != nil {
					return ret, err
				}
				ret = append(ret, paths...)
			}
			continue
		}
	}
	if err := lines.Err(); err != nil {
		return ret, err
	}
	return ret, nil
}

func libraryPaths() []string {
	defaultPaths := []string{
		"/lib64", "/usr/lib64",
		"/lib", "/usr/lib",
	}
	paths, err := readLibPaths("/etc/ld.so.conf")
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		log.Printf("Warning: failed to parse /etc/ld.so.conf paths: %v", err)
	}
	return append(defaultPaths, paths...)
}
