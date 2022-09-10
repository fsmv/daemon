package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"strconv"
	"strings"

	"ask.systems/daemon/portal"
	"google.golang.org/grpc"
	"google.golang.org/grpc/peer"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Methods on this type are exported as rpc calls
type RPCServ struct {
	portal.PortalServer

	leasor    *PortLeasor
	tcpProxy  *TCPProxy
	httpProxy *HTTPProxy
	quit      chan struct{}
}

func hostname(address string) string {
	portIdx := strings.Index(address, ":")
	return address[:portIdx]
}

func (s *RPCServ) loadSavedRegistrations(saveFilepath string) {
	// Load the data from the file
	saveData, err := os.ReadFile(saveFilepath)
	if err != nil {
		log.Print("Save state file not read: ", err)
		return
	}
	state := &State{}
	if err := proto.Unmarshal(saveData, state); err != nil {
		log.Print("Failed to unmarshal save state file: ", err)
		return
	}

	// Register the saved registrations
	loaded := 0
	for _, registration := range state.Registrations {
		// Note: Don't check for expired leases, if we restart everyone gets an
		// extension on their leases. We will remove leases from the file as the
		// expire while we're online.
		_, err := s.internalRegister(registration.ClientAddr, registration.Request)
		if err != nil {
			log.Printf("Failed to recreate registration: %v\n%v", err, registration)
			continue
		}
		loaded += 1
	}
	log.Printf("Successfully loaded %v/%v saved registrations", loaded, len(state.Registrations))
}

// Register registers a new forwarding rule to the rpc client's ip address.
// Randomly assigns port for the client to listen on
func (s *RPCServ) Register(ctx context.Context, request *portal.RegisterRequest) (*portal.Lease, error) {
	// Get the RPC client's address (without the port) from gRPC
	p, _ := peer.FromContext(ctx)
	client := hostname(p.Addr.String())
	return s.internalRegister(client, request)
}

func (s *RPCServ) internalRegister(client string, request *portal.RegisterRequest) (*portal.Lease, error) {
	if strings.HasPrefix(request.Pattern, tcpProxyPrefix) {
		return s.tcpProxy.Register(client, request)
	} else {
		return s.httpProxy.Register(client, request)
	}
}

// Unregister unregisters the forwarding rule with the given pattern
func (s *RPCServ) Unregister(ctx context.Context, lease *portal.Lease) (*portal.Lease, error) {
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
func (s *RPCServ) Renew(ctx context.Context, lease *portal.Lease) (*portal.Lease, error) {
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
	port uint16, saveFilepath string, quit chan struct{}) (*RPCServ, error) {

	s := &RPCServ{
		leasor:    leasor,
		tcpProxy:  tcpProxy,
		httpProxy: httpProxy,
		quit:      quit,
	}
	s.loadSavedRegistrations(saveFilepath)
	server := grpc.NewServer()
	portal.RegisterPortalServer(server, s)
	l, err := net.Listen("tcp", ":"+strconv.Itoa(int(port)))
	if err != nil {
		return nil, fmt.Errorf("Failed to start listener: %v", err)
	}
	go func() {
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
