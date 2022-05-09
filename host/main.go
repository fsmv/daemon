package main

import (
    "log"
    "flag"
    "strings"
    "net/http"
    "path/filepath"

    "ask.systems/daemon/portal"
    "ask.systems/daemon/tools"
)

var (
    portalAddr = flag.String("portal_addr", "127.0.0.1:2048",
        "Address and port for the portal server")
    webRoot = flag.String("web_root", "",
        "Directory to serve files from")
    urlPath = flag.String("url_path", "/",
        "Url path to serve files under. A leading slash (/) is required and if you\n"+
        "don't specify a tailing slash only the single named file will be served (out of the web_root directory).")
)

func main() {
    flag.Parse()
    quit := make(chan struct{})
    tools.CloseOnSignals(quit)

    url := *urlPath
    lease := portal.MustStartRegistration(*portalAddr, &portal.RegisterRequest{
      Pattern: url,
    }, quit)

    // Setup the server handler
    dir := http.Dir(*webRoot)
    fileServer := http.FileServer(dir)
    if strings.HasSuffix(url, "/") {
      fileServer = http.StripPrefix(url, fileServer)
    } else {
      // Don't strip off the filename if we're serving a single file
      fileServer = http.StripPrefix(filepath.Dir(url), fileServer)
    }
    http.HandleFunc(url, func(w http.ResponseWriter, req *http.Request) {
        log.Printf("%v requested %v", req.Header.Get("Orig-Address"), req.URL)
        fileServer.ServeHTTP(w, req)
    })

    // Debugging info because http.Dir isn't helpful
    f, err := dir.Open("/")
    log.Printf("Test open: %v", err)
    _, err = f.Stat()
    log.Printf("Test stat: %v", err)
    f.Close()

    tools.RunHTTPServer(lease.Port, quit)
    log.Print("Goodbye.")
}
