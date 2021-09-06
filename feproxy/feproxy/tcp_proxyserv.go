package main

import (
    "log"
    "io"
    "fmt"
    "strings"
    "net"
    "crypto/tls"

    "ask.systems/daemon/feproxy"
)

// The RegisterRequest.Pattern prefix for tcp proxies
const tcpProxyPrefix = ":tcp"

type TCPProxy struct {
    leasor *PortLeasor
    tlsConfig *tls.Config
    quit chan struct{}
}

func StartTCPProxy(l *PortLeasor, tlsConfig *tls.Config, quit chan struct{}) *TCPProxy {
    return &TCPProxy{
        leasor: l,
        tlsConfig: tlsConfig,
        quit: quit,
    }
}

func (p *TCPProxy) Register(clientAddr string, request *feproxy.RegisterRequest) (*feproxy.Lease, error) {
    cancelLease := make(chan struct{})
    go func() {
        <-p.quit
        close(cancelLease)
    }()
    lease, err := p.leasor.Register(&feproxy.Lease{
        Pattern: request.Pattern,
        Port: request.FixedPort,
    }, func () { close(cancelLease) })
    if err != nil {
        return nil, err
    }
    port := strings.TrimPrefix(request.Pattern, tcpProxyPrefix)
    listener, err := tls.Listen("tcp", port, p.tlsConfig)
    if err != nil {
        return nil, fmt.Errorf("Failed to listen on the requested port (%v): %v", lease.Port, err)
    }
    serverAddress := fmt.Sprintf("%v:%v", clientAddr, lease.Port)
    startTCPProxy(listener, serverAddress, cancelLease)
    log.Printf("Started a TCP proxy forwarding %v to %v.", port, serverAddress)
    return lease, nil
}

func handleConnection(publicConn net.Conn, serverAddress string, quit chan struct{}) {
    privateConn, err := net.Dial("tcp", serverAddress)
    if err != nil {
        log.Printf("Failed to connect to TCP Proxy backend (%v): %v",
            serverAddress, err)
        publicConn.Close()
        return // TODO: maybe stop listening if we see a lot of these
    }
    go func () {
        <-quit // when we quit, close all the connections
        // TODO: do we want to have a timeout for graceful stopping?
        publicConn.Close()
        privateConn.Close()
    }()
    // Forward all the messages unaltered, in both directions
    go io.Copy(publicConn, privateConn)
    go io.Copy(privateConn, publicConn)
}

func startTCPProxy(tlsListener net.Listener, serverAddress string, quit chan struct{}) {
    go func() {
        <-quit // stop listening when we quit
        tlsListener.Close()
    }()
    go func() {
        for {
            publicConn, err := tlsListener.Accept()
            if err != nil {
                log.Printf("Failed to listen on TCP Proxy (%v -> %v): %v",
                    tlsListener.Addr(), serverAddress, err)
                break
            }
            // Use a goroutine just to not wait until the Dial is done before we
            // can accept connections again
            go handleConnection(publicConn, serverAddress, quit)
        }
        tlsListener.Close()
    }()
}
