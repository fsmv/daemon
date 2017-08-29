package main

import (
    "errors"
    "strings"
    "fmt"
    "log"
    "sync"
    "time"
    "os"
    "os/signal"
    "syscall"
    "path/filepath"
)

const goPrefix = "go/"

type Command struct {
    // Paths starting with go/ are Go packages
    Path        string
    Args        []string
    Uid         uint32
    Gid         uint32
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
        attr := &os.ProcAttr{ Env: []string{""} }
        if cmd.Uid != 0 || cmd.Gid != 0 {
            attr.Sys = &syscall.SysProcAttr{
                Credential: &syscall.Credential{
                    Uid: cmd.Uid,
                    Gid: cmd.Gid,
                },
            }
        }
        proc, err := os.StartProcess(cmd.Path, cmd.Args, attr)
        if err != nil {
            log.Printf("Error starting process: %v", err)
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
            WaitTime: 2 * time.Second,
        },
        &Command{
            Path: "go/moneyserv",
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
