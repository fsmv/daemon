package embedportal

import (
	"context"
	"crypto/subtle"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"strconv"
	"strings"
	"time"

	"ask.systems/daemon/portal/gate"
	"ask.systems/daemon/tools"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Methods on this type are exported as rpc calls
type rcpServ struct {
	gate.PortalServer

	clientLeasor *clientLeasor
	tcpProxy     *tcpProxy
	httpProxy    *httpProxy
	rootCert     *tls.Config
	state        *stateManager
	quit         chan struct{}
}

func (s *rcpServ) loadState(saveData []byte) {
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
		_, err := s.internalRegister(registration.Lease.Address, registration.Request)
		if err != nil {
			log.Printf("Failed to recreate registration: %v\n%v", err, registration)
			continue
		}
		loaded += 1
	}
	log.Printf("Successfully loaded %v/%v saved registrations", loaded, len(state.Registrations))
}

func (s *rcpServ) MyHostname(ctx context.Context, empty *emptypb.Empty) (*gate.Hostname, error) {
	p, _ := peer.FromContext(ctx)
	clientAddr, _, err := net.SplitHostPort(p.Addr.String())
	if err != nil {
		return nil, err
	}
	return &gate.Hostname{Hostname: clientAddr}, nil
}

// Register registers a new forwarding rule to the rpc client's ip address.
// Randomly assigns port for the client to listen on
func (s *rcpServ) Register(ctx context.Context, request *gate.RegisterRequest) (*gate.Lease, error) {
	// Get the RPC client's address (without the port) from gRPC
	p, _ := peer.FromContext(ctx)

	var clientAddr string
	if request.Hostname == "" {
		var err error
		clientAddr, _, err = net.SplitHostPort(p.Addr.String())
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
	} else {
		ipAddrs, err := net.LookupIP(request.Hostname)
		if err != nil || len(ipAddrs) < 1 {
			return nil, status.Errorf(codes.InvalidArgument,
				"Failed to resolve request.Hostname to an IP: %v", err)
		}
		// We have to register the resolved IP because it's possible to request
		// different hostname strings that resolve to the same IP. Since this is
		// used to make sure we don't hand out duplicate ports we need to make sure
		// that we get the same port leasor for all of the alias hostnames.
		//
		// This isn't perfect because one machine can have multiple IPs but mostly
		// this will be used with fixed_ports only anyway so there shouldn't be
		// conflicts. Also I think you can actually set the bind address and have
		// multiple servers on the same port.
		clientAddr = ipAddrs[0].String()
	}

	lease, err := s.internalRegister(clientAddr, request)
	if err != nil {
		return nil, err
	}

	var clientCert []byte
	if len(request.CertificateRequest) != 0 {
		root, err := s.rootCert.GetCertificate(nil)
		if err != nil {
			return nil, err
		}
		// We want the expiration to be at least 2x the lease because if the server
		// is restarted you get a refreshed TTL and we want the cert to survive
		// until the next renew happens
		clientCert, err = tools.SignCertificate(root,
			request.CertificateRequest, time.Now().Add(2*leaseTTL), false)
		if err != nil {
			return nil, err
		}
	}
	lease.Certificate = clientCert

	return lease, nil
}

func (s *rcpServ) internalRegister(clientAddr string, request *gate.RegisterRequest) (lease *gate.Lease, err error) {
	if strings.HasPrefix(request.Pattern, tcpProxyPrefix) {
		lease, err = s.tcpProxy.Register(clientAddr, request)
	} else {
		lease, err = s.httpProxy.Register(clientAddr, request)
	}
	if err == nil {
		s.state.NewRegistration(&Registration{
			Request: request,
			Lease:   lease,
		})
	}
	return
}

// Unregister unregisters the forwarding rule with the given pattern
func (s *rcpServ) Unregister(ctx context.Context, lease *gate.Lease) (*gate.Lease, error) {
	leasor := s.clientLeasor.PortLeasorForClient(lease.Address)
	err := leasor.Unregister(lease)
	if err != nil {
		log.Print(err)
		if errors.Is(err, UnregisteredErr) {
			return nil, status.Error(codes.NotFound, err.Error())
		} else if errors.Is(err, InvalidLeaseErr) {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		return lease, err
	}
	log.Printf("Unregistered rule with pattern: %v", lease.Pattern)
	s.state.Unregister(lease)
	lease.Timeout = timestamppb.Now()
	return lease, nil
}

// Renew renews the lease on a currently registered pattern
func (s *rcpServ) Renew(ctx context.Context, lease *gate.Lease) (*gate.Lease, error) {
	leasor := s.clientLeasor.PortLeasorForClient(lease.Address)
	newLease, err := leasor.Renew(lease)
	if err != nil {
		log.Print(err)
		if errors.Is(err, UnregisteredErr) {
			return nil, status.Error(codes.NotFound, err.Error())
		} else if errors.Is(err, InvalidLeaseErr) {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		return nil, err
	}

	registration := s.state.LookupRegistration(lease)

	// Renew the certificate if we had one
	if len(registration.Request.CertificateRequest) != 0 {
		root, err := s.rootCert.GetCertificate(nil)
		if err != nil {
			return nil, err
		}
		newCert, err := tools.SignCertificate(root,
			registration.Request.CertificateRequest, newLease.Timeout.AsTime(), false)
		if err != nil {
			return nil, err
		}
		newLease.Certificate = newCert
	}

	s.state.RenewRegistration(newLease)
	return newLease, nil
}

// StartNew creates a new RPCServ and starts it
func startRPCServer(clientLeasor *clientLeasor,
	tcpProxy *tcpProxy, httpProxy *httpProxy,
	port uint16, rootCert *tls.Config,
	saveData []byte, state *stateManager,
	quit chan struct{}) (*rcpServ, error) {

	s := &rcpServ{
		clientLeasor: clientLeasor,
		state:        state,
		tcpProxy:     tcpProxy,
		httpProxy:    httpProxy,
		quit:         quit,
		rootCert:     rootCert,
	}
	clientLeasor.OnCancel(s.state.Unregister)
	s.loadState(saveData)
	server := grpc.NewServer(
		// TODO: Have a flag like -internet_accessable_rpc which makes the RPC
		// server use the web server cert, and make the portal client library verify
		// certs if connecting to a URL instead of an IP. Then you will know you are
		// for sure talking to your portal server when it's not just on a local
		// network, in which case authenticating the server is good so that we don't
		// send our auth token to a MITM attacker.
		grpc.Creds(credentials.NewTLS(rootCert)),
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
	gate.RegisterPortalServer(server, s)
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
