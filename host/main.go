package main

import (
	"flag"
	"log"
	"net/http"
	"path/filepath"
	"strings"

	_ "ask.systems/daemon/tools/flags"

	"ask.systems/daemon/portal"
	"ask.systems/daemon/tools"
)

var (
	webRoot = flag.String("web_root", "",
		"Directory to serve files from")
	urlPath = flag.String("url_path", "/", ""+
		"Url path to serve files under. A leading slash (/) is required and if you\n"+
		"don't specify a tailing slash only the single named file will be served\n"+
		"(out of the web_root directory).")
	directoryListing = flag.Bool("serve_directory_listing", true, ""+
		"If true serve a file browser page that lists the directory contents for the\n"+
		"web_root and sub-folders. If false serve 404 for directory paths, but still\n"+
		"serve index.html.")
	serveDotfiles = flag.Bool("serve_dotfiles", false, ""+
		"If true, serve 404 for any files starting with . such as .htaccess")
	logRequests = flag.Bool("log_requests", true, ""+
		"If true, log all paths requested plus the IP of the client.")
)

func main() {
	portal.DefineFlags()
	flag.Parse()
	quit := make(chan struct{})
	tools.CloseOnQuitSignals(quit)

	url := *urlPath
	lease, tlsConf := portal.MustStartTLSRegistration(&portal.RegisterRequest{
		Pattern: url,
	}, quit)

	// Setup the server handler
	dir := tools.SecureHTTPDir{
		Dir:                   http.Dir(*webRoot),
		AllowDotfiles:         *serveDotfiles,
		AllowDirectoryListing: *directoryListing,
	}
	fileServer := http.FileServer(dir)
	if strings.HasSuffix(url, "/") {
		fileServer = http.StripPrefix(url, fileServer)
	} else {
		// Don't strip off the filename if we're serving a single file
		fileServer = http.StripPrefix(filepath.Dir(url), fileServer)
	}
	http.HandleFunc(url, func(w http.ResponseWriter, req *http.Request) {
		if *logRequests {
			log.Printf("%v requested %v", req.Header.Get("Orig-Address"), req.URL)
		}
		fileServer.ServeHTTP(w, req)
	})

	// Test if we can open the files, http.FileServer doesn't log anything helpful
	webrootFile, err := dir.Dir.Open("/")
	if err == nil {
		_, err = webrootFile.Stat()
		webrootFile.Close()
	}
	if err != nil {
		log.Print("WARNING: Failed to open and stat web_root directory, we probably can't serve anything. Error: ", err)
	}

	tools.RunHTTPServerTLS(lease.Port, tlsConf, quit)
	log.Print("Goodbye.")
}
