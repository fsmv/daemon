package main

import (
    "context"
    "flag"
    "net/http"
    "log"
    "os"
    "os/signal"
    "strconv"
    "strings"
    "time"

    "daemon/feproxy/proxyserv"
)

var (
    feproxyAddr = flag.String("feproxy_addr", "127.0.0.1:2048",
        "Address and port for the feproxy server")
    webRoot = flag.String("web_root", "",
        "Directory to serve files from")
    urlPath = flag.String("url_path", "/",
        "Url path to serve the files under. Leading and trailing slashes are\n\t" +
        "optional but encouraged. For example \"/test/\" would serve your files\n\t" +
        "under 127.0.0.1/test/.")
)

// TODO: see if the default file server allows disabling directory listings

func addSlashes(path string) string {
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

func shutdownOnSignals(srv *http.Server) {
    sigs := make(chan os.Signal, 2)
    signal.Notify(sigs, os.Interrupt, os.Kill)
    go func() {
        switch <-sigs {
        case os.Interrupt:
            log.Print("Recieved keyboard interrupt")
        case os.Kill:
            log.Print("Recieved kill signal")
        }
        log.Print("Shutting down...")
        log.Print("Waiting 10 seconds for connections to close.")
        ttl, _ := context.WithTimeout(context.Background(), 10 * time.Second)
        code := srv.Shutdown(ttl)
        log.Print("Exit code:", code)
        os.Exit(0)
    }()
}

func main() {
    flag.Parse()
    var srv http.Server
    shutdownOnSignals(&srv)
    url := addSlashes(*urlPath)
    // Register with feproxy and get the port
    fe, lease := proxyserv.MustConnectAndRegister(*feproxyAddr, url)
    defer fe.Unregister(lease.Pattern)
    defer fe.Close()
    go fe.MustKeepLeaseRenewedForever(lease)
    srv.Addr = ":" + strconv.Itoa(int(lease.Port))
    // Start the server
    fileServer := http.StripPrefix(url, http.FileServer(http.Dir(*webRoot)))
    http.HandleFunc(url, func(w http.ResponseWriter, req *http.Request) {
        log.Printf("%v requested %v", req.Header.Get("Orig-Address"), req.URL)
        fileServer.ServeHTTP(w, req)
    })
    log.Print("Starting server...")
    err := srv.ListenAndServe()
    if err != nil && err != http.ErrServerClosed {
        log.Fatal("Server died:", err)
    }
}
