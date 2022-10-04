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
	"path"
	"path/filepath"
	"strings"

	_ "ask.systems/daemon/portal/flags"
	_ "ask.systems/daemon/tools/flags"

	"ask.systems/daemon/portal/gate"
	"ask.systems/daemon/tools"
)

// The filename to read username:password_hash logins per line from
var PasswordsFile = ".passwords"

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
		"If true, serve 404 for any files starting with . such as .passwords")
	logRequests := flags.Bool("log_requests", true, ""+
		"If true, log all paths requested plus the IP of the client.")
	passwordRealm := flags.String("password_realm", "host", ""+
		"The string to pass to the browser for the basic auth realm. The browser will\n"+
		"automatically send the same password if it has authorized with the realm before.")
	flags.Var(tools.BoolFuncFlag(hashPassword), "hash_password",
		"Set to hash a password for the .passwords file instead of hosting a server.")
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
			fullPath := prefix + req.URL.String()
			addr := req.Header.Get("Orig-Address")
			if err := checkAuth(*passwordRealm, &dir, w, req); err != nil {
				if *logRequests {
					log.Print("%v %v at %v", addr, err, fullPath)
				}
				return // Auth failed!
			}
			if *logRequests {
				size, _ := dir.FileSize(req.URL.Path)
				log.Printf("%v requested and was served %v (%v bytes)",
					addr, fullPath, size)
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

func checkAuth(realm string, dir *tools.SecureHTTPDir, w http.ResponseWriter, r *http.Request) error {
	// Never serve the PasswordsFile
	if path.Base(r.URL.Path) == PasswordsFile {
		http.NotFound(w, r)
		return fmt.Errorf("requested a passwords file!")
	}
	auth := tools.BasicAuthHandler{
		Realm: realm,
	}
	// Clean the request to get a filepath
	request := r.URL.Path
	if !strings.HasPrefix(request, "/") {
		request = "/" + request
	}
	if registerPasswords(&auth, dir, path.Clean(request)) == 0 {
		return nil // if there were no passwords, allow the request
	}
	passed := auth.Check(w, r)
	if passed {
		return nil
	}
	if username, _, ok := r.BasicAuth(); ok {
		return fmt.Errorf("failed auth as %v", username)
	} else {
		return fmt.Errorf("requested protected directory. Got login page")
	}
}

// Recursively scan parent directories for the PasswordsFile file and add
// passwords to the auth checker from the top-most directory first
func registerPasswords(auth *tools.BasicAuthHandler, dir *tools.SecureHTTPDir, name string) int {
	f, err := dir.Dir.Open(name)
	if err != nil {
		if name != "/" {
			// We might still be able to read some of the parent dirs
			return registerPasswords(auth, dir, path.Dir(name))
		} else {
			return 0 // At the root, nothing more to check
		}
	}
	dirStat, err := f.Stat()
	f.Close()
	if err == nil && !dirStat.IsDir() { // the name cannot be "/" if it's not a dir.
		// If it's not a dir, register passwords from the dir
		return registerPasswords(auth, dir, path.Dir(name))
	}
	// If we can't stat, just assume it was a dir and look for the .passwords
	// file.  If it was a file, then trying to load a file under it won't work
	// which is fine.

	// Register parent directory passwords first, so subdirectories override
	var parentRegistered int
	if name != "/" {
		parentRegistered = registerPasswords(auth, dir, path.Dir(name))
	}
	// Finally check the password file
	passwords, err := dir.Dir.Open(path.Join(name, PasswordsFile))
	if err != nil {
		return parentRegistered
	}
	// We found a passwords file, register it!
	registered := 0
	scanner := bufio.NewScanner(passwords)
	for scanner.Scan() {
		auth.SetLogin(scanner.Text())
		registered++
	}
	passwords.Close()
	return registered + parentRegistered
}

// The -hash_pasword utility
func hashPassword(string) error {
	fmt.Fprintf(os.Stderr,
		"After you hash your password, add a line to %v in the format username:password_hash.\n",
		PasswordsFile)
	fmt.Fprintf(os.Stderr, "Type your password, prints unmasked, then press enter: ")
	password, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		fmt.Fprint(os.Stderr, "Failed to read password: ", err)
		os.Exit(1)
	}
	fmt.Println(tools.BasicAuthHash(strings.TrimSpace(password)))
	os.Exit(0)
	return nil
}
