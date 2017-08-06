package rpcserv

import (
    "strconv"
    "log"
    "net"
    "net/rpc"

    "feproxy/proxyserv"
)

// Methods on this type are exported as rpc calls
type RPCServ struct {
    proxyServ *proxyserv.ProxyServ
    quit      chan struct{}
}

// Registers a new forwarding rule in the proxy server.
// Randomly assigns port for the client to listen on
func (s *RPCServ) Register(path string, ret *proxyserv.Lease) error {
    lease, err := s.proxyServ.Register(path)
    if err != nil {
        return err
    }
    log.Printf("Registered lease: localhost:%v%v, TTL: %v",
               lease.AssignedPort, path, lease.TTL)
    *ret = lease
    return nil
}

func (s *RPCServ) Deregister(path string, _ *struct{}) error {
    s.proxyServ.DeleteHandler(path)
    log.Print("Unregistered path: ", path)
    return nil
}

func (s *RPCServ) Quit(_, _ *struct{}) error {
    log.Print("Shutting down")
    s.quit <- struct{}{}
    return nil
}

func StartNew(proxyServ *proxyserv.ProxyServ, port uint16,
              quit chan struct{}) *RPCServ {
    s := &RPCServ{
        proxyServ: proxyServ,
        quit:      quit,
    }
    server := rpc.NewServer()
    server.RegisterName("feproxy", s)
    l, err := net.Listen("tcp", ":" + strconv.Itoa(int(port)))
    if err != nil {
        log.Fatal("Failed to start listener:", err)
    }
    go func () {
        server.Accept(l)
        log.Print("RPC server died, quitting")
        quit <- struct{}{}
    }()
    go func() {
        <-quit
        l.Close()
    }()
    return s
}
