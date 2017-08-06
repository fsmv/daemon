package rpcserv

import (
    "fmt"
    "log"
    "net"
    "net/rpc"
    "strconv"

    "feproxy/proxyserv"
)

// Methods on this type are exported as rpc calls
type RPCServ struct {
    proxyServ *proxyserv.ProxyServ
    quit      chan struct{}
}

// Register registers a new forwarding rule in the proxy server.
// Randomly assigns port for the client to listen on
func (s *RPCServ) Register(pattern string, ret *proxyserv.Lease) error {
    lease, err := s.proxyServ.Register(pattern)
    if err != nil {
        return err
    }
    log.Printf("Registered forwarder to localhost:%v, Pattern: %v, TTL: %v",
               lease.Port, pattern, lease.TTL)
    *ret = lease
    return nil
}

// Unregister unregisters the forwarding rule with the given pattern
func (s *RPCServ) Unregister(pattern string, _ *struct{}) error {
    s.proxyServ.Unregister(pattern)
    log.Printf("Unregistered rule with pattern: %v", pattern)
    return nil
}

// Quit quits stops this binary
func (s *RPCServ) Quit(_, _ *struct{}) error {
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
    server := rpc.NewServer()
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
