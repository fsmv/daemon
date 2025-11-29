/*
The client library for registering paths with [ask.systems/daemon/portal].

This package contains the raw [gRPC] service [protos] in addition to helper
functions for using the service. The helpers are:

  - [AutoRegister] an all in one helper for getting a lease from portal and
    keeping it renewed in the background. Most clients should use this. If you
    need multiple registrations use [Client.AutoRegister].
  - [ParsePattern] which splits out the hostname part of multi-hostname patterns
    used to register with portal when portal is hosting multiple URLs. This is
    needed to extract the path part that can be used with [net/http.Handle].
  - [Client] and the methods under it provide full access to the gRPC server
    using the token authentication. Call methods directly using
    [gate.Client.RPC] if you want detailed access to portal.
  - This package contains generated [google.golang.org/protobuf/proto.Message]
    types. They are [Lease], [RegisterRequest], [Hostname], [PortalClient],
    [PortalServer], [UnimplementedPortalServer], and [UnsafePortalServer]

You need to set the [Address] and [Token] vars to use the helper functions like

[AutoRegister] and [DefaultClient] so it can connect to portal. The simplest way
to do this is to import the [ask.systems/daemon/portal/flags] library:

	import (
		_ "ask.systems/daemon/portal/flags"
	)

TODO: a full autoregister code snippet here probably is the easiest thing

When running portal client binaries on the commandline you can use the
PORTAL_ADDR and PORTAL_TOKEN environment variables to easily set the values,
which is handled by [ResolveFlags] (called by [DefaultClient]).

[gRPC]: https://grpc.io/
[protos]: https://developers.google.com/protocol-buffers
*/
package gate

import (
	"context"
	"crypto"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"ask.systems/daemon/internal/portalpb"
	"ask.systems/daemon/tools"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

// Configuration for connecting to portal
//
// If you read these vars directly, call [ResolveFlags] first!
//
// These are used by [DefaultClient] and [AutoRegister]. They are set by the
// [ask.systems/daemon/portal/flags] library. If you don't want to use the flags
// you can set the values here, or use the PORTAL_ADDR and PORTAL_TOKEN env vars
// read by [ResolveFlags].
var (
	// The hostname (or IP) and port of the portal server to connect to.
	Address *string
	// The API authentication token for portal RPCs. Portal logs this on startup.
	Token *string
)

var ServiceProto = portalpb.ServiceProto

// The options proto when making a requests to portal to register for a reverse proxy
// path [Lease].
//
// The minimal request is to only set [gate.RegisterRequest.Pattern].
// [GoAutoRegister] will automatically set the CertificateRequest.
//
// See also: [tools.GenerateCertificateRequest] to create CertificateRequest if
// doing custom cert management.
type RegisterRequest struct {
	// For HTTP: A url pattern that works with http.DefaultServMux. Ex: /images/
	// For TCP: ":tcp:port" for the port number portal should listen on. Only tcp
	// is accepted for now.
	//
	// HTTP patterns optionally accept a hostname (URL) constraint prefix. Or if
	// portal is configured to use the default hostname for no hostname patterns,
	// you can use * for the hostname to always match all URLs. For example:
	//
	//     ask.systems/images/
	//     */favicon.ico
	//
	// You car register multiple distinct patterns on the same port using
	// FixedPort. You can get a random port the first time then re-use that port
	// in later requests in FixedPort.
	//
	// Existing leases with the same pattern will be replaced
	Pattern string

	// Optional: If set this port for the lease instead of getting a random port
	// assigned by portal.
	//
	// Reserves the specified port so portal will not randomly assign it to a
	// client. You can register multiple pattens on the same port if you use
	// FixedPort but the patterns must be different.
	//
	// Existing leases with the same port and pattern will be replaced
	FixedPort uint16

	// Optional: If set, forward the requests for pattern to this IP/hostname.
	// If unset, forward requests to the IP that sent the RegisterRequest.
	//
	// It is easiest to just run assimilate on the machine you want the forwarding
	// rule for, but if you have a cpanel-only host or otherwise don't have access
	// to run arbitrary code then you can use this setting to run assimilate on
	// another machine.
	//
	// If you use this you need to be mindful of TLS and the network you're
	// sending the data over. Ideally you should set up a self-signed certificate
	// on the other machine and portal will detect TLS support. Otherwise make
	// sure you only use this with a trusted local network.
	Hostname string

	// If true, remove the pattern in the URL of HTTP requests we forward to the
	// backend to hide that it is behind a reverse proxy.
	//
	// For example: If the pattern is /foo/ and the request is for /foo/index.html
	//   - If true  the request to the backend is /index.html
	//   - If false the request to the backend is /foo/index.html
	//
	// Ignored for TCP proxies.
	StripPattern bool

	// If true, do not redirect HTTP requests to HTTPS. This means the data will
	// be sent in plain-text readable to anyone if the client requests it. Some
	// legacy systems require plain HTTP requests. Leave this off by default for
	// security, that way responses will only be readable by the client.
	//
	// Ignored for TCP proxies.
	AllowHttp bool

	// If set, the server will sign the certificate request with portal's
	// certificate as the root and accept connections to the signed cert. This way
	// network traffic behind the reverse proxy can be encrypted.
	//
	// Use this instead of a self signed cert.
	//
	// You can use [tools.GenerateCertificateRequest] to make this.
	// It is ASN.1 DER data.
	CertificateRequest []byte

	// If you set CertificateRequest, set this to the corresponding key. This is
	// not actually sent over the network, it is used by the client to cleate the
	// [*tls.Certificate] in the [Lease].
	PrivateKey crypto.PrivateKey
}

type Lease struct {
	lease *portalpb.Lease

	// The pattern that this lease was registered with
	Pattern string
	// The hostname this pattern/port lease is bound to
	// Same as the result from [Client.MyHostname]
	Hostname string
	// The port either assigned by portal or requested with FixedPort
	Port uint16
	// When the lease becomes invalid
	Timeout time.Time
	// If requested, the certificate that has been signed by portal. Use for
	// serving TLS requests to portal.
	Certificate *tls.Certificate
}

func makeLease(lease *portalpb.Lease, privateKey crypto.PrivateKey) (*Lease, error) {
	if lease.Port > (1<<16)-1 {
		return nil, fmt.Errorf("Port out of range for lease %#v: %v", lease.Pattern, lease.Port)
	}

	var cert *tls.Certificate
	if privateKey != nil {
		// Needs to be updated on renew, when newCert is called
		// TODO: support reading the portal CA cert for client auth
		//caCert := &tls.Certificate{
		//	Certificate: lease.Certificate[1:],
		//}

		var err error
		cert, err = tools.TLSCertificateFromBytes(lease.Certificate, privateKey)
		if err != nil {
			err := fmt.Errorf("Failed to parse certificate for lease %#v: %w", lease.Pattern, err)
			return nil, err
		}
	}

	return &Lease{
		lease:       lease,
		Certificate: cert,
		Port:        uint16(lease.Port),
		Pattern:     lease.Pattern,
		Hostname:    lease.Address,
		Timeout:     lease.Timeout.AsTime(),
	}, nil
}

var (
	NotRegisteredError = errors.New("pattern not registered")
)

const failRetryDelay = 2 * time.Minute

// Holds a gRPC client and connection, handles authentication with the token.
//
// Use [DefaultClient] typically and [Connect] for more direct control.
//
// If you use [AutoRegister] it makes a client internally.
type Client struct {
	rpc  portalpb.PortalClient
	conn *grpc.ClientConn
}

func (c *Client) Register(ctx context.Context, req *RegisterRequest) (*Lease, error) {
	lease, err := c.rpc.Register(ctx, &portalpb.RegisterRequest{
		Pattern:            req.Pattern,
		FixedPort:          uint32(req.FixedPort),
		Hostname:           req.Hostname,
		StripPattern:       req.StripPattern,
		AllowHttp:          req.AllowHttp,
		CertificateRequest: req.CertificateRequest,
	})
	if err != nil {
		return nil, err
	}
	return makeLease(lease, req.PrivateKey)
}

func (c *Client) Renew(ctx context.Context, lease *Lease) (*Lease, error) {
	newLease, err := c.rpc.Renew(ctx, lease.lease)
	if err != nil {
		return nil, err
	}
	return makeLease(newLease, lease.Certificate.PrivateKey)
}

func (c *Client) Unregister(ctx context.Context, lease *Lease) error {
	_, err := c.rpc.Unregister(ctx, lease.lease)
	return err
}

// Returns the address that will be used to connect to your server if
// registered. It is necessary to register the correct hostname in the TLS
// certificate signed by portal.
func (c *Client) MyHostname(ctx context.Context) (string, error) {
	resp, err := c.rpc.MyHostname(ctx, &emptypb.Empty{})
	if err != nil {
		return "", err
	}
	return resp.Hostname, nil
}

func renewDuration(deadline time.Time) time.Duration {
	remainingTime := time.Until(deadline)
	buffer := remainingTime / 10
	// For really short TTLs, used in tests, we need to have a minimum buffer time
	// so that we don't detect an expired cert.
	if buffer < time.Second {
		buffer = time.Second
		if buffer >= remainingTime {
			return 0
		}
	}
	return remainingTime - buffer
}

// Sets up [Address] and [Token] based on optional
// [ask.systems/daemon/portal/flags] values and with fallback to using the
// PORTAL_ADDR and PORTAL_TOKEN environment variable values if the the variables
// were not already set in code or by the flags.
func ResolveFlags() error {
	noSetup := (Address == nil && Token == nil)
	// Check if we have the default value of the addr flag without exporting a
	// constant in either package.
	addrFlagSet := false
	flag.Visit(func(f *flag.Flag) { // Only visits flags that were set
		if f.Name == "portal_addr" {
			addrFlagSet = true
		}
	})
	if !addrFlagSet {
		envAddr := os.Getenv("PORTAL_ADDR")
		// Don't overwrite the default flag value if it's empty.
		// But if the user didn't include the flags library we allow setting to "".
		// They might have set *Address to "" but that's their problem.
		if envAddr != "" && Address == nil {
			Address = &envAddr
		}
	}
	if Token == nil || *Token == "" { // simpler because the flag default is ""
		envToken := os.Getenv("PORTAL_TOKEN")
		Token = &envToken
	}
	if *Token == "" {
		if noSetup {
			return errors.New("" +
				"You need to set the portal address and token (printed on portal startup)\n" +
				"to use these helper functions. You can import _ \"ask.systems/daemon/portal/flags\"\n" +
				"or set gate.Address and gate.Token from your code. And if the values aren't\n" +
				"already set, the PORTAL_ADDR and PORTAL_TOKEN environment variables will be used.")
		}
		return errors.New("An API token is required to connect to portal. It is printed in the portal logs on startup.")
	}
	return nil
}

// TODO: Can we have this?
//func AutoTLSConf(<-chan *Lease) *tls.Config
// not ideal because you kinda have to duplicate the channel

// TODO: decide on context vs quit chan for AutoRegister
// We are kind of inconsistent right now because tools.HTTPServer uses quit chan

// Start a new registration with portal and keep it renewed.
//
// Uses the [DefaultClient] and calls [Client.AutoRegister] see the
// documentation there for details on arguments and return values.
//
// The lease is kept renewed until the context is cancelled.
//
// You can pass in a non-nil [sync.WaitGroup] and call Wait on it to allow time
// for the lease to be unregistered when the context is cancelled.
//
// Returns the result from the initial registration before any renewals.
// The error return is always the same as [gate.AutoRegisterResult.Error].
//
// TODO: why not just
// func AutoRegister(ctx context.Context, request *RegisterRequest) (<-chan *Lease, error) {
//   - Hmm if we pass the lease chan to AutoTLSConf then we can't also wait on
//     it.
//   - I could make a helper to duplicate a chan but it would have to use
//     generics
//
// TODO: should it be wait <-chan error instead? That would be for in case the
// client wants to have a fatal error on renew sometimes. Maybe I should just
// alawys retry. If I retry always for now and then change later it's not like
// users are going to actually have log statements for those errors anyway.
func AutoRegister(ctx context.Context, request *RegisterRequest) (port uint16, tlsconf *tls.Config, wait <-chan struct{}, err error) {
	c, err := DefaultClient()
	if err != nil {
		done := make(chan struct{})
		close(done)
		return 0, nil, done, err
	}
	lease, conf, wait, err := c.goAutoRegister(ctx, request, c.Close)
	if debugleaseAny := ctx.Value("debuglease"); debugleaseAny != nil {
		if debuglease, ok := debugleaseAny.(*atomic.Value); ok {
			debuglease.Store(lease)
		}
	}
	return lease.Port, conf, wait, err
}

// Parse a pattern in the syntax accepted by portal separating the hostname
// (URL) part of the pattern from the path part. The path part is then
// compatible with [net/http.Handle]
//
// This is needed to host multiple URLs with portal.
func ParsePattern(pattern string) (host, path string) {
	path = pattern
	firstSlash := strings.Index(pattern, "/")
	if firstSlash > 0 {
		host = pattern[:firstSlash]
		path = pattern[firstSlash:]
	}
	return
}

type rpcToken string

func (token rpcToken) GetRequestMetadata(ctx context.Context, uri ...string) (map[string]string, error) {
	return map[string]string{
		"authorization": string(token),
	}, nil
}

func (token rpcToken) RequireTransportSecurity() bool {
	return true
}

// Create the portal RPC client using the [ask.systems/daemon/portal/flags]
// package configuration. Calls [ResolveFlags] and [Connect].
func DefaultClient() (*Client, error) {
	if err := ResolveFlags(); err != nil {
		return nil, err
	}
	return Connect(*Address, *Token)
}

// Create the portal RPC client and don't do anything else. Use this if you
// want to call the proto RPCs directly and don't want to use
// [ask.systems/daemon/portal/flags].
//
// For most use cases [DefaultClient] is what you want.
//
// Note: this function doesn't actually perform I/O anymore. Originally it used
// [grpc.Dial] but now it uses [grpc.NewClient]. See: [grpc antipatterns]
//
// [grpc antipatterns]: https://github.com/grpc/grpc-go/blob/master/Documentation/anti-patterns.md
func Connect(portalAddr, token string) (*Client, error) {
	conn, err := grpc.NewClient(portalAddr,
		grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
			InsecureSkipVerify: true,
		})),
		grpc.WithPerRPCCredentials(rpcToken(token)),
	)
	if err != nil {
		return nil, fmt.Errorf("Failed to connect to portal RPC server: %w", err)
	}
	return &Client{portalpb.NewPortalClient(conn), conn}, nil
}

// Close the connection to the RPC server
func (c *Client) Close() {
	if c != nil {
		c.conn.Close()
	}
}

// If autoTLSConfig() was public users could easily make the newLease callback
// of AutoRegister() to just call setCert(). Users wouldn't need to do this
// weird return the first cert thing.
//
// The complicated part of this function is that in the error case it's
// necessary to close ret after done so that we can safely return oerr. It would
// be a bit simpler if I used an extra channel for returning an error at the
// end, then I could use a select statement.
func (c *Client) goAutoRegister(ctx context.Context, request *RegisterRequest, cleanup func()) (lease *Lease, tlsconf *tls.Config, wait <-chan struct{}, err error) {
	var oerr error
	done := make(chan struct{})
	ret := make(chan *Lease)
	conf, setCert := autoTLSConfig()
	go func() {
		first := true
		oerr = c.AutoRegister(ctx, request, func(newLease *Lease) {
			setCert(newLease.Certificate)
			if first {
				ret <- newLease
				close(ret)
				first = false
			}
		})
		if cleanup != nil {
			cleanup()
		}
		close(done)
		if first {
			close(ret)
		}
	}()

	if lease, ok := <-ret; ok {
		return lease, conf, done, nil
	} else {
		return nil, nil, done, oerr
	}
}

// TODO: We should do client auth so that nobody but the portal server can
// send requests directly to the backend. This completes a secure chain for
// the X-Forwarded header values. These headers may be important for IP based
// blocking and logging or even generating server side URLs.
//
// - Set conf.ClientAuth to tls.RequireAndVerifyClientCert
// - Set conf.ClientCAs to the portal cert (and make sure to keep it up to date)
//   - It would be nice to have some kind of push update system because if
//     we use a timer requests wouldn't be valid anymore when the old cert
//     expires
//   - Maybe portal can send two client certs the old and the new and we
//     update that way?
//
// - on portal we need to set conf.GetClientCertificate
//
// TODO: eventually turn this into something public and use it in
// tools.AutorenewSelfSignedCertificate
func autoTLSConfig() (conf *tls.Config, setCert func(*tls.Certificate)) {
	certCache := &atomic.Value{}

	setCert = func(c *tls.Certificate) {
		certCache.Store(c)
	}
	conf = &tls.Config{
		GetCertificate: func(hi *tls.ClientHelloInfo) (*tls.Certificate, error) {
			cert, ok := certCache.Load().(*tls.Certificate)
			if !ok || cert == nil {
				return nil, errors.New("Internal error: cannot load certificate")
			}
			if hi != nil {
				if err := hi.SupportsCertificate(cert); err != nil {
					return nil, err
				}
			}
			return cert, nil
		},
	}

	return
}

// Starts a new registration with portal and keeps it renewed. Blocks until the
// context is cancelled, so call it in a goroutine.
//
// Automatically generates a certificate request if
// [gate.RegisterRequest.CertificateRequest] is left nil. If you set your own
// CertificateRequest it will used as-is and [gate.AutoRegisterResult.Lease]
// will contain the signed cert but you will have to make your own [tls.Config].
// If you want to turn off requesting a certificate entirely you can set
// [gate.RegisterRequest.CertificateRequest] to a non-nil empty slice.
//
// Note: portal requires HTTPS if it signed a certificate with the lease and
// only accepts the certificate it signed.
//
// If non-nil, result will be blocking written to after the initial
// registration attempt is done, and then non-blocking written to for each
// renewal. It will be closed when AutoRegister returns.
//
// When you read the first result, if [gate.AutoRegisterResult.Error] is non-nil
// then this function will shortly return the same error, otherwise this
// function will continue to renew the registration, eventually returning with
// the error that caused it to stop.
func (c *Client) AutoRegister(ctx context.Context, request *RegisterRequest, newLease func(*Lease)) error {
	// Add a certificate request if one isn't already set
	var privateKey crypto.PrivateKey
	if request.CertificateRequest == nil {
		// Setup the request for a new certificate
		hostname, err := c.MyHostname(ctx)
		if err != nil {
			err = fmt.Errorf("Error from MyHostname: %w", err)
			return err
		}
		request.CertificateRequest, privateKey, err = tools.GenerateCertificateRequest(hostname)
		if err != nil {
			err = fmt.Errorf("Error generating cert request: %w", err)
			return err
		}
		request.PrivateKey = privateKey
	}

	// Do the initial registration
	lastLease, err := c.Register(ctx, request)
	if err != nil {
		err = fmt.Errorf("Failed to obtain lease from portal: %w", err)
		return err
	}
	log.Printf("Obtained lease for %#v, port: %v, ttl: %v",
		lastLease.lease.Pattern, lastLease.lease.Port, lastLease.lease.Timeout)

	if newLease != nil {
		newLease(lastLease)
	}

	// Set the FixedPort to the Port we got so we never change the port if we
	// re-register. Then we don't have to restart the server.
	request.FixedPort = lastLease.Port
	// Wait until 1% of the time is remaining
	timer := time.NewTimer(renewDuration(lastLease.Timeout))
	for {
		select {
		case <-ctx.Done():
			timer.Stop()
			// Use the background context because if we're here the context is
			// cancelled and we still want to unregister.
			// TODO: timeout
			err := c.Unregister(context.Background(), lastLease)
			if err == nil {
				log.Printf("portal lease %#v unregistered",
					lastLease.Pattern)
			} else {
				log.Printf("Error unregistering portal lease %#v: %v",
					lastLease.Pattern, err)
			}
			return context.Cause(ctx)
		case <-timer.C:
		}
		// TODO: we should be generating a new privateKey for each renew
		// both for rotating keys and forward secrecy reasons and so that we can make
		// the value we pass to newLease completely not used by us anymore after the
		// call
		nextLease, err := c.Renew(ctx, lastLease)
		action := "Renewed"
		if err != nil && status.Code(err) == codes.NotFound {
			log.Print("Tried to renew but the lease wasn't valid, trying to re-register...")
			nextLease, err = c.Register(ctx, request)
			if err != nil {
				log.Printf("Failed to re-register lease from portal: %v", err)
				timer.Reset(failRetryDelay)
				continue
			}
			action = "Re-registered"
		} else if err != nil {
			log.Printf("Error from renew: %v", err)
			timer.Reset(failRetryDelay)
			continue
		}
		if newLease != nil {
			newLease(nextLease)
		}
		lastLease = nextLease

		log.Printf("%v lease, port: %v, ttl: %v", action, lastLease.Port, lastLease.Timeout)
		timer.Reset(renewDuration(lastLease.Timeout))
	}
}

// Make a connection to the portal RPC service and send the registration
// request. Also starts a goroutine to renew (and potentially re-register) the
// lease until the quit channel is closed.
//
// Returns the initial lease or an error if the registration didn't work.
//
// Deprecated: Use [AutoRegister] instead
func StartRegistration(request *RegisterRequest, quit <-chan struct{}) (*Lease, error) {
	c, err := DefaultClient()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-quit
		cancel()
	}()

	request.CertificateRequest = []byte{} // force no SSL cert signing
	lease, _, _, err := c.goAutoRegister(ctx, request, c.Close)
	return lease, err
}

// The same as [StartRegistration] but call [log.Fatal] on error
//
// Deprecated: Use [AutoRegister] instead
func MustStartRegistration(request *RegisterRequest, quit <-chan struct{}) *Lease {
	lease, err := StartRegistration(request, quit)
	if err != nil {
		log.Fatal(err)
	}
	return lease
}

// The same as [StartTLSRegistration] but call [log.Fatal] on error
//
// Deprecated: Use [AutoRegister] instead
func MustStartTLSRegistration(request *RegisterRequest, quit <-chan struct{}) (*Lease, *tls.Config) {
	lease, conf, err := StartTLSRegistration(request, quit)
	if err != nil {
		log.Fatal(err)
	}
	return lease, conf
}

// Make a connection to the portal RPC service and send the registration
// request. Additionally, generate a new TLS certificate and request portal to
// sign it with portal's private Certificate Authority (the key is never sent
// over the network).
//
// Starts a goroutine to renew the both the lease and the TLS certificate (and
// re-register the lease if necessary) until the quit channel is closed.
//
// Returns the initial lease, and a [tls.Config] which automatically
// renews the certificate seamlessly. Or return an error if the registration
// didn't work.
//
// Deprecated: Use [AutoRegister] instead
func StartTLSRegistration(request *RegisterRequest, quit <-chan struct{}) (*Lease, *tls.Config, error) {
	c, err := DefaultClient()
	if err != nil {
		return nil, nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-quit
		cancel()
	}()

	lease, conf, _, err := c.goAutoRegister(ctx, request, c.Close)
	return lease, conf, err
}

// Run a loop to call the Renew RPC for the given lease before the lease expires
// until the quit channel is closed. Intended to be run in a goroutine.
//
// When the quit channel is closed this function unregisters the lease and
// closes the client.
//
// Deprecated: It's best to use [AutoRegister] because this function cannot
// re-register the lease in the unlikely event that the server somehow forgets
// the lease exists when it tries to renew, because it does not have access to
// the registration request. In that case this function gets stuck trying to
// renew forever. The server can't do it because it has forgotten in this
// scenario.
//
// Deprecated: Use [AutoRegister] instead
func (c *Client) KeepLeaseRenewed(quit <-chan struct{}, lease *Lease) {
	c.KeepLeaseRenewedTLS(quit, lease, nil)
}

// Run a loop to call the Renew RPC for the given lease before the lease expires
// until the quit channel is closed. Intended to be run in a goroutine.
//
// Additionally, if the lease contains a TLS certificate request, the
// certificate is renewed with the lease. Each time the certificate is renewed,
// the newCert function is called with the cert data.
//
// When the quit channel is closed this function unregisters the lease and
// closes the client.
//
// Deprecated: It's best to use [AutoRegister] because this function cannot
// re-register the lease in the unlikely event that the server somehow forgets
// the lease exists when it tries to renew, because it does not have access to
// the registration request. In that case this function gets stuck trying to
// renew forever. The server can't do it because it has forgotten in this
// scenario.
//
// Deprecated: Use [AutoRegister] instead
func (c *Client) KeepLeaseRenewedTLS(quit <-chan struct{}, lease *Lease, newCert func([]byte)) {
	defer func() {
		c.Unregister(context.Background(), lease)
		c.Close()
		log.Printf("portal lease %#v unregistered and connection closed",
			lease.Pattern)
	}()
	// Wait until 1% of the time is remaining
	timer := time.NewTimer(renewDuration(lease.Timeout))
	for {
		select {
		case <-quit:
			timer.Stop()
			return
		case <-timer.C:
		}
		newLease, err := c.Renew(context.Background(), lease)
		if err != nil {
			log.Printf("Error from renew: %v", err)
			timer.Reset(failRetryDelay)
			continue
		}
		lease = newLease
		if newCert != nil {
			newCert(lease.Certificate.Certificate[0]) // TODO: CA cert?
		}
		timeout := lease.Timeout
		log.Printf("Renewed lease, port: %v, ttl: %v", lease.Port, timeout)
		timer.Reset(renewDuration(timeout))
	}
}
