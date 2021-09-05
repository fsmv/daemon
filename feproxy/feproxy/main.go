package main

import (
    "flag"
    "fmt"
    "log"
    "net"
    "crypto/tls"
    "os"
    "os/signal"
    "io/ioutil"
    "strconv"
    "encoding/json"
)

const (
    rpcPort = 2048
    portRangeStart = 2049
    portRangeEnd = 4096
    leaseTTL = "24h"
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
    tcpMappings TCPMappings
)

func init() {
    flag.Var(&tcpMappings, "tcp_proxy",
        "A JSON flag for wrapping an internal TCP server in the feproxy SSL/TLS key.\n" +
        "Also accepts negative numbers for the port to use a file descriptor instead.\n" +
        fmt.Sprintf("Schema: %+v", []TCPMapping{{1234, "127.0.0.1:4242"}}))
    flag.Parse()
}

type TCPMappings []TCPMapping
type TCPMapping struct {
    TLSPort int // The port feproxy should listen on
    TCPServerAddress string // The ip_address:port for a TCP server feproxy should forward requests to
}

func (f *TCPMappings) Set(input string) error {
    err := json.Unmarshal([]byte(input), f)
    if err != nil {
        return err
    }
    for i, mapping := range *f {
        if mapping.TLSPort == 0 {
            return fmt.Errorf("Missing TLSPort in index %v", i)
        }
        if mapping.TCPServerAddress == "" {
            return fmt.Errorf("Missing TCPServerAddress in index %v", i)
        }
    }
    return nil
}

func (f *TCPMappings) String() string {
    if f == nil {
        return "nil"
    }
    return fmt.Sprintf("%+v", []TCPMapping(*f))
}

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
    quit := make(chan struct{})
    sigs := make(chan os.Signal, 2)
    signal.Notify(sigs, os.Interrupt, os.Kill)
    go func() {
        <-sigs
        close(quit)
    }()

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

    for i, m := range tcpMappings {
        tcpListener, err := listenerFromPortOrFD(m.TLSPort)
        if err != nil {
            log.Fatalf("Failed to listen on port for TLS mapping (#%v %+v): %v",
                i, m, err)
        }
        StartTCPProxy(tls.NewListener(tcpListener, tlsConfig), m.TCPServerAddress, quit)
        log.Print("Started TCP proxy server")
    }

    proxySrv, err := StartHTTPProxy(
        tlsConfig, httpListener, httpsListener,
        portRangeStart, portRangeEnd, leaseTTL, quit)
    log.Print("Started HTTP proxy server")
    if err != nil {
        log.Fatalf("Failed to start HTTP proxy server: %v", err)
    }

    _, err = StartRPCServer(proxySrv, rpcPort, quit)
    log.Print("Started rpc server on port ", rpcPort)
    if err != nil {
        log.Fatal("Failed to start RPC server:", err)
    }

    <-quit // Wait for quit
}
