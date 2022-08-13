package portal

import (
    "errors"
    "time"
    "log"
    "fmt"
    "context"
    "strings"

    "google.golang.org/grpc"
)

var (
    NotRegisteredError = errors.New("pattern not registered")
)

type Client struct {
    RPC PortalClient // Call any of the service.proto functions here
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

// Connect to the portal RPC server and don't do anything else. Use this if you
// want to call the proto RPCs directly.
func Connect(portalAddr string) (Client, error) {
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
func (c Client) KeepLeaseRenewed(quit <-chan struct{}, lease *Lease) {
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
        timeout := lease.Timeout.AsTime()
        log.Printf("Renewed lease, port: %v, ttl: %v", lease.Port, timeout)
        timer.Reset(time.Until(timeout) - bufferTime)
    }
}

// Deprecated: Use StartRegistration instead
type ThirdPartyArgs struct {
    Port uint16
    Pattern string
}

// Deprecated: Use StartRegistration instead
// Calls Register with {Pattern: pattern, FixedPort: thirdPartyPort, StripPattern: true}
func ConnectAndRegisterThirdParty(portalAddr string, thirdPartyPort uint16, pattern string) (Client, *Lease, error) {
    c, err := Connect(portalAddr)
    if err != nil {
        return c, nil, err
    }
    lease, err := c.RPC.Register(context.Background(), &RegisterRequest{
      Pattern: pattern,
      FixedPort: uint32(thirdPartyPort),
      StripPattern: true,
    })
    if err != nil {
        return c, nil, fmt.Errorf(
            "Failed to obtain lease from portal: %v", err)
    }
    log.Printf("Obtained lease for %#v, port: %v, timeout: %v",
        lease.Pattern, lease.Port, lease.Timeout.AsTime())
    return c, lease, nil
}

// Deprecated: Use StartRegistration instead
func ConnectAndRegister(portalAddr, pattern string) (Client, *Lease, error) {
    c, err := Connect(portalAddr)
    if err != nil {
        return c, nil, err
    }
    lease, err := c.RPC.Register(context.Background(), &RegisterRequest{Pattern: pattern})
    if err != nil {
        return c, nil, fmt.Errorf(
            "Failed to obtain lease from portal: %v", err)
    }
    log.Printf("Obtained lease for %#v, port: %v, ttl: %v",
        lease.Pattern, lease.Port, lease.Timeout.AsTime())
    return c, lease, nil
}

// Deprecated: Use StartRegistration instead
func MustConnectAndRegister(portalAddr, pattern string) (Client, *Lease) {
    c, l, err := ConnectAndRegister(portalAddr, pattern)
    if err != nil {
        log.Fatal(err)
    }
    return c, l
}

// Deprecated: Use StartRegistration instead.
// See the ConnectAndRegisterThirdParty comment for the arguments to use.
func MustConnectAndRegisterThirdParty(portalAddr string, thirdPartyPort uint16, pattern string) (Client, *Lease) {
    c, l, err := ConnectAndRegisterThirdParty(portalAddr, thirdPartyPort, pattern)
    if err != nil {
        log.Fatal(err)
    }
    return c, l
}

// Deprecated: Use c.RPC.Unregister instead
func (c Client) Unregister(pattern string) error {
    _, err := c.RPC.Unregister(context.Background(), &Lease{Pattern:pattern})
    return err
}

// Adds slashes to the beginning and end of a given path so that the given path
// will match all subpaths in serving
func MakeFullPattern(path string) string {
    var b strings.Builder
    b.Grow(len(path)+2)
    if (path[0] != '/') {
        b.WriteRune('/')
    }
    b.WriteString(path)
    if (path[len(path)-1] != '/') {
        b.WriteRune('/')
    }
    return b.String()
}
