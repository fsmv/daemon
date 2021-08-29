package main

import (
    "flag"
    "fmt"
    "log"
    "net"
    "os"
    "os/signal"
    "strconv"
)

const (
    rpcPort = 2048
    portRangeStart = 2049
    portRangeEnd = 4096
    leaseTTL = "24h"
)

var (
    tlsCertPath = flag.String("tls_cert", "",
        "Either the filepath to the tls cert file (fullchain.pem) or\n\t" +
        "the file descriptor id number shared by the parent process")
    tlsKeyPath = flag.String("tls_key", "",
        "Either the filepath to the tls key file (privkey.pem) or\n\t" +
        "the file descriptor id number shared by the parent process")
    httpPortSpec = flag.Int("http_port", 80,
        "If positive, the port to bind to for http traffic or\n\t" +
        "if negative, the file descriptor id for a socket to listen on\n\t" +
        "shared by the parent process.")
    httpsPortSpec = flag.Int("https_port", 443,
        "If positive, the port to bind to for https traffic or\n\t" +
        "if negative, the file descriptor id for a socket to listen on\n\t" +
        "shared by the parent process.")
)

func openFilePathOrFD(pathOrFD string) (*os.File, error) {
    if fd, err := strconv.Atoi(pathOrFD); err == nil {
        return os.NewFile(uintptr(fd), ""), nil
    }
    return os.Open(pathOrFD)
}

func listenerFromPortOrFD(portOrFD int) (net.Listener, error) {
    if portOrFD < 0 {
        return net.FileListener(os.NewFile(uintptr(-portOrFD), ""))
    }
    return net.Listen("tcp", fmt.Sprintf(":%v", portOrFD))
}

func main() {
    flag.Parse()
    quit := make(chan struct{})
    sigs := make(chan os.Signal, 2)
    signal.Notify(sigs, os.Interrupt, os.Kill)
    go func() {
        <-sigs
        close(quit)
    }()

    tlsCert, err := openFilePathOrFD(*tlsCertPath)
    if err != nil {
        log.Fatalf("Failed to load tls cert file (%v): %v",
            *tlsCertPath, err)
    }
    tlsKey, err := openFilePathOrFD(*tlsKeyPath)
    if err != nil {
        log.Fatalf("Failed to load tls key file (%v): %v",
            *tlsKeyPath, err)
    }
    httpListener, err := listenerFromPortOrFD(*httpPortSpec)
    if err != nil {
        log.Fatalf("Failed to listen on http port (%v): %v",
            *httpPortSpec, err)
    }
    httpsListener, err := listenerFromPortOrFD(*httpsPortSpec)
    if err != nil {
        log.Fatalf("Failed to listen on https port (%v): %v",
            *httpsPortSpec, err)
    }

    proxySrv, err := StartHTTPProxy(
        tlsCert, tlsKey, httpListener, httpsListener,
        portRangeStart, portRangeEnd, leaseTTL, quit)
    log.Print("Started frontend proxy server")
    if err != nil {
        log.Fatalf("Failed to start proxyserv: %v", err)
    }

    _, err = StartRPCServer(proxySrv, rpcPort, quit)
    log.Print("Started rpc server on port ", rpcPort)
    if err != nil {
        log.Fatal(err)
    }

    <-quit // Wait for quit
}
