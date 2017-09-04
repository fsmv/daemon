package main

import (
    "os"
    "os/signal"
    "fmt"
    "daemon/feproxy/proxyserv"
    "log"
    "time"
    "net/http"
    "net/rpc"
    "strconv"
)

func main() {
    sigs := make(chan os.Signal, 2)
    signal.Notify(sigs, os.Interrupt, os.Kill)

    client, err := rpc.Dial("tcp", "127.0.0.1:2048")
    if err != nil {
        log.Fatalf("Failed to connect to frontend proxy RPC server: %v", err)
    }
    defer client.Close()

    var lease proxyserv.Lease
    err = client.Call("feproxy.Register", "/test/", &lease)
    if err != nil {
        log.Fatal("Failed to obtain lease from feproxy:", err)
    }
    log.Printf("Obtained lease, port: %v, ttl: %v", lease.Port, lease.TTL)

    defer client.Call("feproxy.Deregister", "/test/", &lease)
    go func() {
        <-sigs
        client.Call("feproxy.Deregister", "/test/", &lease)
        log.Print("Shutting down...")
        // TODO(1.8): use http.Server and call close here
        os.Exit(0)
    }()

    http.HandleFunc("/test/", func(w http.ResponseWriter, r *http.Request) {
        fmt.Fprint(w, "<body><h3>Hello, World!</h3>")
        fmt.Fprint(w, "Your url: ", r.URL.Path)
        fmt.Fprint(w, "</body>")
    })
    http.HandleFunc("/test/quit", func(w http.ResponseWriter, r *http.Request) {
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
