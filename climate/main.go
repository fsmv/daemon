package main

import (
    "io"
    "os"
    "os/signal"
    "fmt"
    "flag"
    "log"
    "time"
    "net/http"
    "net/rpc"
    "strconv"

    "daemon/feproxy/proxyserv"
)

const register = "/climate"

type Point struct {
    x, y float32
}
type Polyline []Point

func (l Polyline) Format(f fmt.State, c rune) {
    if len(l) < 2 {
        fmt.Fprint(f, "<!-- error, not enough points -->\n")
        return
    }
    firstPoint := l[0]
    fmt.Fprintf(f, "<polyline fill=\"none\" stroke=\"black\" points=\"%f %f",
        firstPoint.x, firstPoint.y)
    for _, point := range l[1:] {
        fmt.Fprintf(f, ", %f %f", point.x, point.y)
    }
    fmt.Fprint(f, "\"/>\n")
}

func writeSVGGraph(w io.Writer) {
    const indent = "    "
    const line = "<line x1=\"%f\" y1=\"%f\" x2=\"%f\" y2=\"%f\"/>\n"
    Data := []Point{
        {0,0},{100,500},{200,0},{300,500},{400,0},{500,500},
    }
    fmt.Fprintf(w, "<svg width=\"1280\" height=\"720\">\n%v\n</svg>",
        Polyline(Data))
}

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

    http.HandleFunc("/climate", func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "text/html")
        fmt.Fprint(w, "<!doctype html>\n<html><body>")
        writeSVGGraph(w)
        fmt.Fprint(w, "</body></html>")
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
