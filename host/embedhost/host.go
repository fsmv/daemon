// Embedhost lets you run the host binary main function inside another program
//
// This is used by [ask.systems/daemon], but feel free to use it if you want to!
package embedhost

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
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
		"If true, serve files starting with . such as .passwords, instead of\n"+
		"serving 404 which is the default for security.")
	logRequests := flags.Bool("log_requests", true, ""+
		"If true, log all paths requested plus the IP of the client.")
	passwordRealm := flags.String("password_realm", "host", ""+
		"The string to pass to the browser for the basic auth realm. The browser will\n"+
		"automatically send the same password if it has authorized with the realm\n"+
		"before.")
	flags.Var(tools.BoolFuncFlag(hashPassword), "hash_password", ""+
		"Set this flag to run a password hash utility for the .passwords file,\n"+
		"instead of hosting a server.")
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
		BasicAuthRealm:        *passwordRealm,
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
			fullPath := prefix + req.URL.String()
			clientName := fmt.Sprintf("%v:%v (useragent: %q)",
				req.Header.Get("X-Forwarded-For"), req.Header.Get("X-Forwarded-For-Port"),
				req.UserAgent())
			if err := dir.CheckPasswordsFiles(w, req); err != nil {
				if *logRequests {
					log.Printf("%v %v at %v", clientName, err, fullPath)
				}
				return // Auth failed!
			}
			if *logRequests {
				w = tools.NewSizeTrackerHTTPResponseWriter(w)
			}
			fileServer.ServeHTTP(w, req)
			if *logRequests {
				size := w.(tools.SizeTrackerHTTPResponseWriter).BytesRead()
				log.Printf("%v requested and was served %v (%v bytes)",
					clientName, fullPath, size)
			}
		},
	)))

	// Test if we can open the files, http.FileServer doesn't log anything helpful
	if err := dir.TestOpen("/"); err != nil {
		log.Print("WARNING: Failed to open and stat web_root directory, we probably can't serve anything. Error: ", err)
	}

	tools.RunHTTPServerTLS(lease.Port, tlsConf, quit)
	log.Print("Goodbye.")
}

// The -hash_pasword utility
func hashPassword(string) error {
	fmt.Fprintf(os.Stderr,
		"After you hash your password, add a line to %v in the format username:password_hash.\n",
		tools.PasswordsFile)
	fmt.Fprintf(os.Stderr, "Type your password, prints unmasked, then press enter: ")
	password, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		fmt.Fprint(os.Stderr, "Failed to read password: ", err)
		os.Exit(1)
	}
	fmt.Println(tools.HashPassword(strings.TrimSpace(password)))
	os.Exit(0)
	return nil
}
