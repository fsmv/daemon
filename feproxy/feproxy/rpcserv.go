package main

import (
    "fmt"
    "log"
    "net"
    "context"
    "strings"
    "strconv"

    "ask.systems/daemon/feproxy"
    "google.golang.org/protobuf/types/known/timestamppb"
    "google.golang.org/grpc"
    "google.golang.org/grpc/peer"
)

// Methods on this type are exported as rpc calls
type RPCServ struct {
    feproxy.FeproxyServer

    leasor    *PortLeasor
    tcpProxy  *TCPProxy
    httpProxy *HTTPProxy
    quit      chan struct{}
}

func hostname(address string) string {
    portIdx := strings.Index(address, ":")
    return address[:portIdx]
}

// Register registers a new forwarding rule to the rpc client's ip address.
// Randomly assigns port for the client to listen on
func (s *RPCServ) Register(ctx context.Context, request *feproxy.RegisterRequest) (*feproxy.Lease, error) {
    // Get the RPC client's address (without the port) from gRPC
    p, _ := peer.FromContext(ctx)
    client := hostname(p.Addr.String())

    var lease *feproxy.Lease
    var err error
    if strings.HasPrefix(request.Pattern, tcpProxyPrefix) {
        lease, err = s.tcpProxy.Register(client, request)
    } else {
        lease, err = s.httpProxy.Register(client, request)
    }
    if err != nil {
        log.Print(err)
        return nil, err
    }
    return lease, nil
}

// Unregister unregisters the forwarding rule with the given pattern
func (s *RPCServ) Unregister(ctx context.Context, lease *feproxy.Lease) (*feproxy.Lease, error) {
    err := s.leasor.Unregister(lease)
    if err != nil {
        log.Print(err)
        return lease, err
    }
    log.Printf("Unregistered rule with pattern: %v", lease.Pattern)
    lease.Timeout = timestamppb.Now()
    return lease, nil
}

// Renew renews the lease on a currently registered pattern
func (s *RPCServ) Renew(ctx context.Context, lease *feproxy.Lease) (*feproxy.Lease, error) {
    lease, err := s.leasor.Renew(lease)
    if err != nil {
        log.Print(err)
        return nil, err
    }
    log.Printf("Renewed lease on pattern: %v. Port: %v, Timeout: %v",
        lease.Pattern, lease.Port, lease.Timeout.AsTime())
    return lease, nil
}

// StartNew creates a new RPCServ and starts it
func StartRPCServer(leasor *PortLeasor, tcpProxy *TCPProxy, httpProxy *HTTPProxy,
    port uint16, quit chan struct{}) (*RPCServ, error) {

    s := &RPCServ{
        leasor: leasor,
        tcpProxy: tcpProxy,
        httpProxy: httpProxy,
        quit:      quit,
    }
    server := grpc.NewServer()
    feproxy.RegisterFeproxyServer(server, s)
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
