package proxyserv

import (
    "net/rpc"
    "time"
    "log"
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

func MustConnectAndRegister(feproxyAddr, pattern string) (Client, Lease) {
    client, err := rpc.Dial("tcp", feproxyAddr)
    if err != nil {
        log.Fatalf("Failed to connect to frontend proxy RPC server: %v", err)
    }
    var lease Lease
    err = client.Call("feproxy.Register", pattern, &lease)
    if err != nil {
        log.Fatal("Failed to obtain lease from feproxy:", err)
    }
    log.Printf("Obtained lease for %#v, port: %v, ttl: %v",
        lease.Pattern, lease.Port, lease.TTL)
    return Client{client}, lease
}

func (c Client) Close() {
    c.client.Close()
}

func (c Client) Unregister(pattern string) error {
    return c.client.Call("feproxy.Unregister", pattern, nil)
}

// TODO: accept a channel to know when to quit
func (c Client) MustKeepLeaseRenewedForever(lease Lease) {
    renewDuration, err := time.ParseDuration(lease.TTL)
    if err != nil {
        log.Fatalf("Failed to parse TTL duration (TTL: %#v Err: %v)",
            lease.TTL, err)
    }
    renewDuration -= time.Hour // so we don't miss the deadline
    timer := time.NewTimer(renewDuration)
    for {
        <-timer.C
        err := c.client.Call("feproxy.Renew", lease.Pattern, &lease)
        if err != nil {
            if err == NotRegisteredError {
                log.Print("Got NotRegisteredError, attempting to register.")
                err = c.client.Call("feproxy.Register", lease.Pattern, &lease)
                if err != nil {
                    log.Fatalf("Error from register (%v)", err)
                }
            } else {
                log.Fatalf("Error from renew (%v)", err)
            }
        }
        log.Printf("Renewed lease, port: %v, ttl: %v", lease.Port, lease.TTL)
        // Technically we should check the new TTL, but it is constant
        timer.Reset(renewDuration)
    }
}
