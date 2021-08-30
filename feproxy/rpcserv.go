package main

import (
    "fmt"
    "log"
    "net"
    "context"
    "strings"
    "strconv"

    "ask.systems/daemon/feproxy/client"
    "google.golang.org/grpc"
    "google.golang.org/grpc/peer"
)

// Methods on this type are exported as rpc calls
type RPCServ struct {
    proxyServ *ProxyServ
    quit      chan struct{}
}

// Register registers a new forwarding rule to the rpc client's ip address.
// Randomly assigns port for the client to listen on
func (s *RPCServ) Register(ctx context.Context, request *client.RegisterRequest) (*client.Lease, error) {
    p, _ := peer.FromContext(ctx)
    clientAddr := p.Addr
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

// TODO: delete this function and use the request parameters
// Register registers a new forwarding rule to the rpc client's ip address.
// Uses a fixed port (which must be out of feproxy's reserved range) and strips
// the pattern in requests, intended for web servers that don't know about feproxy.
func (s *RPCServ) RegisterThirdParty(clientAddr net.Addr, args *client.ThirdPartyArgs, ret *client.Lease) error {
    // Strip the port that the client connected to the RPC server with
    addrRaw := clientAddr.String()
    portIdx := strings.Index(addrRaw, ":")
    addrNoPort := addrRaw[:portIdx]

    lease, err := s.proxyServ.RegisterThirdParty(addrNoPort, args.Port, args.Pattern)
    if err != nil {
        log.Print(err)
        return err
    }
    log.Printf("Registered third party forwarder to %v:%v, Pattern: %v, TTL: %v",
               addrNoPort, lease.Port, lease.Pattern, lease.TTL)
    *ret = lease
    return nil
}

// Unregister unregisters the forwarding rule with the given pattern
func (s *RPCServ) Unregister(ctx context.Context, pattern *client.Pattern) (*client.Pattern, error) {
    err := s.proxyServ.Unregister(pattern)
    if err != nil {
        log.Print(err)
        return err
    }
    log.Printf("Unregistered rule with pattern: %v", pattern)
    return pattern, nil
}

// Renew renews the lease on a currently registered pattern
func (s *RPCServ) Renew(ctx context.Context, pattern *client.Pattern, ret *client.Lease) error {
    lease, err := s.proxyServ.Renew(pattern)
    if err != nil {
        log.Print(err)
        return err
    }
    log.Printf("Renewed lease on pattern: %v. Port: %v, TTL: %v",
        pattern, lease.Port, lease.TTL)
    return nil
}

// StartNew creates a new RPCServ and starts it
func StartRPCServer(proxyServ *ProxyServ, port uint16,
              quit chan struct{}) (*RPCServ, error) {
    s := &RPCServ{
        proxyServ: proxyServ,
        quit:      quit,
    }
    server := grpc.NewServer()
    client.RegisterFeproxyServer(server, s)
    l, err := net.Listen("tcp", ":" + strconv.Itoa(int(port)))
    if err != nil {
        return nil, fmt.Errorf("Failed to start listener: %v", err)
    }
    go func () {
        server.Serve(l) // logs any errors itself instead of returning
        log.Print("RPC server died, quitting")
        close(quit)
    }()
    go func() {
        <-quit
        server.GracefulStop()
        l.Close()
    }()
    return s, nil
}
