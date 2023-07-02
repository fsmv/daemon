package embedportal

import (
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync"

	"ask.systems/daemon/portal/gate"
)

// The RegisterRequest.Pattern prefix for tcp proxies
const tcpProxyPrefix = ":tcp"

type tcpProxy struct {
	clientLeasor *clientLeasor
	tlsConfig    *tls.Config
	quit         chan struct{}
	cancelers    sync.Map
}

func startTCPProxy(l *clientLeasor, tlsConfig *tls.Config, quit chan struct{}) *tcpProxy {
	p := &tcpProxy{
		clientLeasor: l,
		tlsConfig:    tlsConfig,
		quit:         quit,
	}
	l.OnCancel(p.Unregister)
	return p
}

func (p *tcpProxy) Unregister(lease *gate.Lease) {
	p.unregisterPattern(lease.GetPattern())
}

func (p *tcpProxy) unregisterPattern(pattern string) {
	canceler, _ := p.cancelers.Load(pattern)
	if canceler != nil {
		close(canceler.(chan struct{}))
		p.cancelers.Delete(pattern)
	}
}

func (p *tcpProxy) Register(clientAddr string, request *gate.RegisterRequest) (*gate.Lease, error) {
	cancelLease := make(chan struct{})
	go func() {
		select {
		case <-p.quit:
			close(cancelLease)
		case <-cancelLease:
			return
		}
	}()
	p.unregisterPattern(request.Pattern) // replace existing patterns
	leasor := p.clientLeasor.PortLeasorForClient(clientAddr)
	lease, err := leasor.Register(request)
	if err != nil {
		return nil, err
	}
	port := strings.TrimPrefix(request.Pattern, tcpProxyPrefix)
	listener, err := tls.Listen("tcp", port, p.tlsConfig) // hopefully the old listener has closed by now
	if err != nil {
		leasor.Unregister(lease)
		return nil, fmt.Errorf("Failed to listen on the requested port for TCP Proxy (%v): %v", lease.Port, err)
	}
	serverAddress := fmt.Sprintf("%v:%v", clientAddr, lease.Port)
	startTCPForward(listener, serverAddress, cancelLease)
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

func startTCPForward(tlsListener net.Listener, serverAddress string, quit chan struct{}) {
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
