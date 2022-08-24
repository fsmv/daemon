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
    "log"
    "net"
    "net/http"
    "net/http/httputil"
    "net/url"
    "strconv"
    "strings"
    "sync"
    "time"

    "ask.systems/daemon/portal"
)

// HTTPProxy implements http.Handler to handle requests using a pool of
// forwarding rules registered at runtime
type HTTPProxy struct {
    leasor *PortLeasor
    // Map from pattern to *forwarder, which must not be modified
    forwarders sync.Map
}

// forwarder holds the data for a forwarding rule registered with HTTPProxy
type forwarder struct {
    Handler http.Handler
    Timeout time.Time
    Pattern string
    Port    uint32
}

// Register leases a new forwarder for the given pattern.
// Returns an error if the server has no more ports to lease.
func (p *HTTPProxy) Register(clientAddr string, request *portal.RegisterRequest) (*portal.Lease, error) {
    if oldFwd := p.selectForwarder(request.Pattern); oldFwd != nil {
        if oldFwd.Pattern != request.Pattern {
            err := fmt.Errorf("Another pattern %#v already covers your requested pattern %#v", oldFwd.Pattern, request.Pattern)
            log.Print("Error registering: ", err)
            return nil, err
        }
        log.Printf("Replacing existing lease with the same pattern: %#v", request.Pattern)
        p.leasor.UnregisterPort(oldFwd.Port) // ignore not registered error
    }
    lease, err := p.leasor.Register(&portal.Lease{
        Pattern: request.Pattern,
        Port: request.FixedPort,
    }, func() { p.forwarders.Delete(request.Pattern) })
    if err != nil {
        log.Print("Error registering: ", err)
        return nil, err
    }

    err = p.saveForwarder(clientAddr, lease, request.StripPattern)
    if err != nil {
        return nil, err
    }
    log.Printf("Registered forwarder to %v:%v, Pattern: %v, Timeout: %v",
        clientAddr, lease.Port, request.Pattern, lease.Timeout.AsTime())
    return lease, nil
}

// Creates and saves a new forwarder that handles request and forwards them to
// the given client.
//
// if stripPattern is true, the pattern will be removed from the prefix of the
// http request paths. This is needed for third party applications that expect
// to get requests for / not /pattern/
func (p *HTTPProxy) saveForwarder(clientAddr string, lease *portal.Lease, stripPattern bool) error {
    backend, err := url.Parse("http://" + clientAddr + ":" + strconv.Itoa(int(lease.Port)))
    if err != nil {
        return err
    }
    // Store the forwarder
    backendQuery := backend.RawQuery
    pattern := lease.Pattern
    proxy := &httputil.ReverseProxy{
        Director: func (req *http.Request) {
            // TODO: does this help anything???
            //req.Header.Add("X-Forwarded-Host", req.Host)
            //req.Header.Add("X-Origin-Host", backend.Host)

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
        Port:    lease.Port,
        Timeout: lease.Timeout.AsTime(),
    }
    p.forwarders.Store(pattern, fwd)
    return nil
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
func (p *HTTPProxy) selectForwarder(url string) *forwarder {
    var ret *forwarder = nil
    var maxPatternLen = 0
    p.forwarders.Range(func (key, value interface{}) bool {
        pattern := key.(string)
        if !urlMatchesPattern(url, pattern) {
            return true
        }
        if ret == nil || len(pattern) > maxPatternLen {
            maxPatternLen = len(pattern)
            ret = value.(*forwarder)
        }
        return true
    })
    return ret
}

// ServeHTTP is the HTTPProxy net/http handler func which selects a registered
// forwarder to handle the request based on the forwarder's pattern
func (p *HTTPProxy) ServeHTTP(w http.ResponseWriter, req *http.Request) {
    fwd := p.selectForwarder(req.URL.Path)
    // No handler for this path, 404
    if fwd == nil {
        log.Print("%v requested unregistered path: ", req.RemoteAddr, req.URL.Path)
        http.NotFound(w, req)
        return
    }

    // If the pattern ends in /, redirect so the url ends in / so relative paths
    // in the html work right
    if fwd.Pattern[len(fwd.Pattern)-1] == '/' && req.URL.Path == fwd.Pattern[:len(fwd.Pattern)-1] {
        log.Print("Redirecting to clean path...")
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

// runServer calls serv.Serv(list) and prints and error and closes the quit
// channel if the server dies
func runServer(quit chan struct{}, name string,
    serv *http.Server, list net.Listener) {

    // TODO: probably should call ServeTLS for tls instead of doing our own
    // I think this doesn't support HTTP/2 currently
    err := serv.Serve(list)
    if err != http.ErrServerClosed {
        log.Printf("Proxy %v server error: %v", name, err)
        log.Printf("Proxy %v server died, shutting down", name)
        close(quit)
    }
}

// StartNew creates a new HTTPProxy and starts the server.
//
// Arguments:
//  - tlsCert and tlsKey are file handles for the TLS certificate and key files
//  - httpList and httpsList are listeners for the http and https ports
//  - startPort and endPort set the range of ports this server offer for lease
//  - leaseTTL is the duration of the life of a lease
//  - quit is a channel that will be closed when the server should quit
func StartHTTPProxy(l *PortLeasor, tlsConfig *tls.Config,
    httpList, httpsList net.Listener, quit chan struct{}) (*HTTPProxy, error) {
    // Start the TLS server
    p := &HTTPProxy{
        leasor: l,
    }
    tlsServer := &http.Server{
        Handler: p,
        TLSConfig: tlsConfig.Clone(),
    }
    // also start the TTL monitoring thread
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
