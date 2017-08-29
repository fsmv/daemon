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

const goPrefix = "go/"

type Command struct {
    // Paths starting with go/ are Go packages
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
        // Set up stdout and stderr piping
        r, w, err := os.Pipe()
        if err != nil {
            log.Printf("%v: Error creating pipe: %v", cmd.Path, err)
            continue
        }
        go io.Copy(os.Stdout, r)
        attr := &os.ProcAttr{
            Env: []string{""},
            Files: []*os.File{nil, w, w},
        }
        // Set the user, group and home dir if there's a user
        if cmd.User != "" {
            u, err := user.Lookup(cmd.User)
            if err != nil {
                log.Printf("%v: Error looking up user %v, message: %v",
                    cmd.Path, cmd.User, err)
                continue
            }
            uid, err := strconv.Atoi(u.Uid)
            if err != nil {
                log.Printf("%v: Uid string not an integer, this is not linux." +
                    " Uid string: %v", cmd.Path, u.Uid)
            }
            gid, err := strconv.Atoi(u.Gid)
            if err != nil {
                log.Printf("%v: Gid string not an integer, this is not linux." +
                    " Gid string: %v", cmd.Path, u.Gid)
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
        log.Printf("Started process: %v; pid: %v", cmd.Path, proc.Pid)
        if cmd.WaitTime != 0 {
            log.Printf("Waiting %v for process startup...", cmd.WaitTime)
            time.Sleep(cmd.WaitTime)
        }
    }
    return ret, errCnt
}

func ResolveGoPaths(commands []*Command) error {
    gopath := os.Getenv("GOPATH")
    if gopath == "" {
        return errors.New("GOPATH environment variable not set")
    }
    binpath := filepath.Join(gopath, "bin")
    for _, cmd := range commands {
        if !strings.HasPrefix(cmd.Path, goPrefix) {
            continue
        }
        cmd.Path = filepath.Join(binpath, cmd.Path[len(goPrefix):])
    }
    return nil
}

func main() {
    Programs := []*Command{
        &Command{
            Path: "go/feproxy",
            WaitTime: time.Second,
        },
        &Command{
            Path: "go/moneyserv",
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
