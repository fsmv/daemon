package main

import (
    "encoding/json"
    "strconv"
    "fmt"
    "flag"
    "net"
    "log"
    "sync"
    "time"
    "io"
    "os"
    "os/signal"
    "os/user"
    "syscall"
    "path/filepath"
)

var (
    configFilename = flag.String("config", "config",
        "The path to the config file")
    path = flag.String("path", "",
        "A single path to use for relative paths in the config file")
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
}

type Child struct {
    Up      bool
    Message string
    Cmd     *Command
    Proc    *os.Process
}

func makeDeadChildMessage(status syscall.WaitStatus,
                          resUsage syscall.Rusage) string {
    return fmt.Sprintf(
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

func MonitorChildrenDeaths(quit chan struct{},
    reportDown func (pid int, message string)) {

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
                    continue
                }
                if err != nil {
                    log.Printf("Error checking child status: " +
                        "pid = %v; error = %v", pid, err)
                    continue
                }
                if !status.Exited() {
                    continue
                }
                reportDown(pid, makeDeadChildMessage(status, resUsage))
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

func StartPrograms(programs []Command) (map[int]*Child, int) {
    var errCnt int = 0
    ret := make(map[int]*Child)
    for i, cmd := range programs {
        if len(cmd.Filepath) == 0 {
            log.Printf("Error in Command #%v, Filepath is required", i)
            errCnt++
            continue
        }
        name := filepath.Base(cmd.Filepath)
        // Set up stdout and stderr piping
        // TODO: don't do pipes at all, just read the child's logfile
        r, w, err := os.Pipe()
        if err != nil {
            log.Printf("%v: Error, failed to create pipe: %v", name, err)
            errCnt++
            continue
        }
        files := []*os.File{nil, w, w}
        if len(cmd.Ports) != 0 {
            socketFiles, err := listenPortsTCP(cmd.Ports)
            if err != nil {
                log.Printf("%v: %v", name, err)
                continue
            }
            files = append(files, socketFiles...)
        }
        if len(cmd.Files) != 0 {
            openedFiles, err := openFiles(cmd.Files)
            if err != nil {
                log.Printf("%v: %v", name, err)
                continue
            }
            files = append(files, openedFiles...)
        }
        // TODO: not a permanant solution, could have race condition issues.
        go io.Copy(os.Stdout, r)
        attr := &os.ProcAttr{
            Env: []string{""},
            Files: files,
        }

        if len(cmd.User) == 0 {
            log.Printf("%v: Error, you must specify a user to run as", name)
            errCnt++
            continue
        }
        if cmd.User == "root" {
            log.Printf("%v: Error, root is not allowed", name)
            errCnt++
            continue
        }
        // Set the user, group, and home dir if we're switching users
        u, err := user.Lookup(cmd.User)
        if err != nil {
            log.Printf("%v: Error looking up user %v, message: %v",
                name, cmd.User, err)
            errCnt++
            continue
        }
        uid, err := strconv.Atoi(u.Uid)
        if err != nil {
            log.Fatal("Uid string not an integer. Uid string: %v", u.Uid)
        }
        gid, err := strconv.Atoi(u.Gid)
        if err != nil {
            log.Fatal("Gid string not an integer. Gid string: %v", u.Gid)
        }
        attr.Dir = u.HomeDir
        attr.Sys = &syscall.SysProcAttr{
            Credential: &syscall.Credential{
                Uid: uint32(uid),
                Gid: uint32(gid),
            },
        }
        // Finalize the argv
        var jsonArgs []string
        for argName, value := range cmd.JsonArgs {
            argVal, err := json.Marshal(value)
            if err != nil {
                log.Printf("%v: Error in json arg %v, message: %v",
                    name, argName, err)
                errCnt++
                break
            }
            jsonArgs = append(jsonArgs, fmt.Sprintf("--%v=%v", argName, string(argVal)))
        }
        if len(jsonArgs) != len(cmd.JsonArgs) {
            continue // There was an error parsing json args
        }
        argv := append([]string{cmd.Filepath}, cmd.Args...)
        argv = append(argv, jsonArgs...)
        // Start the process
        proc, err := os.StartProcess(cmd.Filepath, argv, attr)
        c := &Child{
            Cmd: &cmd,
            Proc: proc,
        }
        if err != nil {
            log.Printf("%v: Error starting process: %v", cmd.Filepath, err)
            errCnt++
            if proc != nil && proc.Pid > 0 {
                c.Up = false
                c.Message = fmt.Sprintf("Error starting process: %v", err)
                ret[proc.Pid] = c
            }
            continue
        }
        ret[proc.Pid] = c
        c.Up = true
        log.Printf("Started process: %v; pid: %v", name, proc.Pid)
        log.Printf("Args: %v", argv)
        log.Printf("Waiting 1 second...")
        time.Sleep(time.Second)
    }
    return ret, errCnt
}

func ResolveRelativePaths(path string, commands []Command) error {
    for i, _ := range commands {
        cmd := &commands[i]
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

func readConfig(filename string) ([]Command, error) {
    f, err := os.Open(filename)
    if err != nil {
        return nil, err
    }
    var ret []Command
    dec := json.NewDecoder(f)
    for dec.More() {
        var cmd Command
        err = dec.Decode(&cmd)
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
    sigs := make(chan os.Signal, 2)
    signal.Notify(sigs, os.Interrupt, os.Kill)
    go func() {
        <-sigs
        close(quit)
    }()

    var children map[int]*Child
    childrenMut := &sync.RWMutex{}
    go MonitorChildrenDeaths(quit, func (pid int, message string) {
        childrenMut.Lock()
        defer childrenMut.Unlock()
        child, ok := children[pid]
        if !ok {
            log.Printf("Got death message for unregistered child: %v", message)
            return
        }
        child.Up = false
        child.Message = message
        log.Printf("%v (pid: %v)\n\n%v",
            children[pid].Cmd.Filepath, pid, message)
    })
    // Mutex to make the death message handler wait for data about the children
    childrenMut.Lock()
    children, errcnt := StartPrograms(commands)
    childrenMut.Unlock()
    if errcnt != 0 {
        log.Printf("%v errors occurred in spawning", errcnt)
    }

    <-quit
}
