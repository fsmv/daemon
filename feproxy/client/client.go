package client

import (
    "errors"
    "time"
    "log"
    "fmt"
    "context"

    "google.golang.org/grpc"
)

var (
    NotRegisteredError = errors.New("pattern not registered")
)

type Client struct {
    client *grpc.ClientConn
}

type ThirdPartyArgs struct {
    Port uint16
    Pattern string
}

func ConnectAndRegisterThirdParty(feproxyAddr string, thirdPartyPort uint16, pattern string) (Client, *Lease, error) {
    client, err := grpc.Dial(feproxyAddr)
    if err != nil {
        return Client{}, nil, fmt.Errorf(
            "Failed to connect to frontend proxy RPC server: %v", err)
    }
    var lease Lease
    err = client.Invoke(context.Background(), "Register", &RegisterRequest{
      Pattern: pattern,
      FixedPort: uint32(thirdPartyPort),
      StripPattern: true,
    }, &lease)
    if err != nil {
        return Client{}, nil, fmt.Errorf(
            "Failed to obtain lease from feproxy: %v", err)
    }
    log.Printf("Obtained lease for %#v, port: %v, timeout: %v",
        lease.Pattern, lease.Port, lease.Timeout.AsTime())
    return Client{client}, &lease, nil
}

func ConnectAndRegister(feproxyAddr, pattern string) (Client, *Lease, error) {
    client, err := grpc.Dial(feproxyAddr)
    if err != nil {
        return Client{}, nil, fmt.Errorf(
            "Failed to connect to frontend proxy RPC server: %v", err)
    }
    var lease Lease
    err = client.Invoke(context.Background(), "Register", &RegisterRequest{Pattern: pattern}, &lease)
    if err != nil {
        return Client{}, nil, fmt.Errorf(
            "Failed to obtain lease from feproxy: %v", err)
    }
    log.Printf("Obtained lease for %#v, port: %v, ttl: %v",
        lease.Pattern, lease.Port, lease.Timeout.AsTime())
    return Client{client}, &lease, nil
}

func MustConnectAndRegister(feproxyAddr, pattern string) (Client, *Lease) {
    c, l, err := ConnectAndRegister(feproxyAddr, pattern)
    if err != nil {
        log.Fatal(err)
    }
    return c, l
}

func MustConnectAndRegisterThirdParty(feproxyAddr string, thirdPartyPort uint16, pattern string) (Client, *Lease) {
    c, l, err := ConnectAndRegisterThirdParty(feproxyAddr, thirdPartyPort, pattern)
    if err != nil {
        log.Fatal(err)
    }
    return c, l
}

func (c Client) Close() {
    c.client.Close()
}

func (c Client) Unregister(pattern string) error {
    return c.client.Invoke(context.Background(), "Unregister", &Lease{Pattern:pattern}, nil)
}

func (c Client) KeepLeaseRenewed(quit <-chan struct{}, lease *Lease) {
    defer func() {
        c.Unregister(lease.Pattern)
        c.Close()
        log.Printf("feproxy lease %#v unregistered and connection closed",
            lease.Pattern)
    }()
    renewDuration := time.Until(lease.Timeout.AsTime())
    renewDuration -= time.Hour // so we don't miss the deadline
    timer := time.NewTimer(renewDuration)
    for {
        select {
        case <-quit:
            return
        case <-timer.C:
        }
        err := c.client.Invoke(context.Background(), "Renew", lease, lease)
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
        log.Printf("Renewed lease, port: %v, ttl: %v", lease.Port, lease.Timeout.AsTime())
        // Technically we should check the new TTL, but it is constant
        timer.Reset(renewDuration)
    }
}
