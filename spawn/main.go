package main

import (
    "errors"
    "strings"
    "strconv"
    "fmt"
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

type Command struct {
    // Paths that don't start with '/' are Go packages
    Path        string
    Args        []string
    User        string
    WaitTime    time.Duration
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

func MonitorChildren(quit chan struct{},
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
                if pid == 0 {
                    break
                }
                if err != nil {
                    log.Printf("Error checking child status: %v", err)
                    break
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

func StartPrograms(programs []*Command) (map[int]*Child, int) {
    var errCnt int = 0
    ret := make(map[int]*Child)
    for _, cmd := range programs {
        name := filepath.Base(cmd.Path)
        // Set up stdout and stderr piping
        r, w, err := os.Pipe()
        if err != nil {
            log.Printf("%v: Warning, failed to create pipe: %v", name, err)
            continue
        }
        // TODO: not a permanant solution, could have race condition issues.
        go io.Copy(os.Stdout, r)
        attr := &os.ProcAttr{
            Env: []string{""},
            Files: []*os.File{nil, w, w},
        }
        if len(cmd.User) == 0 {
            log.Print("%v: Error, you must specify a user to run as", name)
            errCnt++
            continue
        }
        // Set the user, group, and home dir if we're switching users
        if cmd.User != "root" {
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
        }
        // Start the process
        argv := append([]string{cmd.Path}, cmd.Args...)
        proc, err := os.StartProcess(cmd.Path, argv, attr)
        if err != nil {
            log.Printf("%v: Error starting process: %v", cmd.Path, err)
            errCnt++
            if proc != nil && proc.Pid > 0 {
                ret[proc.Pid] = &Child{
                    Up: false,
                    Cmd: cmd,
                    Proc: proc,
                    Message: err.Error(),
                }
            }
            continue
        }
        ret[proc.Pid] = &Child{
            Up: true,
            Cmd: cmd,
            Proc: proc,
        }
        log.Printf("Started process: %v; pid: %v", name, proc.Pid)
        if cmd.WaitTime != 0 {
            log.Printf("Waiting %v for process startup...", cmd.WaitTime)
            time.Sleep(cmd.WaitTime)
        }
    }
    return ret, errCnt
}

func ResolveGoPaths(commands []*Command) error {
    gopath := os.Getenv("GOPATH")
    binpath := filepath.Join(gopath, "bin")
    for _, cmd := range commands {
        if len(cmd.Path) == 0 || cmd.Path[0] == '/' {
            continue
        }
        if len(gopath) == 0 { // Don't error unless there's actually a go path
            return errors.New("GOPATH environment variable not set")
        }
        cmd.Path = filepath.Join(binpath, cmd.Path)
    }
    return nil
}

func main() {
    Programs := []*Command{
        &Command{
            Path: "feproxy",
            WaitTime: time.Second,
            User: "root",
        },
        &Command{
            Path: "moneyserv",
            User: "moneyserv",
        },
    }
    err := ResolveGoPaths(Programs)
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

    children, _ := StartPrograms(Programs)
    childrenMut := &sync.RWMutex{}
    go MonitorChildren(quit, func (pid int, message string) {
        childrenMut.Lock()
        defer childrenMut.Unlock()
        children[pid].Message = message
        log.Printf("%v\n\n%v", children[pid].Cmd.Path, message)
    })

    <-quit
}
