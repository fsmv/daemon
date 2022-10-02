// Embedhost lets you run the host binary main function inside another program
//
// This is used by [ask.systems/daemon], but feel free to use it if you want to!
package embedhost

import (
	"flag"
	"log"
	"net/http"
	"path/filepath"
	"strings"

	_ "ask.systems/daemon/portal/flags"
	_ "ask.systems/daemon/tools/flags"

	"ask.systems/daemon/portal/gate"
	"ask.systems/daemon/tools"
)

func Run(flags *flag.FlagSet, args []string) {
	webRoot := flags.String("web_root", "./",
		"Directory to serve files from")
	urlPath := flags.String("url_path", "/", ""+
		"Url path to serve files under. A leading slash (/) is required and if you\n"+
		"don't specify a tailing slash only the single named file will be served\n"+
		"(out of the web_root directory).")
	directoryListing := flags.Bool("serve_directory_listing", true, ""+
		"If true serve a file browser page that lists the directory contents for the\n"+
		"web_root and sub-folders. If false serve 404 for directory paths, but still\n"+
		"serve index.html.")
	serveDotfiles := flags.Bool("serve_dotfiles", false, ""+
		"If true, serve 404 for any files starting with . such as .htaccess")
	logRequests := flags.Bool("log_requests", true, ""+
		"If true, log all paths requested plus the IP of the client.")
	flags.Parse(args[1:])

	quit := make(chan struct{})
	tools.CloseOnQuitSignals(quit)

	pattern := *urlPath
	_, servePath := gate.ParsePattern(pattern)
	lease, tlsConf := gate.MustStartTLSRegistration(&gate.RegisterRequest{
		Pattern: pattern,
	}, quit)

	// Setup the server handler
	dir := tools.SecureHTTPDir{
		Dir:                   http.Dir(*webRoot),
		AllowDotfiles:         *serveDotfiles,
		AllowDirectoryListing: *directoryListing,
	}
	fileServer := http.FileServer(dir)
	var prefix string
	if strings.HasSuffix(servePath, "/") {
		prefix = servePath
	} else {
		// Don't strip off the filename if we're serving a single file
		prefix = filepath.Dir(servePath)
	}
	http.Handle(servePath, http.StripPrefix(prefix, http.HandlerFunc(
		func(w http.ResponseWriter, req *http.Request) {
			if *logRequests {
				size, _ := dir.FileSize(req.URL.Path)
				log.Printf("%v requested %v (%v bytes)",
					req.Header.Get("Orig-Address"), prefix+req.URL.String(), size)
			}
			fileServer.ServeHTTP(w, req)
		},
	)))

	// Test if we can open the files, http.FileServer doesn't log anything helpful
	if err := dir.TestOpen("/"); err != nil {
		log.Print("WARNING: Failed to open and stat web_root directory, we probably can't serve anything. Error: ", err)
	}

	tools.RunHTTPServerTLS(lease.Port, tlsConf, quit)
	log.Print("Goodbye.")
}
