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

const ttlCheckFreq = "15m"

type ProxyServ struct {
    Server         *http.Server

    handlersMut    *sync.RWMutex
    handlers       map[string]HandlerTTL  // Protected by handlersMut
    openPorts      []int                  // Protected by handlersMut
    openPortOffset uint16
    ttlString      string
    ttlDuration    time.Duration
    ttlCheckTicker *time.Ticker
    quit           chan struct{}
}

type HandlerTTL struct {
    H       http.Handler
    Timeout time.Time
    Port    uint16
}

type Lease struct {
    AssignedPort uint16
    TTL          string
}

func (s *ProxyServ) GetNumLeases() int {
    s.handlersMut.RLock()
    defer s.handlersMut.RUnlock()
    ret := len(s.handlers)
    return ret
}

func patternMatch(pattern, url string) bool{
    if len(pattern) == 0 {
        return false
    }
    n := len(pattern)
    if pattern[n-1] != '/' {
        return pattern == url
    }
    return len(url) >= n && url[0:n] == pattern
}

func (s *ProxyServ) getHandler(url string) (ret HandlerTTL, found bool) {
    s.handlersMut.RLock()
    defer s.handlersMut.RUnlock()
    var longestMatch = 0
    found = false
    for k, h := range s.handlers {
        if !patternMatch(k, url) {
            continue
        }
        if !found || len(k) > longestMatch {
            found = true
            longestMatch = len(k)
            ret = h
        }
    }
    return ret, found
}

func (s *ProxyServ) addHandler(url string, handler HandlerTTL) {
    s.handlersMut.Lock()
    defer s.handlersMut.Unlock()
    s.handlers[url] = handler
}

func (s *ProxyServ) DeleteHandler(url string) {
    s.handlersMut.Lock()
    defer s.handlersMut.Unlock()
    if h, ok := s.handlers[url]; ok {
        s.openPorts = append(s.openPorts, int(h.Port))
        delete(s.handlers, url)
    }
}

func (s *ProxyServ) getNextPort() (uint16, error) {
    s.handlersMut.Lock()
    defer s.handlersMut.Unlock()
    if len(s.openPorts) == 0 {
        return 0, fmt.Errorf("No remaining ports to lease. Active leases: %v", s.GetNumLeases())
    }
    port := uint16(s.openPorts[0]) + s.openPortOffset
    s.openPorts = s.openPorts[1:]
    return port, nil
}

func (s *ProxyServ) checkTTLs() {
    for {
        select {
        case <-s.ttlCheckTicker.C: // on next tick
            s.handlersMut.Lock()
            now := time.Now()
            for k, h := range s.handlers {
                if now.After(h.Timeout) {
                    s.openPorts = append(s.openPorts, int(h.Port))
                    delete(s.handlers, k)
                }
            }
            s.handlersMut.Unlock()
        case <-s.quit: // on quit
            s.ttlCheckTicker.Stop()
            return
        }
    }
}

func (s *ProxyServ) Register(path string) (lease Lease, err error) {
    port, err := s.getNextPort()
    if err != nil {
        return Lease{}, err
    }
    backend, err := url.Parse("http://127.0.0.1:" + strconv.Itoa(int(port)))
    if err != nil {
        log.Fatal(err)
    }
    s.addHandler(path, HandlerTTL{
        H:       httputil.NewSingleHostReverseProxy(backend),
        Timeout: time.Now().Add(s.ttlDuration),
    })
    return Lease{
        AssignedPort: port,
        TTL:          s.ttlString,
    }, nil
}

func StartNew(tlsCert, tlsKey string,
              startPort, endPort uint16,
              leaseTTL string, quit chan struct{}) *ProxyServ {
    if !(startPort < endPort) {
        log.Fatal("startPort must be less than endPort")
        return nil
    }
    r := rand.New(rand.NewSource(time.Now().UnixNano()))
    mux := http.NewServeMux()
    ttlDuration, err := time.ParseDuration(leaseTTL)
    if err != nil {
        log.Fatalf("leaseTTL string format bad, got: %v", leaseTTL)
        return nil
    }
    s := &ProxyServ{
        Server:         &http.Server{Handler: mux},
        handlersMut:    &sync.RWMutex{},
        handlers:       make(map[string]HandlerTTL),
        openPorts:      r.Perm(int(endPort - startPort)),
        openPortOffset: startPort,
        ttlString:      leaseTTL,
        ttlDuration:    ttlDuration,
        ttlCheckTicker: time.NewTicker(ttlDuration),
        quit:           quit,
    }
    go s.checkTTLs()
    // Handle all paths
    mux.HandleFunc("/",
        func (w http.ResponseWriter, req *http.Request) {
            h, ok := s.getHandler(req.URL.Path)
            // No handler for this path, 404
            if !ok {
                http.NotFound(w, req)
                return
            }
            // Handler timed out, delete it then 404
            if time.Now().After(h.Timeout) {
                s.DeleteHandler(req.URL.Path)
                http.NotFound(w, req)
                return
            }
            // forward to the correct handler
            h.H.ServeHTTP(w, req)
        })
    // Start the server
    go func () {
        err := s.Server.ListenAndServeTLS(tlsCert, tlsKey)
        if err != nil {
            log.Print("Proxy server error: ", err)
        }
        log.Print("Proxy server died, shutting down")
        close(s.quit)
    }()
    go func () {
        <-s.quit
        //s.Server.Close()
        log.Fatal("Killing process. Go 1.3 doesn't support http.Server.Close()")
    }()
    return s
}
