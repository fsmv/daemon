package main

import (
    "flag"
    "net/http"
    "log"
    "strings"

    "ask.systems/daemon/portal"
    "ask.systems/daemon/tools"
)

var (
    portalAddr = flag.String("portal_addr", "127.0.0.1:2048",
        "Address and port for the portal server")
    webRoot = flag.String("web_root", "",
        "Directory to serve files from")
    urlPath = flag.String("url_path", "/",
        "Url path to serve the files under. Leading and trailing slashes are\n" +
        "optional but encouraged. For example \"/test/\" would serve your files\n" +
        "under 127.0.0.1/test/.")
)

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

func main() {
    flag.Parse()
    quit := make(chan struct{})
    tools.CloseOnSignals(quit)

    url := addSlashes(*urlPath)
    lease := portal.MustStartRegistration(*portalAddr, &portal.RegisterRequest{
      Pattern: url,
    }, quit)

    // Setup the server handler
    dir := http.Dir(*webRoot)
    fileServer := http.StripPrefix(url, http.FileServer(dir))
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
