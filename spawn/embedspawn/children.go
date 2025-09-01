package embedspawn

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	_ "ask.systems/daemon/portal/flags"
	"ask.systems/daemon/tools"
)

// MegabinaryCommands is the list of commands that spawn should use as
// sub-commands of the binary running spawn. The main [ask.systems/daemon]
// binary sets this so spawn can run commands from it.
var MegabinaryCommands []string

type child struct {
	Up          bool
	Message     error
	Name        string
	Cmd         *Command
	Proc        *os.Process
	ChrootFiles []string
	Binary      string
	quitChild   chan struct{}
}

type children struct {
	*sync.Mutex
	*logHandler
	// Note the PID map will contain all old instances of servers
	ByPID  map[int]*child
	ByName map[string]*child

	libPaths []string

	platform platformSpecificChildrenInfo
}

func newChildren(quit chan struct{}) *children {
	c := &children{
		&sync.Mutex{},
		newLogHandler(quit),
		make(map[int]*child),
		make(map[string]*child),
		libraryPaths(),
		platformSpecificChildrenInfo{},
	}
	// Capture spawn's logs
	r, w := io.Pipe()
	log.SetOutput(io.MultiWriter(log.Writer(), tools.NewTimestampWriter(w)))
	go c.HandleLogs(r, kLogsTag)
	c.platform.Init(c)
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

func (c *children) waitForPortalToken() (string, error) {
	logs, cancel := c.StreamLogs(true /*includeHistory*/)
	defer cancel()
	ttl := time.After(120 * time.Second)
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

	// On windows you can't delete a binary that is being run so we have to do it
	// when we know the process has died
	if runtime.GOOS == "windows" {
		os.Remove(child.Binary)
	}

	// Don't accumulate old Child structs in the ByPID map forever, we will still
	// have it in the ByName map until it gets reloaded then the GC will delete it
	delete(c.ByPID, pid)
	child.Up = false
	child.Message = message
	if child.quitChild != nil {
		close(child.quitChild)
	}
	if strings.Index(message.Error(), "\n") != -1 {
		log.Printf("%v (pid: %v) died:\n\n%v", child.Cmd.Binary, pid, message)
	} else {
		log.Printf("%v (pid: %v) died: %v", child.Cmd.Binary, pid, message)
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
		defer l.Close()
		f, err := l.File()
		if err != nil {
			return nil, fmt.Errorf("error getting FD for port (%v): %v",
				port, err)
		}
		ret = append(ret, f)
	}
	return ret, nil
}

func openFiles(files []string) ([]*os.File, error) {
	var ret []*os.File
	for _, fileName := range files {
		f, err := os.Open(fileName)
		if err != nil {
			return ret, fmt.Errorf("error opening file %#v: %w", fileName, err)
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

// TODO: in the edge case that cmd.Binary and workingDir/name are the exact
// same file, we want to allow the copy the fail (or skip the copy) and not
// delete the binary.
//
// For example: this can happen if you use spawn and portal as separate
// binaries and use working_dir: "./" for portal (i.e. the example config).
//
// The work-around would be to use a sub-directory for the binaries
func copyBinary(bin, out string, exclusive bool, uid, gid uint32) (string, error) {
	// Copy the binary into the home dir and give the user access.
	// Do it even if not in a chroot so we make sure it is accessable to the user.
	binary, openerr := copyFile(bin, out, uid, gid, exclusive)

	// If it's a megabinary, and there wasn't a user-provided binary load the
	// command from the megabinary if it's there
	if openerr != nil && len(MegabinaryCommands) > 0 && errors.Is(openerr, fs.ErrNotExist) {
		testCommand := filepath.Base(bin)
		for _, supported := range MegabinaryCommands {
			if testCommand != supported {
				continue
			}
			megabinary, err := os.Executable()
			if err != nil {
				return "", fmt.Errorf("Could not find current executable: %v", err)
			}
			binary, openerr = copyFile(megabinary, out, uid, gid, exclusive)
			break
		}
	}
	return binary, openerr
}

func (children *children) setupChildFiles(cmd *Command, quit chan struct{}) ([]*os.File, error) {
	// Set up stdout and stderr piping
	r, w, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create pipe for logs: %v", err)
	}
	inr, inw, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create pipe for stdin: %v", err)
	}
	inw.Close()
	// Setup the file descriptors we will pass to the child
	files := []*os.File{inr, w, w}

	if len(cmd.Ports) != 0 {
		socketFiles, err := listenPortsTCP(cmd.Ports)
		if err != nil {
			return files, err
		}
		files = append(files, socketFiles...)
	}

	openedFiles, err := openOrRefreshFiles(cmd, quit)
	files = append(files, openedFiles...)
	if err != nil {
		return files, err
	}
	go children.HandleLogs(r, cmd.FullName())
	return files, nil
}
