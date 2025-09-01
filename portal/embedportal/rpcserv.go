package embedportal

import (
	"context"
	"crypto/subtle"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
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
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Methods on this type are exported as rpc calls
type rpcServ struct {
	gate.UnimplementedPortalServer

	clientLeasor *clientLeasor
	tcpProxy     *tcpProxy
	httpProxy    *httpProxy
	rootCert     *tls.Config
	state        *stateManager
	quit         chan struct{}
}

func (s *rpcServ) loadRegistrations() {
	attempted := 0
	loaded := 0
	s.state.ForEachRegistration(func(registration *Registration) {
		attempted += 1
		if registration.Request.FixedPort == 0 {
			// Request a new lease for the port we had before if the original request
			// was for a random port
			registration.Request.FixedPort = registration.Lease.Port
		}
		// If the lease is expired, give an extension. Otherwise just register for
		// the same lease timeout.
		//
		// This way clients won't get a randomly shorter timeout than they thought
		// they had, and if we missed the renew while portal was down they get an
		// extension.
		timeoutTime := registration.Lease.Timeout.AsTime()
		if now := time.Now(); now.After(timeoutTime) {
			timeoutTime = now.Add(randomTTL(leaseTTL))
		}
		_, err := s.internalRegister(registration.Lease.Address, registration.Request, timeoutTime)
		if err != nil {
			log.Printf("Failed to recreate registration: %v\n%v", err, registration)
			return
		}
		loaded += 1
	})
	if attempted > 0 {
		log.Printf("Successfully loaded %v/%v saved registrations", loaded, attempted)
	}
}

func (s *rpcServ) MyHostname(ctx context.Context, empty *emptypb.Empty) (*gate.Hostname, error) {
	p, _ := peer.FromContext(ctx)
	clientAddr, _, err := net.SplitHostPort(p.Addr.String())
	if err != nil {
		return nil, err
	}
	return &gate.Hostname{Hostname: clientAddr}, nil
}

// Register registers a new forwarding rule to the rpc client's ip address.
// Randomly assigns port for the client to listen on
func (s *rpcServ) Register(ctx context.Context, request *gate.RegisterRequest) (*gate.Lease, error) {
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

	lease, err := s.internalRegister(clientAddr, request, time.Time{})
	if err != nil {
		log.Printf("Registration failed (%v), failed to setup proxy: %v", request.Pattern, err)
		return nil, err
	}
	if err = s.state.SaveRegistration(&Registration{
		Request: request,
		Lease:   lease,
	}); err != nil {
		return nil, err
	}
	// TODO: should the cert code just go in port_leasor?
	//
	// Currently the cert doesn't get saved in the state which is kind of weird.
	// But if we put it in port_leasor then when you load the state we would sign
	// a new cert and store that which the client would never see.
	if err := s.signCert(request, lease); err != nil {
		log.Printf("Registration failed (%v), failed to sign certificate reqest: %v", request.Pattern, err)
		leasor := s.clientLeasor.PortLeasorForClient(clientAddr)
		leasor.Unregister(lease)
		return nil, err
	}
	return lease, nil
}

func (s *rpcServ) internalRegister(clientAddr string, request *gate.RegisterRequest, fixedTimeout time.Time) (lease *gate.Lease, err error) {
	if strings.HasPrefix(request.Pattern, tcpProxyPrefix) {
		lease, err = s.tcpProxy.Register(clientAddr, request, fixedTimeout)
	} else {
		lease, err = s.httpProxy.Register(clientAddr, request, fixedTimeout)
	}
	return
}

func (s *rpcServ) signCert(request *gate.RegisterRequest, lease *gate.Lease) error {
	if len(request.GetCertificateRequest()) != 0 {
		root, err := s.rootCert.GetCertificate(nil)
		if err != nil {
			return err
		}
		// We want the expiration to be at least 2x the lease because if the server
		// is restarted you get a refreshed TTL and we want the cert to survive
		// until the next renew happens
		newCert, err := tools.SignCertificate(root,
			request.CertificateRequest, lease.Timeout.AsTime(), false)
		if err != nil {
			return err
		}
		lease.Certificate = [][]byte{newCert}
	}
	return nil
}

// Unregister unregisters the forwarding rule with the given pattern
func (s *rpcServ) Unregister(ctx context.Context, lease *gate.Lease) (*gate.Lease, error) {
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
func (s *rpcServ) Renew(ctx context.Context, lease *gate.Lease) (*gate.Lease, error) {
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

	// TODO: there's some mutex weirdness here. Between this check and the one
	// below there might have been an Unregister of this lease. For that matter
	// it's weird everywhere that portLeasor has a separate mutex from the state.
	//
	// Not sure if this causes any real problems
	registration := s.state.LookupRegistration(lease)
	if registration == nil {
		log.Print("Programming Error: no registration found in state in renew for lease: ", leaseString(lease))
	}

	// Renew the certificate if we had one
	if err := s.signCert(registration.GetRequest(), newLease); err != nil {
		log.Printf("Error renewing certificate (%v): %v", leaseString(newLease), err)
		return nil, err
	}

	if err := s.state.RenewRegistration(newLease); err != nil {
		log.Printf("Error renewing lease state: %v", err)
	}
	return newLease, nil
}

// StartNew creates a new RPCServ and starts it
func startRPCServer(clientLeasor *clientLeasor,
	tcpProxy *tcpProxy, httpProxy *httpProxy,
	port int, rootCert *tls.Config,
	state *stateManager, quit chan struct{}) (*rpcServ, error) {

	s := &rpcServ{
		clientLeasor: clientLeasor,
		state:        state,
		tcpProxy:     tcpProxy,
		httpProxy:    httpProxy,
		quit:         quit,
		rootCert:     rootCert,
	}
	clientLeasor.OnCancel(s.state.Unregister)
	s.loadRegistrations()
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
	l, err := listenerFromPortOrFD(port)
	if err != nil {
		return nil, fmt.Errorf("Failed to start listener: %v", err)
	}
	go func() {
		server.Serve(l) // logs any errors itself instead of returning
		log.Print("RPC server died, quitting")
		select {
		case <-quit:
		default:
			close(quit)
		}
	}()
	go func() {
		<-quit
		server.GracefulStop()
		l.Close()
	}()
	return s, nil
}
