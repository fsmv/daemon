package main

import (
    "bytes"
    "os"
    "os/signal"
    "fmt"
    "flag"
    "log"
    "time"
    "net/http"
    "net/rpc"
    "strings"
    "strconv"
    "sync"

    "daemon/feproxy/proxyserv"
)

var (
    csvbuf  bytes.Buffer
    counter int
    mut     sync.Mutex

    oauthClientId = flag.String("client_id", "",
        "The OAuth2 Client ID")
    oauthClientSecret = flag.String("client_secret", "",
        "The OAuth2 Client Secret")
)

func addNRows(n int) error {
    mut.Lock()
    defer mut.Unlock()
    for n += counter; counter < n; counter++ {
        row := fmt.Sprintf("%v,%v\n", counter, counter*counter)
        _, err := csvbuf.WriteString(row)
        if err != nil {
            return err
        }
    }
    return nil
}

func getCSV() []byte {
    mut.Lock()
    defer mut.Unlock()
    return csvbuf.Bytes()
}

const register = "/"

func main() {
    flag.Parse()
    sigs := make(chan os.Signal, 2)
    signal.Notify(sigs, os.Interrupt, os.Kill)

    client, err := rpc.Dial("tcp", "127.0.0.1:2048")
    if err != nil {
        log.Fatalf("Failed to connect to frontend proxy RPC server: %v", err)
    }
    defer client.Close()

    var lease proxyserv.Lease
    err = client.Call("feproxy.Register", register, &lease)
    if err != nil {
        log.Fatal("Failed to obtain lease from feproxy:", err)
    }
    log.Printf("Obtained lease, port: %v, ttl: %v", lease.Port, lease.TTL)

    defer client.Call("feproxy.Deregister", register, &lease)
    go func() {
        <-sigs
        client.Call("feproxy.Deregister", register, &lease)
        log.Print("Shutting down...")
        // TODO(1.8): use http.Server and call close here
        os.Exit(0)
    }()

    InitSheetsUpdater(*oauthClientId, *oauthClientSecret)

    http.HandleFunc("/data.csv", func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "text/csv")
        w.Write(getCSV())
    })
    http.HandleFunc("/add/", func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "text/html")
        fmt.Fprint(w, "<html><body><h2>\n")
        defer fmt.Fprint(w, "</h2></body></html>\n")
        path := r.URL.Path
        lastSlash := strings.LastIndex(path, "/")
        if lastSlash != len(path) - 1{
            num, err := strconv.Atoi(path[lastSlash+1:])
            if err != nil {
                fmt.Fprint(w, "Error, should use /add/{number}")
                return
            }
            addNRows(num)
        } else {
            addNRows(1)
        }
    })
    http.HandleFunc("/quit", func(w http.ResponseWriter, r *http.Request) {
        fmt.Fprint(w, "<body><h3>Shutting down!</h3></body>")
        time.AfterFunc(time.Second, func () {
            client.Call("feproxy.Quit", struct{}{}, struct{}{})
            sigs <- os.Kill
        })
    })

    log.Print("Starting server...")
    // TODO(1.8): check for err == ErrServerClosed
    log.Fatal(http.ListenAndServe(":" + strconv.Itoa(int(lease.Port)), nil))
}
