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

  _ "embed"
  _ "ask.systems/daemon/tools/flags"

  "ask.systems/daemon/tools"
  "google.golang.org/protobuf/encoding/prototext"
)

const (
  kLogLinesBufferSize = 256 // Per tag
  kSubscriptionChannelSize = 5*kLogLinesBufferSize
  kPublishChannelSize = 32
)

var (
  configFilename = flag.String("config", "config.pbtxt",
    "The path to the config file")
  path = flag.String("path", "",
    "A single path to use for relative paths in the config file")
  spawningDelay = flag.Duration("spawning_delay", 2*time.Second,
    "The amount of time to wait between starting processes.\n" +
    "Useful especially for feproxy which should go first and be given time\n" +
    "to start up so others can connect.")
)

//go:embed config.proto
var configSchema string

func init() {
  flag.Var(tools.BoolFuncFlag(func(string) error {
      fmt.Print(configSchema)
      os.Exit(2)
      return nil
    }), "config_schema",
    "Print the config schema in proto format, for reference, and exit.")
}

func (cmd *Command) FullName() string {
  name := filepath.Base(cmd.Filepath)
  if cmd.Name != "" {
    name = fmt.Sprintf("%v-%v", name, cmd.Name)
  }
  return name
}

type Child struct {
  Up      bool
  Message error
  Name    string
  Cmd     *Command
  Proc    *os.Process
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
  c := &Children {
    &sync.Mutex{},
    NewLogHandler(quit),
    make(map[int]*Child),
    make(map[string]*Child),
  }
  r, w := io.Pipe()
  log.SetOutput(io.MultiWriter(log.Writer(), tools.NewTimestampWriter(w)))
  go c.HandleLogs(r, "spawn")
  return c
}

func (c *Children) Store(child *Child) {
  c.Lock()
  c.unsafeStore(child)
  c.Unlock()
}

func (c *Children) unsafeStore(child *Child) {
  c.ByPID[child.Proc.Pid] = child
  c.ByName[child.Name] = child
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
        Cmd: cmd,
        Name: name,
        Up: false,
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
  child.Up = false
  child.Message = message
  if child.quitFileRefresh != nil {
    close(child.quitFileRefresh)
  }
  log.Printf("%v (pid: %v)\n\n%v", child.Cmd.Filepath, pid, message)
}

type  LogMessage struct {
  Line string
  Tag string
}

// Not thread safe
type ringBuffer struct {
  buffer [kLogLinesBufferSize]string
  nextLine int
  filled bool
}

func (r *ringBuffer) Push(line string) {
  r.buffer[r.nextLine] = line
  r.nextLine++
  if r.nextLine == len(r.buffer) {
    r.filled = true
    r.nextLine = 0
  }
}

// Not thread safe, simultaneous push will break it
func (r *ringBuffer) Write(out chan<- LogMessage, tag string) {
  if r.filled {
    for _, line := range r.buffer[r.nextLine:] {
      out <- LogMessage{Line: line, Tag: tag}
    }
  }
  for _, line := range r.buffer[:r.nextLine] {
    out <- LogMessage{Line: line, Tag: tag}
  }
}

type logHandler struct {
  logLines map[string]*ringBuffer
  // Broadcasting system
  quit chan struct{}
  publish chan LogMessage
  subscribe chan chan<- LogMessage
  subscribers map[chan<- LogMessage]struct{}
}

func NewLogHandler(quit chan struct{}) *logHandler {
  h := &logHandler{
    quit: quit,
    logLines: make(map[string]*ringBuffer),
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
        for tag, log := range h.logLines {
          log.Write(sub, tag)
        }

        h.subscribers[sub] = struct{}{}
      }
    case m := <-h.publish:
      // Make a new ring buffer if needed and push to the buffer
      log, ok := h.logLines[m.Tag]
      if !ok {
        log = &ringBuffer{}
        h.logLines[m.Tag] = log
      }
      log.Push(m.Line)

      // Push to subscribers
      for sub, _ := range h.subscribers {
        // TODO: maybe timeout or non-blocking with select default
        //   - if we send id: int after the data in the SSE stream then after
        //     reconnecting it will send a Last-Event-ID header so we can
        //     restart from the place we stopped at
        sub <- m
      }
    }
  }
}

// Publish a logs file or pipe to all of the subscribers of the handler
func (h *logHandler) HandleLogs(logs io.ReadCloser, tag string) {
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

func listenPortsTCP(ports []uint32) ([]*os.File, error) {
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

type refreshFile struct {
  writePipe *os.File
  fileName string
}

type fileRefresher []refreshFile

func (f fileRefresher) refreshOnSignal(quit chan struct{}) {
  sigs := make(chan os.Signal, 1)
  sigs<-syscall.SIGUSR1 // trigger the initial run (buffered)
  signal.Notify(sigs, syscall.SIGUSR1)
  // Close in a goroutine because we might block on writing to the pipe and we
  // need to close it asynchronously to unblock that this parent goroutine
  go func () {
    <-quit
    close(sigs)
    for _, file := range f {
      file.writePipe.Close()
    }
  }()
  for {
    select {
    case <-quit:
      return
    case <-sigs:
      log.Print("Starting TLS certificate refresh...")
      for _, refresh := range f {
        dataFile, err := os.Open(refresh.fileName)
        if err != nil {
          log.Print("Error opening file for refresh %#v: %w", refresh.fileName, err)
          dataFile.Close()
          continue
        }
        if _, err := io.Copy(refresh.writePipe, dataFile); err != nil {
          log.Print("Failed to refresh file on write to the OS pipe for %#v: %w",
            refresh.fileName, err)
        }
        refresh.writePipe.WriteString("\x04") // EOT
        refresh.writePipe.Sync()
        dataFile.Close()
        continue
      }
      log.Print("Successfully refreshed TLS certificate...")
    }
  }
}

func startFileRefresh(files []string, quit chan struct{}) ([]*os.File, error) {
  var ret []*os.File

  var refresher fileRefresher
  for _, fileName := range files {
    // Test if we can open the file
    f, err := os.Open(fileName)
    f.Close()
    if err != nil {
      return nil, fmt.Errorf("error opening file %#v: %w", fileName, err)
    }

    r, w, err := os.Pipe()
    if err != nil {
      return nil, fmt.Errorf("failed to create OS pipe to refresh file %#v: %w",
        fileName, err)
    }
    ret = append(ret, r)
    refresher = append(refresher, refreshFile{
      writePipe: w,
      fileName: fileName,
    })
  }
  go refresher.refreshOnSignal(quit)
  log.Print("Started -auto_tls_certs pipe")
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
    if cmd.AutoTlsCerts && len(cmd.Files) >= 2 {
      quitFileRefresh = make(chan struct{})
      refreshFiles, err := startFileRefresh(cmd.Files[:2], quitFileRefresh)
      if err != nil {
        return err
      }
      files = append(files, refreshFiles...)
      openedFiles, filesErr = openFiles(cmd.Files[2:])
    } else {
      openedFiles, filesErr = openFiles(cmd.Files)
    }
    if filesErr != nil {
      return filesErr
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
  binaryCopy, err := copyFile(cmd.Filepath, filepath.Join(workingDir, name), uid, gid)
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
    binpath = "/"+filepath.Base(binaryCopy)
  }
  // Finalize the argv
  argv := append([]string{binpath}, cmd.Args...)

  // For chroots copy timezone info into the home dir and give the user access
  if !cmd.NoChroot {
    err := os.Mkdir(filepath.Join(workingDir, "/etc/"), 0777)
    if err != nil {
      log.Printf("Failed to mkdir for /etc/localtime: %v", err)
    } else {
      timezoneFile, err := copyFile("/etc/localtime",
        filepath.Join(workingDir, "/etc/localtime"), uid, gid)
      // Don't leave a dangling file
      go func() {
        time.Sleep(30*time.Second) // Give the binary time to load it
        if timezoneFile != "" {
          _ = os.Remove(timezoneFile) // remove the file
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
    Cmd: cmd,
    Proc: proc,
    Name: name,
    quitFileRefresh: quitFileRefresh,
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

func ReadConfig(filename string) ([]*Command, error) {
  configText, err := os.ReadFile(filename)
  if err != nil {
    return nil, err
  }
  config := &Config{}
  if err := prototext.Unmarshal(configText, config); err != nil {
    return nil, err
  }
  err = ResolveRelativePaths(*path, config.Command)
  return config.Command, err
}

func main() {
    flag.Parse()
    commands, err := ReadConfig(*configFilename)
    if err != nil {
        log.Fatalf("Failed to read config file. error: \"%v\"", err)
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
