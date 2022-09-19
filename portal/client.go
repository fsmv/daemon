package portal

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync/atomic"
	"time"

	"ask.systems/daemon/tools"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
)

var (
	NotRegisteredError = errors.New("pattern not registered")
)

type Client struct {
	RPC  PortalClient // Call any of the service.proto functions here
	conn *grpc.ClientConn
}

// Make a connection to the portal RPC service and send the registration
// request. Also starts a goroutine to renew the lease (KeepLeaseRenewed) until
// the quit channel is closed.
//
// See service.proto for request documentation.
// Returns the initial lease or an error if the registration didn't work.
func StartRegistration(portalAddr string, request *RegisterRequest, quit <-chan struct{}) (*Lease, error) {
	c, err := Connect(portalAddr)
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

func MustStartRegistration(portalAddr string, request *RegisterRequest, quit <-chan struct{}) *Lease {
	lease, err := StartRegistration(portalAddr, request, quit)
	if err != nil {
		log.Fatal(err)
	}
	return lease
}

func MustStartTLSRegistration(portalAddr string,
	request *RegisterRequest, quit <-chan struct{}) (*Lease, *tls.Config) {
	lease, conf, err := StartTLSRegistration(portalAddr, request, quit)
	if err != nil {
		log.Fatal(err)
	}
	return lease, conf
}

func StartTLSRegistration(portalAddr string,
	request *RegisterRequest, quit <-chan struct{}) (*Lease, *tls.Config, error) {

	c, err := Connect(portalAddr)
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

// Connect to the portal RPC server and don't do anything else. Use this if you
// want to call the proto RPCs directly.
func Connect(portalAddr string) (Client, error) {
	// TODO: TLS grpc server
	conn, err := grpc.Dial(portalAddr, grpc.WithInsecure())
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
		lease, err = c.RPC.Renew(context.Background(), lease)
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
			//}
		}
		if newCert != nil {
			newCert(lease.Certificate)
		}
		timeout := lease.Timeout.AsTime()
		log.Printf("Renewed lease, port: %v, ttl: %v", lease.Port, timeout)
		timer.Reset(time.Until(timeout) - bufferTime)
	}
}

// Adds slashes to the beginning and end of a given path so that the given path
// will match all subpaths in serving
func MakeFullPattern(path string) string {
	var b strings.Builder
	b.Grow(len(path) + 2)
	if path[0] != '/' {
		b.WriteRune('/')
	}
	b.WriteString(path)
	if path[len(path)-1] != '/' {
		b.WriteRune('/')
	}
	return b.String()
}
