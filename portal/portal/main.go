package main

import (
    "flag"
    "fmt"
    "log"
    "net"
    "time"
    "crypto/tls"
    "os"
    "io/ioutil"
    "strconv"

    "ask.systems/daemon/tools"
)

const (
    rpcPort = 2048
    portRangeStart = 2049
    portRangeEnd = 4096
    leaseTTL = 24*time.Hour
)

var (
    tlsCertPath = flag.String("tls_cert", "",
        "Either the filepath to the tls cert file (fullchain.pem) or\n" +
        "the file descriptor id number shared by the parent process")
    tlsKeyPath = flag.String("tls_key", "",
        "Either the filepath to the tls key file (privkey.pem) or\n" +
        "the file descriptor id number shared by the parent process")
    httpPortSpec = flag.Int("http_port", 80,
        "If positive, the port to bind to for http traffic or\n" +
        "if negative, the file descriptor id for a socket to listen on\n" +
        "shared by the parent process.")
    httpsPortSpec = flag.Int("https_port", 443,
        "If positive, the port to bind to for https traffic or\n" +
        "if negative, the file descriptor id for a socket to listen on\n" +
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

// loadTLSConfig loads the data from the tls files then closes them
func loadTLSConfig(tlsCert, tlsKey *os.File) (*tls.Config, error) {
    defer tlsCert.Close()
    defer tlsKey.Close()
    certBytes, err := ioutil.ReadAll(tlsCert)
    if err != nil {
        return nil, fmt.Errorf("failed to read tls cert file: %v", err)
    }
    keyBytes, err := ioutil.ReadAll(tlsKey)
    if err != nil {
        return nil, fmt.Errorf("failed to read tls key file: %v", err)
    }
    cert, err := tls.X509KeyPair(certBytes, keyBytes)
    if err != nil {
        return nil, fmt.Errorf("invalid tls file format: %v", err)
    }
    ret := &tls.Config{
        Certificates: make([]tls.Certificate, 1),
    }
    ret.Certificates[0] = cert
    return ret, nil
}

func main() {
    flag.Parse()
    quit := make(chan struct{})
    tools.CloseOnSignals(quit)

    tlsCert, err := openFilePathOrFD(*tlsCertPath)
    if err != nil {
        log.Fatalf("Failed to load tls cert file (%v): %v", *tlsCertPath, err)
    }
    tlsKey, err := openFilePathOrFD(*tlsKeyPath)
    if err != nil {
        log.Fatalf("Failed to load tls key file (%v): %v", *tlsKeyPath, err)
    }
    tlsConfig, err := loadTLSConfig(tlsCert, tlsKey)
    if err != nil {
        log.Fatalf("failed to load TLS config: %v", err)
    }

    httpListener, err := listenerFromPortOrFD(*httpPortSpec)
    if err != nil {
        log.Fatalf("Failed to listen on http port (%v): %v", *httpPortSpec, err)
    }
    httpsListener, err := listenerFromPortOrFD(*httpsPortSpec)
    if err != nil {
        log.Fatalf("Failed to listen on https port (%v): %v", *httpsPortSpec, err)
    }

    l := StartPortLeasor(portRangeStart, portRangeEnd, leaseTTL, quit)
    tcpProxy := StartTCPProxy(l, tlsConfig, quit)
    httpProxy, err := StartHTTPProxy(l, tlsConfig, httpListener, httpsListener, quit)
    log.Print("Started HTTP proxy server")
    if err != nil {
        log.Fatalf("Failed to start HTTP proxy server: %v", err)
    }

    _, err = StartRPCServer(l, tcpProxy, httpProxy, rpcPort, quit)
    log.Print("Started rpc server on port ", rpcPort)
    if err != nil {
        log.Fatal("Failed to start RPC server:", err)
    }

    <-quit // Wait for quit
}
