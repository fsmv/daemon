package rpcserv

import (
    "fmt"
    "log"
    "net"
    "strings"
    "strconv"

    "daemon/feproxy/proxyserv"
)

// Methods on this type are exported as rpc calls
type RPCServ struct {
    proxyServ *proxyserv.ProxyServ
    quit      chan struct{}
}

// Register registers a new forwarding rule in the proxy server.
// Randomly assigns port for the client to listen on
func (s *RPCServ) Register(clientAddr net.Addr, pattern string, ret *proxyserv.Lease) error {
    addrRaw := clientAddr.String()
    portIdx := strings.Index(addrRaw, ":")
    addrNoPort := addrRaw[:portIdx]
    lease, err := s.proxyServ.Register(addrNoPort, pattern)
    if err != nil {
        log.Print(err)
        return err
    }
    log.Printf("Registered forwarder to %v:%v, Pattern: %v, TTL: %v",
               addrNoPort, lease.Port, pattern, lease.TTL)
    *ret = lease
    return nil
}

// Unregister unregisters the forwarding rule with the given pattern
func (s *RPCServ) Unregister(clientAddr net.Addr, pattern string, _ *struct{}) error {
    err := s.proxyServ.Unregister(pattern)
    if err != nil {
        log.Print(err)
        return err
    }
    log.Printf("Unregistered rule with pattern: %v", pattern)
    return nil
}

// Renew renews the lease on a currently registered pattern
func (s *RPCServ) Renew(clientAddr net.Addr, pattern string, ret *proxyserv.Lease) error {
    lease, err := s.proxyServ.Renew(pattern)
    if err != nil {
        log.Print(err)
        return err
    }
    log.Printf("Renewed lease on pattern: %v. Port: %v, TTL: %v",
        pattern, lease.Port, lease.TTL)
    return nil
}

// Quit quits stops this binary
func (s *RPCServ) Quit(clientAddr net.Addr, _, _ *struct{}) error {
    log.Print("Shutting down")
    close(s.quit)
    return nil
}

// StartNew creates a new RPCServ and starts it
func StartNew(proxyServ *proxyserv.ProxyServ, port uint16,
              quit chan struct{}) (*RPCServ, error) {
    s := &RPCServ{
        proxyServ: proxyServ,
        quit:      quit,
    }
    server := NewServerWithIP()
    server.RegisterName("feproxy", s)
    l, err := net.Listen("tcp", ":" + strconv.Itoa(int(port)))
    if err != nil {
        return nil, fmt.Errorf("Failed to start listener: %v", err)
    }
    go func () {
        server.Accept(l) // logs any errors itself instead of returning
        log.Print("RPC server died, quitting")
        close(quit)
    }()
    go func() {
        <-quit
        l.Close()
    }()
    return s, nil
}
