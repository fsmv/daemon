package proxyserv

import (
    "net/rpc"
    "time"
    "log"
    "fmt"
)

// Lease contains the terms of the lease granted by ProxyServ
type Lease struct {
    Pattern string
    Port    uint16
    TTL     string
}

type Client struct {
    client *rpc.Client
}

func ConnectAndRegister(feproxyAddr, pattern string) (Client, Lease, error) {
    client, err := rpc.Dial("tcp", feproxyAddr)
    if err != nil {
        return Client{}, Lease{}, fmt.Errorf(
            "Failed to connect to frontend proxy RPC server: %v", err)
    }
    var lease Lease
    err = client.Call("feproxy.Register", pattern, &lease)
    if err != nil {
        return Client{}, Lease{}, fmt.Errorf(
            "Failed to obtain lease from feproxy: %v", err)
    }
    log.Printf("Obtained lease for %#v, port: %v, ttl: %v",
        lease.Pattern, lease.Port, lease.TTL)
    return Client{client}, lease, nil
}

func MustConnectAndRegister(feproxyAddr, pattern string) (Client, Lease) {
    c, l, err := ConnectAndRegister(feproxyAddr, pattern)
    if err != nil {
        log.Fatal(err)
    }
    return c, l
}

func (c Client) Close() {
    c.client.Close()
}

func (c Client) Unregister(pattern string) error {
    return c.client.Call("feproxy.Unregister", pattern, nil)
}

func (c Client) KeepLeaseRenewed(quit <-chan struct{}, lease Lease) {
    defer func() {
        c.Unregister(lease.Pattern)
        c.Close()
        log.Printf("feproxy lease %#v unregistered and connection closed",
            lease.Pattern)
    }()
    renewDuration, err := time.ParseDuration(lease.TTL)
    if err != nil {
        log.Fatalf("Failed to parse TTL duration (TTL: %#v Err: %v)",
            lease.TTL, err)
    }
    renewDuration -= time.Hour // so we don't miss the deadline
    timer := time.NewTimer(renewDuration)
    for {
        select {
        case <-quit:
            return
        case <-timer.C:
        }
        err := c.client.Call("feproxy.Renew", lease.Pattern, &lease)
        if err != nil {
            if err == NotRegisteredError {
                log.Print("Got NotRegisteredError, attempting to register.")
                err = c.client.Call("feproxy.Register", lease.Pattern, &lease)
                if err != nil {
                    log.Printf("Error from register: %v", err)
                }
            } else {
                log.Printf("Error from renew: %v", err)
            }
        }
        log.Printf("Renewed lease, port: %v, ttl: %v", lease.Port, lease.TTL)
        // Technically we should check the new TTL, but it is constant
        timer.Reset(renewDuration)
    }
}
