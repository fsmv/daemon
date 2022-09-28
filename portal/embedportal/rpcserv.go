package embedportal

import (
	"context"
	"crypto/subtle"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"strconv"
	"strings"

	"ask.systems/daemon/portal"
	"ask.systems/daemon/tools"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Methods on this type are exported as rpc calls
type RPCServ struct {
	portal.PortalServer

	leasor    *PortLeasor
	tcpProxy  *TCPProxy
	httpProxy *HTTPProxy
	rootCert  *tools.AutorenewCertificate
	state     *StateManager
	quit      chan struct{}
}

func hostname(address string) string {
	portIdx := strings.Index(address, ":")
	return address[:portIdx]
}

func (s *RPCServ) loadState(saveData []byte) {
	state := &State{}
	if err := proto.Unmarshal(saveData, state); err != nil {
		log.Print("Failed to unmarshal save state file: ", err)
		return
	}

	s.state.SetToken(state.ApiToken) // generates a new token if not set

	loaded := 0
	for _, ca := range state.RootCAs {
		if err := s.state.NewRootCA(ca); err != nil {
			log.Print("Failed to save root CA: ", err)
			continue
		}
		loaded += 1
	}
	log.Printf("Successfully loaded %v/%v saved root CAs", loaded, len(state.RootCAs))

	// Register the saved registrations
	loaded = 0
	for _, registration := range state.Registrations {
		if registration.Request.FixedPort == 0 {
			// Request a new lease for the port we had before if the original request
			// was for a random port
			registration.Request.FixedPort = registration.Lease.Port
		}
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

func (s *RPCServ) MyHostname(ctx context.Context, empty *emptypb.Empty) (*portal.Hostname, error) {
	p, _ := peer.FromContext(ctx)
	client := hostname(p.Addr.String())
	return &portal.Hostname{Hostname: client}, nil
}

// Register registers a new forwarding rule to the rpc client's ip address.
// Randomly assigns port for the client to listen on
func (s *RPCServ) Register(ctx context.Context, request *portal.RegisterRequest) (*portal.Lease, error) {
	// Get the RPC client's address (without the port) from gRPC
	p, _ := peer.FromContext(ctx)
	client := hostname(p.Addr.String())

	lease, err := s.internalRegister(client, request)
	if err != nil {
		return nil, err
	}

	var clientCert []byte
	if len(request.CertificateRequest) != 0 {
		clientCert, err = tools.SignCertificate(s.rootCert.Certificate(),
			request.CertificateRequest, lease.Timeout.AsTime(), false)
		if err != nil {
			return nil, err
		}
	}
	lease.Certificate = clientCert

	return lease, nil
}

func (s *RPCServ) internalRegister(client string, request *portal.RegisterRequest) (lease *portal.Lease, err error) {
	if strings.HasPrefix(request.Pattern, tcpProxyPrefix) {
		lease, err = s.tcpProxy.Register(client, request)
	} else {
		lease, err = s.httpProxy.Register(client, request)
	}
	if err == nil {
		s.state.NewRegistration(&Registration{
			ClientAddr: client,
			Request:    request,
			Lease:      lease,
		})
	}
	return
}

// Unregister unregisters the forwarding rule with the given pattern
func (s *RPCServ) Unregister(ctx context.Context, lease *portal.Lease) (*portal.Lease, error) {
	err := s.leasor.Unregister(lease)
	if err != nil {
		log.Print(err)
		return lease, err
	}
	log.Printf("Unregistered rule with pattern: %v", lease.Pattern)
	s.state.Unregister(lease)
	lease.Timeout = timestamppb.Now()
	return lease, nil
}

// Renew renews the lease on a currently registered pattern
func (s *RPCServ) Renew(ctx context.Context, lease *portal.Lease) (*portal.Lease, error) {
	newLease, err := s.leasor.Renew(lease)
	if err != nil {
		log.Print(err)
		return nil, err
	}

	registration := s.state.LookupRegistration(lease)

	// Renew the certificate if we had one
	if len(registration.Request.CertificateRequest) != 0 {
		newCert, err := tools.SignCertificate(s.rootCert.Certificate(),
			registration.Request.CertificateRequest, newLease.Timeout.AsTime(), false)
		if err != nil {
			return nil, err
		}
		newLease.Certificate = newCert
	}

	s.state.RenewRegistration(newLease)
	log.Printf("Renewed lease on pattern: %v. Port: %v, Timeout: %v",
		newLease.Pattern, newLease.Port, newLease.Timeout.AsTime())
	return newLease, nil
}

// StartNew creates a new RPCServ and starts it
func StartRPCServer(leasor *PortLeasor,
	tcpProxy *TCPProxy, httpProxy *HTTPProxy,
	port uint16, rootCert *tools.AutorenewCertificate,
	saveData []byte, state *StateManager, quit chan struct{}) (*RPCServ, error) {

	s := &RPCServ{
		leasor:    leasor,
		state:     state,
		tcpProxy:  tcpProxy,
		httpProxy: httpProxy,
		quit:      quit,
		rootCert:  rootCert,
	}
	leasor.OnCancel(s.state.Unregister)
	s.loadState(saveData)
	server := grpc.NewServer(
		// TODO: Have a flag like -internet_accessable_rpc which makes the RPC
		// server use the web server cert, and make the portal client library verify
		// certs if connecting to a URL instead of an IP. Then you will know you are
		// for sure talking to your portal server when it's not just on a local
		// network, in which case authenticating the server is good so that we don't
		// send our auth token to a MITM attacker.
		grpc.Creds(credentials.NewTLS(&tls.Config{
			GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
				return rootCert.Certificate(), nil
			},
		})),
		grpc.UnaryInterceptor(func(ctx context.Context, req interface{},
			info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp interface{}, err error) {
			md, ok := metadata.FromIncomingContext(ctx)
			noTokenErr := grpc.Errorf(codes.Unauthenticated, "A metadata authorization token must be presented")
			if !ok {
				return nil, noTokenErr
			}
			clientToken, ok := md["authorization"]
			if !ok || len(clientToken) != 1 {
				return nil, noTokenErr
			}
			if subtle.ConstantTimeCompare([]byte(clientToken[0]), []byte(state.Token())) == 0 {
				return nil, grpc.Errorf(codes.Unauthenticated, "Invalid token")
			}
			return handler(ctx, req)
		}),
	)
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