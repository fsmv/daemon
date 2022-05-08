package main

import (
  "os"
  "io"
  "fmt"
  "log"
  "net"
  "flag"
  "sync"
  "time"
  "bufio"
  "strconv"
  "syscall"
  "os/user"
  "os/signal"
  "path/filepath"
  "encoding/json"

  "ask.systems/daemon/tools"
)

const (
  kLogLinesBufferSize = 1024 // Shared across all servers
  kSubscriptionChannelSize = kLogLinesBufferSize
  kPublishChannelSize = 32
)

var (
  configFilename = flag.String("config", "config",
    "The path to the config file")
  path = flag.String("path", "",
    "A single path to use for relative paths in the config file")
  spawningDelay = flag.Duration("spawning_delay", 2*time.Second,
    "The amount of time to wait between starting processes.\n" +
    "Useful especially for feproxy which should go first and be given time\n" +
    "to start up so others can connect.")
)

// Command is one executable to run with options
type Command struct {
  // Filepath is the absolute path to the executable file or the relative
  // path within the directory provided in the --path flag.
  //
  // Required.
  Filepath    string
  // User to run the process as. Cannot be root.
  //
  // Required.
  User        string
  // Args is the arguments to pass to the executable
  Args        []string
  JsonArgs    map[string]interface{}
  // Ports to listen on (with tcp) and pass to the process as files.
  // Useful for accessing the privelaged ports (<1024).
  //
  // In the child process, the sockets will have fd = 3 + i, where Ports[i] is
  // the port to bind
  Ports       []uint16
  // Files to open and pass to the process
  //
  // In the child process, the files will have fd = 3 + len(Ports) + i, where
  // Files[i] is the file
  Files       []string
  // Set to true if you don't want a chroot to the home dir, which is the
  // default
  NoChroot    bool
  // If unset, cd and/or chroot into $HOME, otherwise use this directory
  WorkingDir  string
}

type Child struct {
  Up      bool
  Message error
  Name    string
  Cmd     *Command
  Proc    *os.Process
}

type Children struct {
  *sync.Mutex
  *logHandler
  // Note the PID map will contain all old instances of servers
  ByPID  map[int]*Child
  ByName map[string]*Child
}

func NewChildren(quit chan struct{}) *Children {
  return &Children {
    &sync.Mutex{},
    NewLogHandler(quit),
    make(map[int]*Child),
    make(map[string]*Child),
  }
}

func (c *Children) Store(child *Child) {
  c.Lock()
  c.ByPID[child.Proc.Pid] = child
  c.ByName[child.Name] = child
  c.Unlock()
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
    proc.Kill()
  }
  cmd := child.Cmd // technically we should copy but we don't modify it

  c.Unlock()

  if proc != nil {
    proc.Wait()
    c.ReportDown(proc.Pid, fmt.Errorf("Killed for restart"))
  }
  c.StartProgram(cmd)
}

func (c *Children) ReportDown(pid int, message error) {
  c.Lock()
  defer c.Unlock() // need it the whole time we modify child
  child, ok := c.ByPID[pid]
  if !ok {
    log.Printf("Got death message for unregistered child: %v", message)
    return
  }
  child.Up = false
  child.Message = message
  log.Printf("%v (pid: %v)\n\n%v", child.Cmd.Filepath, pid, message)
}

type  LogMessage struct {
  Line string
  Tag string
}

// Not thread safe
type ringBuffer struct {
  buffer [kLogLinesBufferSize]LogMessage
  nextLine int
  filled bool
}

func (r *ringBuffer) Push(line LogMessage) {
  r.buffer[r.nextLine] = line
  r.nextLine++
  if r.nextLine == len(r.buffer) {
    r.filled = true
    r.nextLine = 0
  }
}

func (r *ringBuffer) Copy() []LogMessage {
  var length int
  if r.filled {
    length = len(r.buffer)
  } else {
    length = r.nextLine
  }
  ret := make([]LogMessage, length)
  if !r.filled || r.nextLine == 0 {
    copy(ret, r.buffer[:])
  } else {
    n := copy(ret, r.buffer[r.nextLine:])
    copy(ret[n:], r.buffer[:r.nextLine])
  }
  return ret
}

type logHandler struct {
  logLines ringBuffer
  // Broadcasting system
  quit chan struct{}
  publish chan LogMessage
  subscribe chan chan<- LogMessage
  subscribers map[chan<- LogMessage]struct{}
}

func NewLogHandler(quit chan struct{}) *logHandler {
  h := &logHandler{
    quit: quit,
    subscribers: make(map[chan<- LogMessage]struct{}),
    publish: make(chan LogMessage, kPublishChannelSize),
    subscribe: make(chan chan<- LogMessage),
  }
  go h.run()
  return h
}

// Broadcasts all the lines sent to the publish channel from HandleLogs to all
// of the subscribers
func (h *logHandler) run() {
  for {
    select {
    case <-h.quit:
      return
    case sub := <-h.subscribe:
      if _, ok := h.subscribers[sub]; ok {
        delete(h.subscribers, sub)
      } else {
        // New subscribers get the history buffer
        lines := h.logLines.Copy()
        for _, line := range lines {
          sub <- line
        }

        h.subscribers[sub] = struct{}{}
      }
    case m := <-h.publish:
      h.logLines.Push(m)
      for sub, _ := range h.subscribers {
        // TODO: maybe timeout or non-blocking with select default
        sub <- m
      }
    }
  }
}

// Publish a logs file or pipe to all of the subscribers of the handler
func (h *logHandler) HandleLogs(logs *os.File, tag string) {
  defer logs.Close()
  r := bufio.NewReader(logs)
  for {
    line, err := r.ReadString('\n')
    if err != nil {
      log.Print("Failed reading logs: ", err)
      return
    }
    fmt.Printf("%v: %v", tag, line) // for running spawn on commandline and not using syslog
    h.publish <- LogMessage{Line: line, Tag: tag}
    select {
    case <-h.quit:
      return
    default:
    }
  }
}

func (h *logHandler) StreamLogs() (stream <-chan LogMessage, cancel func()) {
  sub := make(chan LogMessage, kSubscriptionChannelSize)
  h.subscribe <- sub
  return sub, func() {
    h.subscribe <- sub
    close(sub)
  }
}

func makeDeadChildMessage(status syscall.WaitStatus,
                          resUsage syscall.Rusage) error {
  return fmt.Errorf(
    "Process died.\n" +
    "CPU Time: %d.%03d s (user); %d.%03d s (system)\n" +
    "Max Resident Set Size: %v Kb\n" +
    "Page faults: %v\n" +
    "Context Switches: %v (voluntary); %v (involuntary)\n",
    resUsage.Utime.Sec, resUsage.Utime.Usec/1000,
    resUsage.Stime.Sec, resUsage.Stime.Usec/1000,
    resUsage.Maxrss, resUsage.Majflt,
    resUsage.Nvcsw, resUsage.Nivcsw)
}

func (c *Children) MonitorDeaths(quit chan struct{}) {
  child := make(chan os.Signal)
  signal.Notify(child, syscall.SIGCHLD)
  for {
    select{
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

func listenPortsTCP(ports []uint16) ([]*os.File, error) {
    var ret []*os.File
    for _, port := range ports {
        l, err := net.ListenTCP("tcp", &net.TCPAddr{Port:int(port)})
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
    }
    return ret, nil
}

func openFiles(files []string) ([]*os.File, error) {
    var ret []*os.File
    for _, fileName := range files {
        f, err := os.Open(fileName)
        if err != nil {
            return nil, fmt.Errorf("error opening file (%v): %v",
                fileName, err)
        }
        ret = append(ret, f)
    }
    return ret, nil
}

// Returns the new filepath of the binary
func copyBinary(oldName string, newDir string, uid, gid int) (string, error) {
  newName := filepath.Join(newDir, filepath.Base(oldName))
  oldf, err := os.Open(oldName)
  if err != nil {
    return newName, err
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
  if len(cmd.Filepath) == 0 {
    return fmt.Errorf("Filepath is required")
  }
  name := filepath.Base(cmd.Filepath)
  log.Print("Starting ", name)
  // Set up stdout and stderr piping
  r, w, err := os.Pipe()
  if err != nil {
    return fmt.Errorf("failed to create pipe: %v", err)
  }
  files := []*os.File{nil, w, w}
  if len(cmd.Ports) != 0 {
    socketFiles, err := listenPortsTCP(cmd.Ports)
    if err != nil {
      return err
    }
    files = append(files, socketFiles...)
  }
  if len(cmd.Files) != 0 {
    openedFiles, err := openFiles(cmd.Files)
    if err != nil {
      return err
    }
    files = append(files, openedFiles...)
  }
  attr := &os.ProcAttr{
    Env: []string{""},
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
    /*if id == gid {
      continue
    }*/
    groups = append(groups, uint32(id))
  }
  attr.Sys = &syscall.SysProcAttr{
    Credential: &syscall.Credential{
      Uid: uint32(uid),
      Gid: uint32(gid),
      Groups: groups,
    },
  }
  workingDir := cmd.WorkingDir
  if workingDir == "" {
    workingDir = u.HomeDir
  }
  // Copy the binary into the home dir and give the user access
  binaryCopy, err := copyBinary(cmd.Filepath, workingDir, uid, gid)
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
    binpath = "/"+filepath.Base(cmd.Filepath)
  }
  // Finalize the argv
  var jsonArgs []string
  for argName, value := range cmd.JsonArgs {
    argVal, err := json.Marshal(value)
    if err != nil {
      return fmt.Errorf("error in json arg %v, message: %v",
        argName, err)
    }
    jsonArgs = append(jsonArgs, fmt.Sprintf("--%v=%v", argName, string(argVal)))
  }
  argv := append([]string{binpath}, cmd.Args...)
  argv = append(argv, jsonArgs...)

  // Start the process
  proc, err := os.StartProcess(binpath, argv, attr)
  c := &Child{
    Cmd: cmd,
    Proc: proc,
    Name: name,
  }
  go children.HandleLogs(r, name)
  if err != nil {
    msg := fmt.Errorf("failed starting process: %v", err)
    if proc != nil && proc.Pid > 0 {
      c.Up = false
      c.Message = msg
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

func ResolveRelativePaths(path string, commands []*Command) error {
    for i, _ := range commands {
        cmd := commands[i]
        if len(cmd.Filepath) == 0 || cmd.Filepath[0] == '/' {
            continue
        }
        if len(path) == 0 { // Don't error unless there's actually a go path
            return fmt.Errorf(
                "--path flag not set which is required by Command #%v, " +
                "filepath: %v", i, cmd.Filepath)
        }
        cmd.Filepath = filepath.Join(path, cmd.Filepath)
    }
    return nil
}

func readConfig(filename string) ([]*Command, error) {
    f, err := os.Open(filename)
    if err != nil {
        return nil, err
    }
    var ret []*Command
    // TODO: make a reader wrapper that skips comments
    dec := json.NewDecoder(f)
    for dec.More() {
        cmd := new(Command)
        err = dec.Decode(cmd)
        if err != nil {
            return nil, fmt.Errorf(
                "parsing error. filepath: %v, command #%v, error: \"%v\"",
                filename, len(ret)+1, err)
        }
        ret = append(ret, cmd)
    }
    if len(ret) == 0 {
        return nil, fmt.Errorf(
            "no commands found in config file. filepath: %v", filename)
    }
    return ret, nil
}

func main() {
    flag.Parse()
    commands, err := readConfig(*configFilename)
    if err != nil {
        log.Fatalf("Failed to read config file. error: \"%v\"", err)
    }

    err = ResolveRelativePaths(*path, commands)
    if err != nil {
        log.Fatal(err)
    }

    quit := make(chan struct{})
    tools.CloseOnSignals(quit)

    children := NewChildren(quit)
    go children.MonitorDeaths(quit)
    // Mutex to make the death message handler wait for data about the children
    errcnt := children.StartPrograms(commands)
    if errcnt != 0 {
        log.Printf("%v errors occurred in spawning", errcnt)
    }
    if _, err := StartDashboard(children, quit); err != nil {
        log.Print("Failed to start dashboard: ", err)
        // TODO: retry it? Also check the dashboardQuit signal for retries
    }

    <-quit
    log.Print("Goodbye.")
}
