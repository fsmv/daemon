package embedportal

import (
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"ask.systems/daemon/portal/gate"
)

// The RegisterRequest.Pattern prefix for tcp proxies
const tcpProxyPrefix = ":tcp"

type tcpProxy struct {
	clientLeasor *clientLeasor
	tlsConfig    *tls.Config
	quit         chan struct{}
	leases       sync.Map // map from pattern to *tcpLease
}

type tcpLease struct {
	Cancel chan struct{}
	Lease  *gate.Lease
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
	val, _ := p.leases.Load(lease.GetPattern())
	if l, ok := val.(*tcpLease); ok && l != nil {
		close(l.Cancel)
		p.leases.Delete(lease.Pattern)
	}
}

func (p *tcpProxy) Register(clientAddr string, request *gate.RegisterRequest, fixedTimeout time.Time) (*gate.Lease, error) {
	cancelLease := make(chan struct{})
	go func() {
		select {
		case <-p.quit:
			close(cancelLease)
		case <-cancelLease:
			return
		}
	}()
	leasor := p.clientLeasor.PortLeasorForClient(clientAddr)

	val, _ := p.leases.Load(request.Pattern)
	if l, ok := val.(*tcpLease); ok && l != nil {
		log.Printf("Replacing existing lease with the same pattern: %#v", request.Pattern)
		leasor.Unregister(l.Lease) // calls tcpProxy.Unregister
	}

	lease, err := leasor.Register(request, fixedTimeout)
	if err != nil {
		return nil, err
	}
	port := strings.TrimPrefix(request.Pattern, tcpProxyPrefix)
	listener, err := tls.Listen("tcp", port, p.tlsConfig) // hopefully the old listener has closed by now
	if err != nil {
		leasor.Unregister(lease)
		return nil, fmt.Errorf("Failed to listen on the requested port for TCP Proxy (%v): %v", lease.Port, err)
	}
	var host string
	if request.Hostname != "" {
		host = request.Hostname
	} else {
		host = clientAddr
	}
	hostPort := fmt.Sprintf("%v:%v", host, lease.Port)
	startTCPForward(listener, hostPort, cancelLease)
	log.Printf("Registered a TCP proxy forwarding %v to %v.", port, hostPort)
	p.leases.Store(request.Pattern, &tcpLease{
		Lease:  lease,
		Cancel: cancelLease,
	})
	return lease, nil
}

func handleConnection(publicConn net.Conn, serverAddress string, tlsCheck *tlsChecker, quit chan struct{}) {
	var privateConn net.Conn
	var err error
	switch tlsCheck.State() {
	case tlsState_UNKNOWN:
		privateConn, err = tls.Dial("tcp", serverAddress, tlsCheck.Conf)
		if err != nil {
			tlsErr := err
			privateConn, err = net.Dial("tcp", serverAddress)
			if err == nil {
				tlsCheck.TCPOnly()
				log.Printf("Warning: TLS is not supported for the %v TCP backend. This internal traffic will not be encrypted. Message: %v",
					serverAddress, tlsErr)
			}
		} else {
			tlsCheck.TLSOnly()
		}
	case tlsState_TLS_ONLY:
		privateConn, err = tls.Dial("tcp", serverAddress, tlsCheck.Conf)
	case tlsState_TCP_ONLY:
		privateConn, err = net.Dial("tcp", serverAddress)
	}

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

// Conf is read only once set. Tracks a simple state machine atomically.
//
// States:
//  1. Unknown if there's TLS support (try running TLS, fallback to non TLS)
//  2. Has TLS (only try TLS)
//  3. No TLS (only try non-TLS)
type tlsChecker struct {
	Conf  *tls.Config
	state atomic.Int32
}

type tlsState int32

const (
	tlsState_UNKNOWN tlsState = iota
	tlsState_TLS_ONLY
	tlsState_TCP_ONLY
)

// Starts in the UNKNOWN state and transitions to one of the other states
// exactly once when support is reported.
func (c *tlsChecker) State() tlsState {
	return tlsState(c.state.Load())
}

func (c *tlsChecker) TCPOnly() {
	c.state.CompareAndSwap(int32(tlsState_UNKNOWN), int32(tlsState_TCP_ONLY))
}

func (c *tlsChecker) TLSOnly() {
	c.state.CompareAndSwap(int32(tlsState_UNKNOWN), int32(tlsState_TLS_ONLY))
}

func startTCPForward(tlsListener net.Listener, serverAddress string, quit chan struct{}) {
	go func() {
		<-quit // stop listening when we quit
		tlsListener.Close()
	}()

	// TODO: when assimilate supports certificate requests, make it so we
	// verify using the portal root CA (and maybe system CAs) when the cert
	// request was used.
	tlsCheck := &tlsChecker{
		Conf: &tls.Config{
			InsecureSkipVerify: true,
		},
	}

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
			go handleConnection(publicConn, serverAddress, tlsCheck, quit)
		}
		tlsListener.Close()
	}()
}
