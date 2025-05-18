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
	"sync"
	"sync/atomic"
	"time"

	"ask.systems/daemon/tools"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

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
	// Call any of the service.proto functions here
	RPC  PortalClient
	conn *grpc.ClientConn
}

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

func renewDuration(deadline time.Time) time.Duration {
	remainingTime := time.Until(deadline)
	return remainingTime - remainingTime/100
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
		if envAddr != "" || Address == nil {
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
func AutoRegister(ctx context.Context, request *RegisterRequest, wg *sync.WaitGroup) (*AutoRegisterResult, error) {
	c, err := DefaultClient()
	if err != nil {
		return &AutoRegisterResult{Error: err}, err
	}
	resultChan := make(chan *AutoRegisterResult, 0)
	go func() {
		if wg != nil {
			wg.Add(1)
		}
		c.AutoRegister(ctx, request, resultChan)
		c.Close()
		if wg != nil {
			wg.Done()
		}
	}()

	result := <-resultChan
	return result, result.Error
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
		c.Close()
	}()

	request.CertificateRequest = []byte{} // force no SSL cert signing
	resultChan := make(chan *AutoRegisterResult, 0)
	go c.AutoRegister(ctx, request, resultChan)

	result := <-resultChan
	return result.Lease, result.Error
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
		c.Close()
	}()
	resultChan := make(chan *AutoRegisterResult, 0)
	go c.AutoRegister(ctx, request, resultChan)

	result := <-resultChan
	return result.Lease, result.TLSConfig, result.Error
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
	return &Client{NewPortalClient(conn), conn}, nil
}

// Close the connection to the RPC server
func (c *Client) Close() {
	if c != nil {
		c.conn.Close()
	}
}

// The result type for [AutoRegister] and [Client.AutoRegister]
//
// See also: [gate.RegisterRequest.CertificateRequest].
type AutoRegisterResult struct {
	// The lease obtained in the initial registration request if successful.
	Lease *Lease
	// Will only be set if request.CertificateRequest was nil when
	// calling AutoRegister. In that case this config will automatically update
	// with the renewed certificate from portal.
	TLSConfig *tls.Config
	// The error status of the initial registration before moving on to auto-renew
	// from AutoRegister. If set the other fields are nil.
	Error error
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
// renewal. result will be closed when this function returns.
//
// When you read the first result, if [gate.AutoRegisterResult.Error] is non-nil
// then this function will shortly return the same error, otherwise this
// function will continue to renew the registration, eventually returning with
// the error that caused it to stop.
func (c *Client) AutoRegister(ctx context.Context, request *RegisterRequest, result chan<- *AutoRegisterResult) error {
	if result != nil {
		defer close(result)
	}

	// Add a certificate request if one isn't already set
	var privateKey crypto.Signer
	if request.CertificateRequest == nil {
		// Setup the request for a new certificate
		hostResp, err := c.RPC.MyHostname(ctx, &emptypb.Empty{})
		if err != nil {
			err = fmt.Errorf("Error from MyHostname: %w", err)
			if result != nil {
				result <- &AutoRegisterResult{Error: err}
			}
			return err
		}
		request.CertificateRequest, privateKey, err = tools.GenerateCertificateRequest(hostResp.Hostname)
		if err != nil {
			err = fmt.Errorf("Error generating cert request: %w", err)
			if result != nil {
				result <- &AutoRegisterResult{Error: err}
			}
			return err
		}
	}

	// Do the initial registration
	lease, err := c.RPC.Register(ctx, request)
	if err != nil {
		err = fmt.Errorf("Failed to obtain lease from portal: %w", err)
		if result != nil {
			result <- &AutoRegisterResult{Error: err}
		}
		return err
	}
	log.Printf("Obtained lease for %#v, port: %v, ttl: %v",
		lease.Pattern, lease.Port, lease.Timeout.AsTime())

	var updateConf func([]byte)
	var conf *tls.Config
	if privateKey != nil {
		cert, err := tools.TLSCertificateFromBytes([][]byte{lease.Certificate}, privateKey)
		if err != nil {
			err := fmt.Errorf("Failed to parse certificate for lease %#v: %w", lease.Pattern, err)
			if result != nil {
				result <- &AutoRegisterResult{Error: err}
			}
			return err
		}

		certCache := &atomic.Value{}
		certCache.Store(cert)
		updateConf = func(certBytes []byte) {
			newCert, err := tools.TLSCertificateFromBytes([][]byte{certBytes}, privateKey)
			if err != nil {
				log.Printf("Failed to parse renewed certificate from portal for lease %#v: %v",
					lease.Pattern, err)
			} else {
				certCache.Store(newCert)
			}
		}
		// TODO: We should do client auth so that nobody but the portal server can
		// send requests directly to the backend. This completes a secure chain for
		// the X-Forwarded header values. These headers may be important for IP based
		// blocking and logging or even generating server side URLs.
		conf = &tls.Config{
			GetCertificate: func(hi *tls.ClientHelloInfo) (*tls.Certificate, error) {
				cert, ok := certCache.Load().(*tls.Certificate)
				if !ok || cert == nil {
					log.Print("Failed to load certificate!")
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
	}

	initResult := &AutoRegisterResult{
		Lease:     lease,
		TLSConfig: conf,
		Error:     nil,
	}
	if result != nil {
		result <- initResult
	}
	return c.keepRegistrationAlive(ctx, request, initResult, result, updateConf)
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
func (c *Client) KeepLeaseRenewedTLS(quit <-chan struct{}, lease *Lease, newCert func([]byte)) {
	defer func() {
		c.RPC.Unregister(context.Background(), lease)
		c.Close()
		log.Printf("portal lease %#v unregistered and connection closed",
			lease.Pattern)
	}()
	// Wait until 1% of the time is remaining
	timer := time.NewTimer(renewDuration(lease.Timeout.AsTime()))
	for {
		select {
		case <-quit:
			timer.Stop()
			return
		case <-timer.C:
		}
		newLease, err := c.RPC.Renew(context.Background(), lease)
		if err != nil {
			log.Printf("Error from renew: %v", err)
			timer.Reset(failRetryDelay)
			continue
		}
		lease = newLease
		if newCert != nil {
			newCert(lease.Certificate)
		}
		timeout := lease.Timeout.AsTime()
		log.Printf("Renewed lease, port: %v, ttl: %v", lease.Port, timeout)
		timer.Reset(renewDuration(timeout))
	}
}

func (c *Client) keepRegistrationAlive(
	ctx context.Context, request *RegisterRequest,
	lastResult *AutoRegisterResult, result chan<- *AutoRegisterResult,
	newCert func(cert []byte)) error {
	// Set the FixedPort to the Port we got so we never change the port if we
	// re-register. Then we don't have to restart the server.
	request.FixedPort = lastResult.Lease.Port
	// Wait until 1% of the time is remaining
	timer := time.NewTimer(renewDuration(lastResult.Lease.Timeout.AsTime()))
	for {
		select {
		case <-ctx.Done():
			timer.Stop()
			// Use the background context because if we're here the context is
			// cancelled and we still want to unregister.
			_, err := c.RPC.Unregister(context.Background(), lastResult.Lease)
			if err == nil {
				log.Printf("portal lease %#v unregistered",
					lastResult.Lease.Pattern)
			} else {
				log.Printf("Error unregistering portal lease %#v: %v",
					lastResult.Lease.Pattern, err)
			}
			return context.Cause(ctx)
		case <-timer.C:
		}
		newLease, err := c.RPC.Renew(ctx, lastResult.Lease)
		action := "Renewed"
		if err != nil && status.Code(err) == codes.NotFound {
			log.Print("Tried to renew but the lease wasn't valid, trying to re-register...")
			newLease, err = c.RPC.Register(ctx, request)
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
		if newCert != nil {
			newCert(newLease.Certificate)
		}
		lastResult.Lease = newLease
		if result != nil {
			select {
			case result <- lastResult:
			default:
			}
		}
		timeout := newLease.Timeout.AsTime()
		log.Printf("%v lease, port: %v, ttl: %v", action, newLease.Port, timeout)
		timer.Reset(renewDuration(timeout))
	}
}
