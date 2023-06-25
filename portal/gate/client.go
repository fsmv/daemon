/*
The client library for registering paths with [ask.systems/daemon/portal].

This package contains the raw [gRPC] service [protos] in addition to helper
functions for using the service. The helpers are:

  - [StartTLSRegistration], [MustStartTLSRegistration], [StartRegistration], and
    [MustStartRegistration] are all in one helpers for getting a lease from
    portal and keeping it renewed in the background. Most clients should use
    one of these.
  - [ParsePattern] which splits out the hostname part of multi-hostname patterns
    used to register with portal when portal is hosting multiple URLs. This is
    needed to extract the path part that can be used with [http.Handle].
  - [Client] and the methods under it provide full access to the gRPC server
    using the token authentication. Call methods directly using [Client].RPC if
    you want detailed access to portal.

You need to set the [Address] and [Token] vars to use the 4 helper functions
like [StartTLSRegistration] so it can connect to portal. The simplest way to
do this is to import the [ask.systems/daemon/portal/flags] library:

	import (
		_ "ask.systems/daemon/portal/flags"
	)

When running portal client binaries on the commandline you can use the
PORTAL_ADDR and PORTAL_TOKEN environment variables to easily set the values.

[gRPC]: https://grpc.io/
[protos]: https://developers.google.com/protocol-buffers
*/
package gate

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"ask.systems/daemon/tools"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/protobuf/types/known/emptypb"
)

var (
	NotRegisteredError = errors.New("pattern not registered")
)

type Client struct {
	// Call any of the service.proto functions here
	RPC  PortalClient
	conn *grpc.ClientConn
}

// Configuration for connecting to portal
//
// If you read these vars directly, call [ResolveFlags] first!
//
// These are set by the [ask.systems/daemon/portal/flags] library. If you don't want to
// use the flags you can set the values here.
var (
	// The hostname (or IP) and port of the portal server to connect to.
	Address *string
	// The API authentication token for portal RPCs. Portal logs this on startup.
	Token *string
)

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

// Make a connection to the portal RPC service and send the registration
// request. Also starts a goroutine to renew the lease (using
// [Client.KeepLeaseRenewed]) until the quit channel is closed.
//
// Returns the initial lease or an error if the registration didn't work.
func StartRegistration(request *RegisterRequest, quit <-chan struct{}) (*Lease, error) {
	if err := ResolveFlags(); err != nil {
		return nil, err
	}
	c, err := Connect(*Address, *Token)
	if err != nil {
		return nil, err
	}
	lease, err := c.RPC.Register(context.Background(), request)
	if err != nil {
		return nil, fmt.Errorf("Failed to obtain lease from portal: %v", err)
	}
	log.Printf("Obtained lease for %#v, port: %v, ttl: %v",
		lease.Pattern, lease.Port, lease.Timeout.AsTime())
	go c.KeepLeaseRenewed(quit, lease)
	return lease, nil
}

// The same as [StartRegistration] but call [log.Fatal] on error
func MustStartRegistration(request *RegisterRequest, quit <-chan struct{}) *Lease {
	lease, err := StartRegistration(request, quit)
	if err != nil {
		log.Fatal(err)
	}
	return lease
}

// The same as [StartTLSRegistration] but call [log.Fatal] on error
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
// Starts a goroutine to renew the both the lease and the TLS certificate
// (using [Client.KeepLeaseRenewedTLS]) until the quit channel is closed.
//
// Returns the initial lease, and a [tls.Config] which automatically
// renews the certificate seemlessly. Or return an error if the registration
// didn't work.
func StartTLSRegistration(request *RegisterRequest, quit <-chan struct{}) (*Lease, *tls.Config, error) {
	if err := ResolveFlags(); err != nil {
		return nil, nil, err
	}
	c, err := Connect(*Address, *Token)
	if err != nil {
		return nil, nil, err
	}

	// Setup the request for a new certificate
	hostResp, err := c.RPC.MyHostname(context.Background(), &emptypb.Empty{})
	if err != nil {
		return nil, nil, fmt.Errorf("Error from MyHostname: %v", err)
	}
	csr, privateKey, err := tools.GenerateCertificateRequest(hostResp.Hostname)
	request.CertificateRequest = csr
	if err != nil {
		return nil, nil, fmt.Errorf("Error generating cert request: %v", err)
	}

	// Do the registration
	lease, err := c.RPC.Register(context.Background(), request)
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to obtain lease from portal: %v", err)
	}
	log.Printf("Obtained TLS lease for %#v, port: %v, ttl: %v",
		lease.Pattern, lease.Port, lease.Timeout.AsTime())

	// Keep the lease renewed and set up a TLS config that uses the auto-renewed
	// TLS certificate
	certCache := &atomic.Value{}
	certCache.Store(lease.Certificate)
	go c.KeepLeaseRenewedTLS(quit, lease, func(cert []byte) { certCache.Store(cert) })
	config := &tls.Config{
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			cert := tools.CertificateFromSignedCert(certCache.Load().([]byte), privateKey)
			return cert, nil
		},
	}
	return lease, config, nil
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

// Connect to the portal RPC server and don't do anything else. Use this if you
// want to call the proto RPCs directly.
func Connect(portalAddr, token string) (Client, error) {
	conn, err := grpc.Dial(portalAddr,
		grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
			InsecureSkipVerify: true,
		})),
		grpc.WithPerRPCCredentials(rpcToken(token)),
	)
	if err != nil {
		return Client{}, fmt.Errorf("Failed to connect to frontend proxy RPC server: %v", err)
	}
	return Client{NewPortalClient(conn), conn}, nil
}

// Close the connection to the RPC server
func (c Client) Close() {
	c.conn.Close()
}

// Run a loop to call the Renew RPC for the given lease before the lease expires
// until the quit channel is closed. Intended to be run in a goroutine.
//
// When the quit channel is closed this function unregisters the lease and
// closes the client.
func (c Client) KeepLeaseRenewed(quit <-chan struct{}, lease *Lease) {
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
func (c Client) KeepLeaseRenewedTLS(quit <-chan struct{}, lease *Lease, newCert func([]byte)) {
	defer func() {
		c.RPC.Unregister(context.Background(), lease)
		c.Close()
		log.Printf("portal lease %#v unregistered and connection closed",
			lease.Pattern)
	}()
	const bufferTime = time.Hour // so we don't let the lease expire
	timer := time.NewTimer(time.Until(lease.Timeout.AsTime()) - bufferTime)
	for {
		select {
		case <-quit:
			timer.Stop()
			return
		case <-timer.C:
		}
		var err error
		newLease, err := c.RPC.Renew(context.Background(), lease)
		if err != nil {
			/*if err == NotRegisteredError {
			    // TODO: we would need to save the RegisterRequest options to do
			    // this right. Also would my code work if the port changes?
			    log.Print("Got NotRegisteredError, attempting to register.")
			    err = c.client.Invoke("Register", &RegisterRequest{Pattern:lease.Pattern}, lease)
			    if err != nil {
			        log.Printf("Error from register: %v", err)
			    }
			} else {*/
			log.Printf("Error from renew: %v", err)
			timer.Reset(2 * time.Minute)
			continue
			//}
		}
		lease = newLease
		if newCert != nil {
			newCert(lease.Certificate)
		}
		timeout := lease.Timeout.AsTime()
		log.Printf("Renewed lease, port: %v, ttl: %v", lease.Port, timeout)
		timer.Reset(time.Until(timeout) - bufferTime)
	}
}
