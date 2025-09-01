// Embedhost lets you run the host binary main function inside another program
//
// This is used by [ask.systems/daemon], but feel free to use it if you want to!
package embedhost

import (
	"bufio"
	"context"
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

func Run(ctx context.Context, flags *flag.FlagSet, args []string) {
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

	// Extract the path part of the pattern and the prefix to remove
	_, servePath := gate.ParsePattern(*urlPath)
	prefix := servePath
	if !strings.HasSuffix(prefix, "/") {
		// Don't strip off the filename if we're serving a single file
		prefix = filepath.Dir(prefix)
	}

	// Setup the server handler
	var handler http.Handler
	dir := tools.SecureHTTPDir{
		Dir:                   http.Dir(*webRoot),
		AllowDotfiles:         *serveDotfiles,
		AllowDirectoryListing: *directoryListing,
		BasicAuthRealm:        *passwordRealm,
	}
	fileServer := http.FileServer(dir)
	if !*logRequests {
		handler = dir.CheckPasswordsHandler(fileServer)
	} else {
		handler = http.HandlerFunc(
			func(w http.ResponseWriter, req *http.Request) {
				fullPath := prefix + req.URL.String()
				clientName := fmt.Sprintf("%v:%v",
					req.Header.Get("X-Forwarded-For"),
					req.Header.Get("X-Forwarded-For-Port"))

				if err := dir.CheckPasswordsFiles(w, req); err != nil {
					log.Printf("%v %v at %v (useragent: %q)",
						clientName, err, fullPath, req.UserAgent())
					return // Auth failed!
				}
				sw := tools.NewSizeTrackerHTTPResponseWriter(w)
				fileServer.ServeHTTP(sw, req)
				log.Printf("%v requested and was served %v (%v bytes) useragent: %q",
					clientName, fullPath, sw.BytesRead(), req.UserAgent())
			})
	}
	http.Handle(servePath, http.StripPrefix(prefix, handler))

	// Test if we can open the files, http.FileServer doesn't log anything helpful
	if err := dir.TestOpen("/"); err != nil {
		log.Print("WARNING: Failed to open and stat web_root directory, we probably can't serve anything. Error: ", err)
	}

	// Setup graceful stopping
	ctx, _ = tools.ContextWithQuitSignals(context.Background())

	// Register the reverse proxy pattern with portal.
	// Only blocks until the initial registration is done, then keeps renewing.
	reg, waitForUnregister, err := gate.AutoRegister(ctx, &gate.RegisterRequest{
		Pattern: *urlPath,
	})
	if err != nil {
		log.Print("Fatal error registering with portal:", err)
		return
	}
	// Start serving files (blocks until graceful stop is done)
	tools.HTTPServer(ctx.Done(), reg.Lease.Port, reg.TLSConfig, nil)
	// Wait for the AutoRegister background goroutine to gracefully stop
	<-waitForUnregister
	log.Print("Goodbye.")
}

// The -hash_pasword utility
func hashPassword(string) error {
	fmt.Fprintf(os.Stderr,
		"After you hash your password, add a line to %v in the format username:password_hash.\n",
		tools.PasswordsFile)
	fmt.Fprintf(os.Stderr,
		"Type your password, prints unmasked, then press enter: ")
	password, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		fmt.Fprintln(os.Stderr, "Failed to read password:", err)
		os.Exit(1)
	}
	hash, err := tools.HashPassword(strings.TrimSpace(password))
	if err != nil {
		fmt.Fprintln(os.Stderr, "Failed to hash password:", err)
		os.Exit(1)
	} else {
		fmt.Println(hash)
		os.Exit(0)
	}
	return nil
}
