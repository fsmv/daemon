package main

import (
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync"

	"ask.systems/daemon/portal"
)

// The RegisterRequest.Pattern prefix for tcp proxies
const tcpProxyPrefix = ":tcp"

type TCPProxy struct {
	leasor    *PortLeasor
	tlsConfig *tls.Config
	quit      chan struct{}
	cancelers sync.Map
}

func StartTCPProxy(l *PortLeasor, tlsConfig *tls.Config, quit chan struct{}) *TCPProxy {
	p := &TCPProxy{
		leasor:    l,
		tlsConfig: tlsConfig,
		quit:      quit,
	}
	l.OnTTL(p.Unregister)
	return p
}

func (p *TCPProxy) Unregister(lease *portal.Lease) {
	canceler, _ := p.cancelers.Load(lease.Pattern)
	close(canceler.(chan struct{}))
}

func (p *TCPProxy) Register(clientAddr string, request *portal.RegisterRequest) (*portal.Lease, error) {
	cancelLease := make(chan struct{})
	go func() {
		select {
		case <-p.quit:
			close(cancelLease)
		case <-cancelLease:
			return
		}
	}()
	lease, err := p.leasor.Register(request)
	if err != nil {
		if errors.Is(err, FixedPortTakenErr) {
			// TODO: can we notify the old lease holder that we kicked them?
			log.Printf("Replacing an existing lease for the same TCP pattern: %#v", request.Pattern)
			p.leasor.Unregister(lease)
			lease, err = p.leasor.Register(request)
		}
	}
	if err != nil {
		return nil, err
	}
	port := strings.TrimPrefix(request.Pattern, tcpProxyPrefix)
	listener, err := tls.Listen("tcp", port, p.tlsConfig)
	if err != nil {
		return nil, fmt.Errorf("Failed to listen on the requested port for TCP Proxy (%v): %v", lease.Port, err)
	}
	serverAddress := fmt.Sprintf("%v:%v", clientAddr, lease.Port)
	startTCPProxy(listener, serverAddress, cancelLease)
	log.Printf("Registered a TCP proxy forwarding %v to %v.", port, serverAddress)
	p.cancelers.Store(request.Pattern, cancelLease)
	return lease, nil
}

func handleConnection(publicConn net.Conn, serverAddress string, quit chan struct{}) {
	privateConn, err := net.Dial("tcp", serverAddress)
	if err != nil {
		log.Printf("Failed to connect to TCP Proxy backend (for client %v): %v",
			publicConn.RemoteAddr(), err)
		publicConn.Close()
		return
	}
	go func() {
		<-quit // when we quit, close all the connections
		// TODO: do we want to have a timeout for graceful stopping?
		publicConn.Close()
		privateConn.Close()
	}()
	// Forward all the messages unaltered, in both directions.
	//
	// If either copy direction has an error or closes, we need to make sure
	// both connections are closed (and we only want to log once)
	var closedLog sync.Once
	closedMsg := fmt.Sprintf(
		"TCP Proxy Closed; user: %v -> backend: %v. Appears to backend as %v ",
		publicConn.RemoteAddr(), serverAddress, privateConn.LocalAddr())
	go func() {
		io.Copy(publicConn, privateConn)
		publicConn.Close()
		privateConn.Close()
		closedLog.Do(func() { log.Printf(closedMsg) })
	}()
	go func() {
		io.Copy(privateConn, publicConn)
		publicConn.Close()
		privateConn.Close()
		closedLog.Do(func() { log.Printf(closedMsg) })
	}()
	log.Printf("TCP Proxy Established; user: %v -> backend: %v. Appears to backend as %v ",
		publicConn.RemoteAddr(), serverAddress, privateConn.LocalAddr())
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
				log.Printf("Failed to accept user connection on TCP Proxy (backend: %v): %v",
					serverAddress, err)
				break
			}
			// Use a goroutine just to not wait until the Dial is done before we
			// can accept connections again
			go handleConnection(publicConn, serverAddress, quit)
		}
		tlsListener.Close()
	}()
}
