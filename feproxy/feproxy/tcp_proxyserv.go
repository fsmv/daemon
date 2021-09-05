package main

import (
    "log"
    "io"
    "net"
)

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

func StartTCPProxy(tlsListener net.Listener, serverAddress string, quit chan struct{}) {
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
