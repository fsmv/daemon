package main

import (
    "fmt"
    "log"
    "net"
    "net/url"
    "context"
    "strconv"

    "ask.systems/daemon/feproxy/client"
    "google.golang.org/protobuf/types/known/timestamppb"
    "google.golang.org/grpc"
    "google.golang.org/grpc/peer"
)

// Methods on this type are exported as rpc calls
type RPCServ struct {
    client.FeproxyServer
    proxyServ *ProxyServ
    quit      chan struct{}
}

// Register registers a new forwarding rule to the rpc client's ip address.
// Randomly assigns port for the client to listen on
func (s *RPCServ) Register(ctx context.Context, request *client.RegisterRequest) (*client.Lease, error) {
    // Get the RPC client's address (without the port) from gRPC
    p, _ := peer.FromContext(ctx)
    client, err := url.Parse(p.Addr.String())
    if err != nil {
        log.Print(err)
        return nil, fmt.Errorf("Failed to get client hostname: %v", err)
    }

    lease, err := s.proxyServ.Register(client.Hostname(), request)
    if err != nil {
        log.Print(err)
        return nil, err
    }
    log.Printf("Registered forwarder to %v:%v, Pattern: %v, Timeout: %v",
               client.Hostname(), lease.Port, request.Pattern, lease.Timeout.AsTime())
    return lease, nil
}

// Unregister unregisters the forwarding rule with the given pattern
func (s *RPCServ) Unregister(ctx context.Context, lease *client.Lease) (*client.Lease, error) {
    err := s.proxyServ.Unregister(lease.Pattern)
    if err != nil {
        log.Print(err)
        return lease, err
    }
    log.Printf("Unregistered rule with pattern: %v", lease.Pattern)
    lease.Timeout = timestamppb.Now()
    return lease, nil
}

// Renew renews the lease on a currently registered pattern
func (s *RPCServ) Renew(ctx context.Context, lease *client.Lease) (*client.Lease, error) {
    lease, err := s.proxyServ.Renew(lease.Pattern)
    if err != nil {
        log.Print(err)
        return nil, err
    }
    log.Printf("Renewed lease on pattern: %v. Port: %v, Timeout: %v",
        lease.Pattern, lease.Port, lease.Timeout.AsTime())
    return lease, nil
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
