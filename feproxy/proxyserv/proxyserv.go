// Package proxyserv provides an HTTPS frontend gateway (reverse proxy) server.
// At the start there are no forwarding rules and every URL returns 404.
// At runtime, call Register or Unregister to set up forwarding rules.
//
// Registration will grant the caller a Lease to the forwarding rule for a time
// period called the time to live (TTL). After the TTL has expired, the
// forwarding rule will be automatically unregistered.
package proxyserv

import (
    "fmt"
    "log"
    "math/rand"
    "net/http"
    "net/http/httputil"
    "net/url"
    "strconv"
    "sync"
    "time"
)

// How often to look through the Leases and unregister those past TTL
const ttlCheckFreq = "15m"

// ProxyServ implements http.Handler to handle requests using a pool of
// forwarding rules registered at runtime
type ProxyServ struct {
    mut              *sync.RWMutex
    // Map from pattern to forwarder. Protected by mut.
    forwarders       map[string]forwarder
    // List of ports to be leased out, in a random order. Protected by mut.
    // Always has values between 0 and n, see unusedPortOffset.
    unusedPorts      []int
    // Add this to the values in unusedPorts to get the stored port number
    unusedPortOffset uint16
    ttlString        string
    ttlDuration      time.Duration
}

// forwarder holds the data for a forwarding rule registered with ProxyServ
type forwarder struct {
    Handler http.Handler
    Timeout time.Time
    Pattern string
    Port    uint16
}

// Lease contains the terms of the lease granted by ProxyServ
type Lease struct {
    Port uint16
    TTL  string
}

// GetNumLeases returns the number of Registered Leases
func (p *ProxyServ) GetNumLeases() int {
    p.mut.RLock()
    defer p.mut.RUnlock()
    ret := len(p.forwarders)
    return ret
}

// Unregister deletes the forwarder and associated lease with the given pattern
func (p *ProxyServ) Unregister(pattern string) {
    p.mut.Lock()
    defer p.mut.Unlock()
    if fwd, ok := p.forwarders[pattern]; ok {
        p.unusedPorts = append(p.unusedPorts, int(fwd.Port))
        delete(p.forwarders, pattern)
    }
}

// reservePort retuns a random unused port and marks it as used.
// Returns an error if the server has no more ports to lease.
//
// You must have a (write) lock on mut before calling.
func (p *ProxyServ) reservePortUnsafe() (uint16, error) {
    if len(p.unusedPorts) == 0 {
        return 0, fmt.Errorf("No remaining ports to lease. Active leases: %v",
                             len(p.forwarders))
    }
    port := uint16(p.unusedPorts[0]) + p.unusedPortOffset
    p.unusedPorts = p.unusedPorts[1:]
    return port, nil
}

// Register leases a new forwarder for the given pattern.
// Returns an error if the server has no more ports to lease.
func (p *ProxyServ) Register(pattern string) (lease Lease, err error) {
    p.mut.Lock()
    defer p.mut.Unlock()
    port, err := p.reservePortUnsafe()
    if err != nil {
        return Lease{}, err
    }
    backend, err := url.Parse("http://127.0.0.1:" + strconv.Itoa(int(port)))
    if err != nil {
        log.Fatal(err)
        return Lease{}, err
    }
    // Store the forwarder
    p.forwarders[pattern] = forwarder{
        Handler: httputil.NewSingleHostReverseProxy(backend),
        Timeout: time.Now().Add(p.ttlDuration),
        Pattern: pattern,
        Port:    port,
    }
    return Lease{
        Port: port,
        TTL:  p.ttlString,
    }, nil
}

// monitorTTLs monitors the list of leases and removes the expired ones.
// Checks the lease once per each ttlCheckFreq duration.
func (p *ProxyServ) monitorTTLs(ticker *time.Ticker, quit <-chan struct{}) {
    for {
        select {
        case <-ticker.C: // on next tick
            p.mut.Lock()
            now := time.Now()
            for pattern, fwd := range p.forwarders {
                if now.After(fwd.Timeout) {
                    p.unusedPorts = append(p.unusedPorts, int(fwd.Port))
                    delete(p.forwarders, pattern)
                }
            }
            p.mut.Unlock()
        case <-quit: // on quit
            ticker.Stop()
            return
        }
    }
}

// urlMatchesPattern returns whether or not the url matches the pattern string.
//
// Similar to http.ServeMux.pathMatch, see https://golang.org/LICENSE
func urlMatchesPattern(url, pattern string) bool{
    if len(pattern) == 0 {
        return false
    }
    n := len(pattern) - 1
    if pattern[n] != '/' {
        return pattern == url
    }
    return len(url) >= n && url[0:n] == pattern[0:n]
}

// selectForwarder finds the appropriate forwarder for the given url.
//
// To be selected, the url must match the forwarder's pattern. If the url
// matches multiple patterns, then the forwarder with the longest pattern is
// selected.
//
// returns nil if no matching forwarder is found
//
// Similar to http.ServeMux.match, see https://golang.org/LICENSE
func (p *ProxyServ) selectForwarder(url string) *forwarder {
    p.mut.RLock()
    defer p.mut.RUnlock()
    var ret *forwarder = nil
    var maxPatternLen = 0
    for pattern, fwd := range p.forwarders {
        if !urlMatchesPattern(url, pattern) {
            continue
        }
        if ret == nil || len(pattern) > maxPatternLen {
            maxPatternLen = len(pattern)
            ret = &fwd
        }
    }
    return ret
}


// ServeHTTP is the ProxyServ net/http handler func which selects a registered
// forwarder to handle the request based on the forwarder's pattern
func (p *ProxyServ) ServeHTTP(w http.ResponseWriter, req *http.Request) {
    fwd := p.selectForwarder(req.URL.Path)
    // No handler for this path, 404
    if fwd != nil {
        http.NotFound(w, req)
        return
    }
    // Handler timed out, delete it then 404
    if time.Now().After(fwd.Timeout) {
        p.Unregister(req.URL.Path)
        http.NotFound(w, req)
        return
    }
    // handle the request with the selected forwarder
    fwd.Handler.ServeHTTP(w, req)
}

// StartNew creates a new ProxyServ and starts the server.
//
// Arguments:
//  - tlsCert and tlsKey are the path to the TLS certificate and key files
//  - startPort and endPort set the range of ports this server offer for lease
//  - leaseTTL is the duration of the life of a lease
//  - quit is a channel that will be closed when the server should quit
func StartNew(tlsCert, tlsKey string, startPort, endPort uint16,
              leaseTTL string, quit chan struct{}) (*ProxyServ, error) {
    if !(startPort < endPort) {
        return nil, fmt.Errorf("startPort (%v) must be less than endPort (%v)",
            startPort, endPort)
    }
    r := rand.New(rand.NewSource(time.Now().UnixNano()))
    ttlDuration, err := time.ParseDuration(leaseTTL)
    if err != nil {
        return nil, fmt.Errorf("bad leaseTTL string format: %v", leaseTTL)
    }
    p := &ProxyServ{
        mut:              &sync.RWMutex{},
        forwarders:       make(map[string]forwarder),
        unusedPorts:      r.Perm(int(endPort - startPort)),
        unusedPortOffset: startPort,
        ttlString:        leaseTTL,
        ttlDuration:      ttlDuration,
    }
    server := &http.Server{
        Handler: p,
    }
    // Start the TTL monitoring thread
    go p.monitorTTLs(time.NewTicker(ttlDuration), quit)
    // Start the server
    go func () {
        err := server.ListenAndServeTLS(tlsCert, tlsKey)
        if err != nil { // TODO(1.8): err != ErrServerClosed
            log.Print("Proxy server error: ", err)
            log.Print("Proxy server died, shutting down")
        }
        close(quit)
    }()
    // Close the server on quit signal
    go func () {
        <-quit
        // TODO(1.8): server.Close()
        log.Fatal("Killing process. Go 1.3 doesn't support http.Server.Close()")
    }()
    return p, nil
}
