// Package proxyserv provides an HTTPS frontend gateway (reverse proxy) server.
// At the start there are no forwarding rules and every URL returns 404.
// At runtime, call Register or Unregister to set up forwarding rules.
//
// Registration will grant the caller a Lease to the forwarding rule for a time
// period called the time to live (TTL). After the TTL has expired, the
// forwarding rule will be automatically unregistered.
package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"ask.systems/daemon/portal"
	"ask.systems/daemon/tools"
)

// The path for the let's encrypt web-root cert challenge
const certChallengePattern = "/.well-known/acme-challenge/"

// HTTPProxy implements http.Handler to handle requests using a pool of
// forwarding rules registered at runtime
type HTTPProxy struct {
	leasor *PortLeasor
	// Map from pattern to *forwarder, which must not be modified
	forwarders sync.Map
	rootCert   *tools.AutorenewCertificate
	state      *StateManager
}

// forwarder holds the data for a forwarding rule registered with HTTPProxy
type forwarder struct {
	Handler http.Handler
	Lease   *portal.Lease
}

func (p *HTTPProxy) Unregister(lease *portal.Lease) {
	p.forwarders.Delete(lease.Pattern)
}

// Register leases a new forwarder for the given pattern.
// Returns an error if the server has no more ports to lease.
func (p *HTTPProxy) Register(
	clientAddr string, request *portal.RegisterRequest) (*portal.Lease, error) {

	if oldFwd := p.selectForwarder(request.Pattern); oldFwd != nil {
		if oldFwd.Lease.Pattern == certChallengePattern {
			err := fmt.Errorf("Clients cannot register the cert challenge path %#v which covers your requested pattern %#v", certChallengePattern, request.Pattern)
			log.Print("Error registering: ", err)
			return nil, err
		}
		if oldFwd.Lease.Pattern != request.Pattern {
			err := fmt.Errorf("Another pattern %#v already covers your requested pattern %#v", oldFwd.Lease.Pattern, request.Pattern)
			log.Print("Error registering: ", err)
			return nil, err
		}
		log.Printf("Replacing existing lease with the same pattern: %#v", request.Pattern)
		p.leasor.Unregister(oldFwd.Lease) // ignore not registered error
	}
	lease, err := p.leasor.Register(request)
	if err != nil {
		log.Print("Error registering: ", err)
		return nil, err
	}

	useTLS := len(request.CertificateRequest) != 0
	err = p.saveForwarder(clientAddr, lease, request.StripPattern, useTLS)
	if err != nil {
		return nil, err
	}
	log.Printf("Registered forwarder to %v:%v, Pattern: %#v, Timeout: %v",
		clientAddr, lease.Port, lease.Pattern, lease.Timeout.AsTime())
	return lease, nil
}

// Creates and saves a new forwarder that handles request and forwards them to
// the given client.
//
// if stripPattern is true, the pattern will be removed from the prefix of the
// http request paths. This is needed for third party applications that expect
// to get requests for / not /pattern/
func (p *HTTPProxy) saveForwarder(clientAddr string, lease *portal.Lease,
	stripPattern bool, useTLS bool) error {

	var protocol string
	if useTLS {
		protocol = "https://"
	} else {
		protocol = "http://"
	}
	backend, err := url.Parse(protocol + clientAddr + ":" + strconv.Itoa(int(lease.Port)))
	if err != nil {
		return err
	}
	// Store the forwarder
	backendQuery := backend.RawQuery
	pattern := lease.Pattern

	// Accept certificates signed by the latest portal cert
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialTLSContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		roots := p.state.RootCAs()
		dialConf := &tls.Config{
			RootCAs:   roots,
			ClientCAs: roots,
			// Use the server cert for client auth
			GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
				return p.rootCert.Certificate(), nil
			},
		}

		// Note: We can use dialConf.VerifyPeerCertificate to only allow talking to
		// client that we registered with. This is pointless right now because we
		// allow any client with a valid token (same as the clients that have a
		// valid cert signed by us) to replace any lease. We assume validated
		// clients are not malicious right now.
		dialer := &tls.Dialer{Config: dialConf}
		return dialer.DialContext(ctx, network, addr)
	}

	proxy := &httputil.ReverseProxy{
		Transport: transport,
		Director: func(req *http.Request) {
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
		Lease:   lease,
	}
	p.forwarders.Store(pattern, fwd)
	return nil
}

// urlMatchesPattern returns whether or not the url matches the pattern string.
func urlMatchesPattern(url, pattern string) bool {
	if len(pattern) == 0 {
		return false
	}
	// Take the slash out of url if it has one
	if url[len(url)-1] == '/' {
		url = url[0 : len(url)-1]
	}
	if pattern[len(pattern)-1] != '/' {
		// If the pattern does not end in /, exact match only
		return pattern == url
	}
	// Since we passed above we know pattern ends in /
	if len(url) == len(pattern)-1 {
		// Exact match for patterns that end in /
		return pattern[0:len(pattern)-1] == url
	}
	if len(url) >= len(pattern) {
		// Match subdirectories of the pattern (url must contain the / at the end of pattern)
		return strings.HasPrefix(url, pattern)
	}
	return false
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
	p.forwarders.Range(func(key, value interface{}) bool {
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
	if fwd == nil {
		log.Printf("%v requested unregistered path: %v", req.RemoteAddr, req.URL.Path)
		http.NotFound(w, req)
		return
	}

	// If the pattern ends in /, redirect so the url ends in / so relative paths
	// in the html work right
	pattern := fwd.Lease.Pattern
	if pattern[len(pattern)-1] == '/' && req.URL.Path == pattern[:len(pattern)-1] {
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

	err := serv.Serve(list)
	if err != http.ErrServerClosed {
		log.Printf("Proxy %v server error: %v", name, err)
		log.Printf("Proxy %v server died, shutting down", name)
		close(quit)
	}
}

func makeChallengeHandler(webRoot string) (http.Handler, error) {
	if err := os.MkdirAll(webRoot, 0775); err != nil {
		return nil, err
	}
	dir := http.Dir(webRoot)
	// Make sure the webRoot dir is readable
	f, err := dir.Open("/")
	defer f.Close()
	if err != nil {
		return nil, err
	}
	if _, err := f.Stat(); err != nil {
		return nil, err
	}
	fileServer := http.StripPrefix(certChallengePattern, http.FileServer(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		log.Printf("%v requested %v", req.RemoteAddr, req.URL)
		if filepath.Base(req.URL.Path) == filepath.Base(certChallengePattern) {
			http.NotFound(w, req)
			return // Don't allow the directory listing page
		}
		fileServer.ServeHTTP(w, req)
	}), nil
}

// StartNew creates a new HTTPProxy and starts the server.
//
// Arguments:
//   - tlsCert and tlsKey are file handles for the TLS certificate and key files
//   - httpList and httpsList are listeners for the http and https ports
//   - certChallengeWebRoot if non-empty start the file server for tls cert challenges
//   - quit is a channel that will be closed when the server should quit
func StartHTTPProxy(l *PortLeasor, tlsConfig *tls.Config,
	httpList, httpsList net.Listener, certChallengeWebRoot string,
	state *StateManager, rootCert *tools.AutorenewCertificate,
	quit chan struct{}) (*HTTPProxy, error) {
	ret := &HTTPProxy{
		leasor:   l,
		rootCert: rootCert,
		state:    state,
	}
	l.OnTTL(ret.Unregister)

	// Set up serving cert challenges
	if certChallengeWebRoot != "" {
		handler, err := makeChallengeHandler(certChallengeWebRoot)
		if err != nil {
			log.Print("Failed to start cert challenge webroot: ", err)
		} else {
			ret.forwarders.Store(certChallengePattern, &forwarder{
				Handler: handler,
				Lease: &portal.Lease{
					Pattern: certChallengePattern,
				},
			})
			log.Print("Started serving cert challenge path.")
		}
	}

	// Start the TLS server
	tlsServer := &http.Server{
		Handler: ret,
	}
	// Support HTTP/2. See https://pkg.go.dev/net/http#Serve
	// > HTTP/2 support is only enabled if ... configured with "h2" in the TLS Config.NextProtos.
	tlsConfig.NextProtos = append(tlsConfig.NextProtos, "h2")
	go runServer(quit, "TLS", tlsServer, tls.NewListener(httpsList, tlsConfig))
	// Start the HTTP server to redirect to HTTPS
	httpServer := &http.Server{
		Handler: http.HandlerFunc(RedirectToHTTPS),
	}
	go runServer(quit, "HTTP redirect", httpServer, httpList)
	// Close the servers on quit signal
	go func() {
		<-quit
		httpServer.Close()
		tlsServer.Close()
		fmt.Print("Got quit signal, killed servers")
	}()
	return ret, nil
}
