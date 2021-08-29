// Package proxyserv provides an HTTPS frontend gateway (reverse proxy) server.
// At the start there are no forwarding rules and every URL returns 404.
// At runtime, call Register or Unregister to set up forwarding rules.
//
// Registration will grant the caller a Lease to the forwarding rule for a time
// period called the time to live (TTL). After the TTL has expired, the
// forwarding rule will be automatically unregistered.
package main

import (
    "crypto/tls"
    "fmt"
    "io/ioutil"
    "log"
    "math/rand"
    "net"
    "net/http"
    "net/http/httputil"
    "net/url"
    "os"
    "strconv"
    "strings"
    "sync"
    "time"

    "ask.systems/daemon/feproxy/client"
)

// How often to look through the Leases and unregister those past TTL
const ttlCheckFreq = "15m"

// ProxyServ implements http.Handler to handle requests using a pool of
// forwarding rules registered at runtime
type ProxyServ struct {
    mut              *sync.RWMutex
    // Map from pattern to forwarder. Protected by mut.
    forwarders       map[string]*forwarder
    // List of ports to be leased out, in a random order. Protected by mut.
    // Always has values between 0 and n, see unusedPortOffset.
    unusedPorts      []int
    // Add this to the values in unusedPorts to get the stored port number
    unusedPortOffset uint16
    ttlString        string
    ttlDuration      time.Duration
    startPort        uint16
    endPort          uint16
}

// forwarder holds the data for a forwarding rule registered with ProxyServ
type forwarder struct {
    Handler http.Handler
    Timeout time.Time
    Pattern string
    Port    uint16
}

// GetNumLeases returns the number of Registered Leases
func (p *ProxyServ) GetNumLeases() int {
    p.mut.RLock()
    defer p.mut.RUnlock()
    ret := len(p.forwarders)
    return ret
}

// Unregister deletes the forwarder and associated lease with the given pattern.
// Returns an error if the pattern is not registered
func (p *ProxyServ) Unregister(pattern string) error {
    p.mut.Lock()
    defer p.mut.Unlock()
    fwd, ok := p.forwarders[pattern]
    if !ok {
        return client.NotRegisteredError
    }
    p.unusedPorts = append(p.unusedPorts, int(fwd.Port))
    delete(p.forwarders, pattern)
    return nil
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

// Creates and saves a new forwarder that handles request and forwards them to
// the given client.
//
// if stripPattern is true, the pattern will be removed from the prefix of the
// http request paths. This is needed for third party applications that expect
// to get requests for / not /pattern/
//
// mut must be locked
func (p *ProxyServ) saveForwarder(clientAddr string, clientPort uint16, pattern string, stripPattern bool) error {
    backend, err := url.Parse("http://" + clientAddr + ":" + strconv.Itoa(int(clientPort)))
    if err != nil {
        return err
    }
    // Store the forwarder
    backendQuery := backend.RawQuery
    proxy := &httputil.ReverseProxy{
        Director: func (req *http.Request) {
            // Copied from https://golang.org/src/net/http/httputil/reverseproxy.go?s=2588:2649#L80
            req.URL.Scheme = backend.Scheme
            req.URL.Host = backend.Host
            // Can't use path.Join(., .) because it calls path.Clean which
            // causes a redirect loop if the pattern has a trailing / because
            // this will remove it and the DefaultServMux will redirect no
            // trailing slash to trailing slash.
            if req.URL.Path[0] != '/' {
                req.URL.Path = "/" + req.URL.Path
            }
            if stripPattern {
                if pattern[len(pattern)-1] != '/' { // if the pattern doesn't end in / then it's exact match only
                    req.URL.Path = "/"
                } else {
                    req.URL.Path = strings.TrimPrefix(req.URL.Path, pattern[0:len(pattern)-1])
                    if req.URL.Path == "" {
                        req.URL.Path = "/"
                    }
                }
            }
            req.URL.Path = backend.Path + req.URL.Path
            if backendQuery == "" || req.URL.RawQuery == "" {
                req.URL.RawQuery = backendQuery + req.URL.RawQuery
            } else {
                req.URL.RawQuery = backendQuery + "&" + req.URL.RawQuery
            }
            if _, ok := req.Header["User-Agent"]; !ok {
                // explicitly disable User-Agent so it's not set to default value
                req.Header.Set("User-Agent", "")
            }
            // My addition
            req.Header.Add("Orig-Address", req.RemoteAddr)
        },
    }
    fwd := &forwarder{
        Handler: proxy,
        Pattern: pattern,
        Port:    clientPort,
        Timeout: time.Now().Add(p.ttlDuration),
    }
    p.forwarders[pattern] = fwd
    return nil
}

func (p *ProxyServ) RegisterThirdParty(clientAddr string, clientPort uint16, pattern string) (lease client.Lease, err error) {
    // Assumes the port is not in the configured range (because otherwise we
    // might hand out a lease for that port). It's checked in the flag Set method.
    p.mut.Lock()
    defer p.mut.Unlock()

    if (clientPort >= p.startPort && clientPort <= p.endPort) {
        return client.Lease{}, fmt.Errorf("Fixed port %v must not be in the reserved feproxy client range: [%v, %v]",
            clientPort, p.startPort, p.endPort)
    }

    err = p.saveForwarder(clientAddr, clientPort, pattern, /*stripPattern*/ true)
    if err != nil {
        return client.Lease{}, err
    }

    return client.Lease{
        Pattern: pattern,
        Port:    clientPort,
        TTL:     p.ttlString,
    }, nil
}

// Register leases a new forwarder for the given pattern.
// Returns an error if the server has no more ports to lease.
func (p *ProxyServ) Register(clientAddr string, pattern string) (lease client.Lease, err error) {
    p.mut.Lock()
    defer p.mut.Unlock()

    port, err := p.reservePortUnsafe()
    if err != nil {
        return client.Lease{}, err
    }

    err = p.saveForwarder(clientAddr, port, pattern, /*stripPattern*/ false)
    if err != nil {
        return client.Lease{}, err
    }

    return client.Lease{
        Pattern: pattern,
        Port:    port,
        TTL:     p.ttlString,
    }, nil
}

// Renew renews an existing lease. Returns an error if the pattern is not
// registered.
func (p *ProxyServ) Renew(pattern string) (lease client.Lease, err error) {
    p.mut.Lock()
    defer p.mut.Unlock()
    fwd, ok := p.forwarders[pattern]
    if !ok {
        return client.Lease{}, client.NotRegisteredError
    }
    fwd.Timeout = time.Now().Add(p.ttlDuration)
    return client.Lease{
        Port: fwd.Port,
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
            ret = fwd
        }
    }
    return ret
}

// ServeHTTP is the ProxyServ net/http handler func which selects a registered
// forwarder to handle the request based on the forwarder's pattern
func (p *ProxyServ) ServeHTTP(w http.ResponseWriter, req *http.Request) {
    fwd := p.selectForwarder(req.URL.Path)
    // No handler for this path, 404
    if fwd == nil {
        log.Print("Forwarder not found for path: ", req.URL.Path)
        http.NotFound(w, req)
        return
    }
    // Handler timed out, delete it then 404
    if time.Now().After(fwd.Timeout) {
        log.Print("Forwarder timed out for pattern: ", fwd.Pattern)
        p.Unregister(req.URL.Path)
        http.NotFound(w, req)
        return
    }

    // If the pattern ends in /, redirect so the url ends in / so relative paths
    // in the html work right
    if fwd.Pattern[len(fwd.Pattern)-1] == '/' && req.URL.Path == fwd.Pattern[:len(fwd.Pattern)-1] {
        log.Print("Redirecting...")
        req.URL.Path += "/"
        http.Redirect(w, req, req.URL.String(), http.StatusSeeOther)
        return
    }

    // handle the request with the selected forwarder
    fwd.Handler.ServeHTTP(w, req)
}

// RedirectToHTTPS is an http Handler function which redirects any requests to
// the same url but with https instead of http
func RedirectToHTTPS(w http.ResponseWriter, req *http.Request) {
    var url url.URL = *req.URL // make a copy
    url.Scheme = "https"
    url.Host = req.Host
    url.Host = url.Hostname() // strip the port if one exists
    http.Redirect(w, req, url.String(), http.StatusSeeOther)
}

// loadTLSConfig loads the data from the tls files then closes them
func loadTLSConfig(tlsCert, tlsKey *os.File) (*tls.Config, error) {
    defer tlsCert.Close()
    defer tlsKey.Close()
    certBytes, err := ioutil.ReadAll(tlsCert)
    if err != nil {
        return nil, fmt.Errorf("failed to read tls cert file: %v", err)
    }
    keyBytes, err := ioutil.ReadAll(tlsKey)
    if err != nil {
        return nil, fmt.Errorf("failed to read tls key file: %v", err)
    }
    cert, err := tls.X509KeyPair(certBytes, keyBytes)
    if err != nil {
        return nil, fmt.Errorf("invalid tls file format: %v", err)
    }
    ret := &tls.Config{
        Certificates: make([]tls.Certificate, 1),
    }
    ret.Certificates[0] = cert
    return ret, nil
}

// runServer calls serv.Serv(list) and prints and error and closes the quit
// channel if the server dies
func runServer(quit chan struct{}, name string,
    serv *http.Server, list net.Listener) {

    err := serv.Serve(list)
    if err != http.ErrServerClosed {
        log.Printf("Proxy %v server error: %v", name, err)
        log.Printf("Proxy %v server died, shutting down", name)
        close(quit)
    }
}

// StartNew creates a new ProxyServ and starts the server.
//
// Arguments:
//  - tlsCert and tlsKey are file handles for the TLS certificate and key files
//  - httpList and httpsList are listeners for the http and https ports
//  - startPort and endPort set the range of ports this server offer for lease
//  - leaseTTL is the duration of the life of a lease
//  - quit is a channel that will be closed when the server should quit
func StartHTTPProxy(tlsCert, tlsKey *os.File, httpList, httpsList net.Listener,
    startPort, endPort uint16,
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
    tlsConfig, err := loadTLSConfig(tlsCert, tlsKey)
    if err != nil {
        return nil, fmt.Errorf("failed to load TLS config: %v", err)
    }
    // Start the TLS server
    p := &ProxyServ{
        mut:              &sync.RWMutex{},
        forwarders:       make(map[string]*forwarder),
        unusedPorts:      r.Perm(int(endPort - startPort)),
        unusedPortOffset: startPort,
        ttlString:        leaseTTL,
        ttlDuration:      ttlDuration,
        startPort:        startPort,
        endPort:          endPort,
    }
    tlsServer := &http.Server{
        Handler: p,
        TLSConfig: tlsConfig.Clone(),
    }
    // also start the TTL monitoring thread
    go p.monitorTTLs(time.NewTicker(ttlDuration), quit)
    go runServer(quit, "TLS", tlsServer, tls.NewListener(httpsList, tlsConfig))
    // Start the HTTP server to redirect to HTTPS
    httpServer := &http.Server{
        Handler: http.HandlerFunc(RedirectToHTTPS),
    }
    go runServer(quit, "HTTP redirect", httpServer, httpList)
    // Close the servers on quit signal
    go func () {
        <-quit
        httpServer.Close()
        tlsServer.Close()
        fmt.Print("Got quit signal, killed servers")
    }()
    return p, nil
}
