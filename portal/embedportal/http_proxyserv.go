package embedportal

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"ask.systems/daemon/portal/gate"
	"ask.systems/daemon/tools"
)

// The path for the let's encrypt web-root cert challenge
const certChallengePattern = "*/.well-known/acme-challenge/"

// httpProxy implements http.Handler to handle requests using a pool of
// forwarding rules registered at runtime
//
// It provides an HTTPS frontend gateway (reverse proxy) server.
// At the start there are no forwarding rules and every URL returns 404.
// At runtime, call [httpProxy.Register] or [httpProxy.Unregister] to set up
// forwarding rules.
//
// Registration will grant the caller a Lease to the forwarding rule for a time
// period called the time to live (TTL). After the TTL has expired, the
// forwarding rule will be automatically unregistered.
type httpProxy struct {
	clientLeasor *clientLeasor
	// Map from pattern to *forwarder, which must not be modified
	forwarders  sync.Map
	allowsHTTP  atomic.Bool
	rootCert    *tls.Config
	state       *stateManager
	defaultHost string
	challenges  *acmeChallenges

	httpList  net.Listener
	httpsList net.Listener
}

// forwarder holds the data for a forwarding rule registered with httpProxy
type forwarder struct {
	Handler   http.Handler
	Lease     *gate.Lease
	AdminOnly bool
	AllowHTTP bool
}

func (p *httpProxy) Unregister(lease *gate.Lease) {
	p.forwarders.Delete(lease.Pattern)
}

// Register leases a new forwarder for the given pattern.
// Returns an error if the server has no more ports to lease.
func (p *httpProxy) Register(
	clientAddr string, request *gate.RegisterRequest, fixedTimeout time.Time) (*gate.Lease, error) {

	if request.Pattern == "" {
		return nil, fmt.Errorf("Registration pattern must not be empty.")
	}

	leasor := p.clientLeasor.PortLeasorForClient(clientAddr)
	if oldFwd := p.selectForwarder(gate.ParsePattern(request.Pattern)); oldFwd != nil {
		if oldFwd.Lease.Pattern == certChallengePattern {
			return nil, fmt.Errorf("Clients cannot register the cert challenge path %#v which covers your requested pattern %#v", certChallengePattern, request.Pattern)
		}
		if oldFwd.Lease.Pattern == request.Pattern {
			log.Printf("Replacing existing lease with the same pattern: %#v", request.Pattern)
			leasor.Unregister(oldFwd.Lease) // this calls also httpProxy.Unregister via callback
		}
	}
	lease, err := leasor.Register(request, fixedTimeout)
	if err != nil {
		return nil, err
	}

	err = p.saveForwarder(clientAddr, lease, request)
	if err != nil {
		leasor.Unregister(lease)
		return nil, err
	}
	log.Printf("Registered forwarder to %v:%v, Pattern: %#v, Timeout: %v",
		clientAddr, lease.Port, lease.Pattern, lease.Timeout.AsTime().In(time.Local))
	return lease, nil
}

// Creates and saves a new forwarder that handles request and forwards them to
// the given client.
//
// if stripPattern is true, the pattern will be removed from the prefix of the
// http request paths. This is needed for third party applications that expect
// to get requests for / not /pattern/
func (p *httpProxy) saveForwarder(clientAddr string, lease *gate.Lease,
	request *gate.RegisterRequest) error {

	var host string
	if request.Hostname != "" {
		host = request.Hostname
	} else {
		host = clientAddr
	}
	hostPort := fmt.Sprintf("%v:%v", host, lease.Port)

	// TODO: when assimilate supports writing cert files, maybe make it so
	// normally we only accept the portal root CA (and maybe add the system root
	// CAs as well) and only skip verify if the hostname field was set.
	//
	// Then if people can run assimilate on the same machine as their server
	// they'll have to use the protal signed cert so that we can actually verify
	// it instead of turning that off.
	var conf *tls.Config
	if len(request.CertificateRequest) == 0 {
		conf = &tls.Config{
			InsecureSkipVerify: true,
		}
	} else {
		roots := p.state.RootCAs()
		// Note: We can use conf.VerifyPeerCertificate to only allow talking to
		// client that we registered with. This is pointless right now because we
		// allow any client with a valid token (same as the clients that have a
		// valid cert signed by us) to replace any lease. We assume validated
		// clients are not malicious right now.
		conf = &tls.Config{
			RootCAs: roots,
			// Use the server cert for client auth
			GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
				return p.rootCert.GetCertificate(nil)
			},
		}
	}

	// Detect TLS support for FixedPort backends, if we don't have a FixedPort set
	// then the server cannot be already running and won't run until we return
	// this RPC.
	protocol := "http://"
	if len(request.CertificateRequest) == 0 && request.FixedPort != 0 {
		d := tls.Dialer{
			NetDialer: &net.Dialer{Timeout: 6 * time.Second},
			Config:    conf,
		}
		conn, err := d.Dial("tcp", hostPort)
		if err == nil {
			conn.Close()
			protocol = "https://"
		} else {
			log.Printf("Warning: TLS is not supported for the %v backend. This internal traffic will not be encrypted. Message: %v",
				lease.Pattern, err)
		}
	}
	// If there was a cert request then we require HTTPS. Most go clients will use
	// the gate API and will be using cert requests.
	//
	// So this covers detecting TLS for go usecases and above we checked for most
	// third-party usecases which will not be able to register with the portal
	// API.
	// TODO: maybe do more robust TLS checking with the same state machine idea as
	// in tcp_proxyserv.go, but it's niche, it would only help non-FixedPort
	// non-CertificateRequest backends that do use their own cert.
	if len(request.CertificateRequest) != 0 {
		protocol = "https://"
	}

	backend, err := url.Parse(protocol + hostPort)
	if err != nil {
		return err
	}
	// Store the forwarder
	backendQuery := backend.RawQuery
	pattern := lease.Pattern

	// Accept certificates signed by the latest portal cert
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialTLSContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		usedConf := conf
		if conf.RootCAs != nil {
			usedConf = conf.Clone()
			usedConf.RootCAs = p.state.RootCAs()
		}
		dialer := &tls.Dialer{Config: usedConf}
		return dialer.DialContext(ctx, network, addr)
	}

	proxy := &httputil.ReverseProxy{
		Transport: transport,
		Director: func(req *http.Request) {
			// Copied from https://golang.org/src/net/http/httputil/reverseproxy.go?s=2588:2649#L80
			req.URL.Scheme = backend.Scheme
			req.URL.Host = backend.Host
			// Can't use path.Join(., .) because it calls path.Clean which
			// causes a redirect loop if the pattern has a trailing / because
			// this will remove it and the DefaultServeMux will redirect no
			// trailing slash to trailing slash.
			if req.URL.Path[0] != '/' {
				req.URL.Path = "/" + req.URL.Path
			}

			// For security, delete any Forwarded or X-Forwarded-* headers sent by the
			// user. If we didn't remove them they might be able to affect certain
			// backends since we don't always overwrite the value. For example change
			// the paths generated on server side to scripts.
			//
			// TODO: do we need to avoid removing X-Forwarded-For in the case that
			// someone chains multiple portals together in a double reverse proxy?
			// It's suppossed to make a list of all the r. proxies in the value and
			// the go standard library does it.
			for key, _ := range req.Header {
				canonical := http.CanonicalHeaderKey(key)
				if canonical == "Forwarded" ||
					strings.HasPrefix(canonical, "X-Forwarded") {
					delete(req.Header, key)
				}
			}

			if request.StripPattern {
				if pattern[len(pattern)-1] != '/' { // if the pattern doesn't end in / then it's exact match only
					req.URL.Path = "/"
					req.Header.Add("X-Forwarded-Prefix", pattern)
				} else {
					prefix := pattern[0 : len(pattern)-1]
					req.URL.Path = strings.TrimPrefix(req.URL.Path, prefix)
					if req.URL.Path == "" {
						req.URL.Path = "/"
					}
					req.Header.Add("X-Forwarded-Prefix", prefix)
				}
			}
			req.URL.Path = backend.Path + req.URL.Path
			if backendQuery == "" || req.URL.RawQuery == "" {
				req.URL.RawQuery = backendQuery + req.URL.RawQuery
			} else {
				req.URL.RawQuery = backendQuery + "&" + req.URL.RawQuery
			}
			if _, ok := req.Header["User-Agent"]; !ok {
				// explicitly disable User-Agent if not set so it's not set to default value
				req.Header.Set("User-Agent", "")
			}
			// Note: httputil.ReverseProxy automatically adds X-Forwarded-For
			// https://cs.opensource.google/go/go/+/master:src/net/http/httputil/reverseproxy.go;l=440;drc=2449bbb5e614954ce9e99c8a481ea2ee73d72d61
			req.Header.Add("X-Forwarded-Host", req.Host) // This includes the port if non-standard
			if req.TLS == nil {
				req.Header.Add("X-Forwarded-Proto", "http")
			} else {
				req.Header.Add("X-Forwarded-Proto", "https")
			}
			if _, port, err := net.SplitHostPort(req.RemoteAddr); err == nil {
				req.Header.Add("X-Forwarded-For-Port", port) // The client's port
			}
			// TODO: also do X-Forwarded-Port if portal is running on a non-standard
			// port
		},
	}

	if request.AllowHttp {
		p.allowsHTTP.Store(true)
	}

	fwd := &forwarder{
		Handler:   proxy,
		Lease:     lease,
		AllowHTTP: request.AllowHttp,
	}
	p.forwarders.Store(pattern, fwd)
	return nil
}

// urlMatchesPattern returns whether or not the url matches the pattern string.
func urlMatchesPattern(url, pattern string) bool {
	if len(pattern) == 0 || len(url) == 0 {
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
func (p *httpProxy) selectForwarder(host, path string) *forwarder {
	var ret *forwarder = nil
	var mostSpecificPattern string
	p.forwarders.Range(func(key, value interface{}) bool {
		pattern := key.(string)
		patternHost, pattern := gate.ParsePattern(pattern)

		// If the hostname doesn't match skip this forwarder
		if patternHost == "" {
			if host != "" && p.defaultHost != "" && p.defaultHost != host {
				return true
			}
		} else {
			if host != "" && patternHost != "*" && patternHost != host {
				return true
			}
			// If we are matching a pattern that doesn't have a host (host == ""), and
			// a default host is setup, then match as if host was p.defaultHost
			if host == "" && p.defaultHost != "" && p.defaultHost != patternHost {
				return true
			}
		}
		// If the pattern doesn't match skip this forwarder
		if !urlMatchesPattern(path, pattern) {
			return true
		}
		// The pattern matches, find the most specific one
		if ret == nil || // First one we've seen
			pattern == strings.TrimSuffix(mostSpecificPattern, "/") || // the single file version of an existing directory pattern
			len(pattern) > len(mostSpecificPattern) { // The longer pattern is just a subdir of the shorter pattern (now that the above case is handled)

			mostSpecificPattern = pattern
			ret = value.(*forwarder)
		}
		return true
	})
	return ret
}

// ServeHTTP is the HTTPProxy net/http handler func which selects a registered
// forwarder to handle the request based on the forwarder's pattern
func (p *httpProxy) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.URL.Path == "" {
		// TODO: What are these requests looking for? Should we redirect to '/'?
		// I don't understand how these are getting through because the go http
		// server seems to return errors for empty URIs so there has to be something
		// in there. The parsing code is too complex to guess what it could be.
		http.Error(w, "Empty path requested.", http.StatusBadRequest)
		log.Printf("%v requested an empty req.URL.Path. Raw URI: %q (useragent: %q)",
			req.RemoteAddr, req.RequestURI, req.UserAgent())
		return
	}
	fwd := p.selectForwarder(req.Host, req.URL.Path)
	if fwd == nil {
		log.Printf("%v requested unregistered path: %v%v (useragent: %q) ",
			req.RemoteAddr, req.Host, req.URL.EscapedPath(), req.UserAgent())
		http.NotFound(w, req)
		return
	}

	// TODO: HSTS UX needs more improvement
	//   - Need to track allowsHTTP per-host
	//   - Have a flag that enables 1 (or maybe 2) year HSTS for single subdomains
	//   - Have a flag that enables includeSubdomains, preload, immutable
	//   - Print some instructions on how to set this up in the logs
	//   - Should I just have a flag to set the raw string because there's lots of
	//     options?
	if !p.allowsHTTP.Load() {
		w.Header().Add("Strict-Transport-Security", "max-age=300")
	}

	if !fwd.AllowHTTP {
		if req.TLS == nil {
			tools.RedirectToHTTPS{}.ServeHTTP(w, req)
			return
		}
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

func makeChallengeHandler(webRoot string, challenges *acmeChallenges) (http.Handler, error) {
	var fileServer http.Handler = http.HandlerFunc(http.NotFound)
	if webRoot != "" {
		dir := tools.SecureHTTPDir{
			Dir:                   http.Dir(webRoot),
			AllowDotfiles:         true,
			AllowDirectoryListing: false,
		}
		if err := dir.TestOpen("/"); err != nil {
			log.Printf("Warning: the -cert_challenge_webroot %v could not be read: %v",
				webRoot, err)
			log.Print("If you want to use certbot, correct this error and restart portal.")
			log.Print("If you use -autocert_domains, you can set args: \"-cert_challenge_webroot=\" to suppress this warning.")
		} else {
			log.Printf("Serving acme challenge directory webroot: %v", webRoot)
			fileServer = http.FileServer(dir)
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		log.Printf("ACME challenge handler: %v requested %v%v (useragent: %q)",
			req.RemoteAddr, req.Host, req.URL.EscapedPath(), req.UserAgent())

		resp, ok := challenges.Read(req.URL.Path)
		if !ok {
			fileServer.ServeHTTP(w, req)
		}
		w.Write([]byte(resp))
	}), nil
}

func makeHTTPProxy(l *clientLeasor, rootCert *tls.Config,
	httpList, httpsList net.Listener, defaultHost string, challenges *acmeChallenges, certChallengeWebRoot string,
	state *stateManager) (*httpProxy, error) {
	ret := &httpProxy{
		clientLeasor: l,
		rootCert:     rootCert,
		state:        state,
		defaultHost:  defaultHost,
		httpList:     httpList,
		httpsList:    httpsList,
		challenges:   challenges,
	}
	l.OnCancel(ret.Unregister)

	// Set up serving cert challenges
	handler, err := makeChallengeHandler(certChallengeWebRoot, ret.challenges)
	if err != nil {
		log.Print("Failed to start cert challenge HTTP handler: ", err)
	} else {
		ret.forwarders.Store(certChallengePattern, &forwarder{
			Handler: handler,
			Lease: &gate.Lease{
				Pattern: certChallengePattern,
			},
			AllowHTTP: true,
		})
		log.Print("Started cert challenges HTTP handler.")
	}

	// Close the servers on quit signal
	return ret, nil
}

func (p *httpProxy) StartHTTP(quit chan struct{}) {
	// Start the HTTP server to redirect to HTTPS
	httpServer := &http.Server{
		Handler: p,
	}
	go runServer(quit, "HTTP", httpServer, p.httpList)
	go func() {
		<-quit
		httpServer.Close()
		fmt.Print("Got quit signal, killed HTTP servers")
	}()
}

func (p *httpProxy) StartHTTPS(serveCert *tls.Config, quit chan struct{}) {
	// Support HTTP/2. See https://pkg.go.dev/net/http#Serve
	// > HTTP/2 support is only enabled if ... configured with "h2" in the TLS Config.NextProtos.
	serveCert.NextProtos = append(serveCert.NextProtos, "h2")
	p.httpsList = tls.NewListener(p.httpsList, serveCert)
	// Start the TLS server
	tlsServer := &http.Server{
		Handler: p,
	}
	go runServer(quit, "HTTPS", tlsServer, p.httpsList)
	go func() {
		<-quit
		tlsServer.Close()
		fmt.Print("Got quit signal, killed HTTPS server")
	}()
}
