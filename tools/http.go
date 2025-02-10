/*
Tools provides utility functions useful for web servers

Also check out the optional [ask.systems/daemon/tools/flags] library which
provides -version and -syslog when you include it.

Common features:

  - Run a web server with graceful shutdown when the quit channel is closed in
    one function call. Prefer [RunHTTPServerTLS].
  - Easily setup standard signal handlers to close your quit channel with
    [CloseOnQuitSignals]
  - Generate random tokens or secret URL paths with [RandomString]
  - Authenticate users via HTTP basic auth with [BasicAuthHandler]
  - [SecureHTTPDir] which is a way to use [http.FileServer] and not serve
    directory listings, as well as password protect directories with .passwords
    files. [ask.systems/daemon/host] uses this so it's only needed if you want a
    file server as part a larger application.

Less common features:

  - Generate self signed certificates and be your own Certificate Authority.
    These certificate functions are used by [ask.systems/daemon/portal] and the
    [ask.systems/daemon/portal/gate] client library. You only need them if you
    want to do extra custom certificate logic.
  - Enforce HTTPS only with [RedirectToHTTPS]. [ask.systems/daemon/portal] uses
    this for all client connections, and will only connect to your backend via
    HTTPS. So you don't really need to use this unless you're accepting
    connections from clients other than portal.
  - Create a flag that parses with no value and runs a callback when it is
    parsed with [BoolFuncFlag]. This is how -version and -syslog from
    [ask.systems/daemon/tools/flags] works.
  - Prepend the current timestamp to any [io.Writer.Write] calls with
    [TimestampWriter]. This can be used for log files. This is already used by
    -syslog and the default [log] package prints the same timestamp format by
    default so this is only useful if you are working with custom output
    streams that you want timestamps for.
*/
package tools

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// The filename to read username:password_hash logins per line from when using
// [SecureHTTPDir.CheckPasswordsFiles]
var PasswordsFile = ".passwords"

func strSliceContains(slice []string, key string) bool {
	for _, val := range slice {
		if val == key {
			return true
		}
	}
	return false
}

// Starts an HTTPS server on the specified port using the TLS config and block
// until the quit channel is closed and graceful shutdown has finished.
func RunHTTPServerTLS(port uint32, config *tls.Config, quit chan struct{}) {
	log.Printf("Starting HTTPS server on port %d...", port)
	var srv http.Server

	// Support HTTP/2. See https://pkg.go.dev/net/http#Serve
	// > HTTP/2 support is only enabled if ... configured with "h2" in the TLS Config.NextProtos.
	if !strSliceContains(config.NextProtos, "h2") {
		config.NextProtos = append(config.NextProtos, "h2")
	}

	go func() {
		listener, err := tls.Listen("tcp", ":"+strconv.Itoa(int(port)), config)
		if err != nil {
			close(quit)
			log.Print("Failed to start listener for TLS server: ", err)
			return
		}
		err = srv.Serve(listener)
		if err != nil && err != http.ErrServerClosed {
			close(quit)
			log.Print("TLS Server died: ", err)
		}
	}()

	<-quit
	log.Printf("Shutting down HTTPS Server on port %d...", port)
	log.Printf("Waiting up to 10 seconds for HTTPS connections to close on port %d.", port)
	ttl, ttl_cancel := context.WithTimeout(context.Background(), 10*time.Second)
	code := srv.Shutdown(ttl)
	ttl_cancel()
	log.Printf("HTTPS server (port %d) exit status: %v", port, code)
}

// Starts an HTTP server on the specified port and block until the quit channel
// is closed and graceful shutdown has finished.
func RunHTTPServer(port uint32, quit chan struct{}) {
	log.Printf("Starting HTTP server on port %d...", port)
	var srv http.Server
	srv.Addr = ":" + strconv.Itoa(int(port))
	go func() {
		err := srv.ListenAndServe()
		if err != nil && err != http.ErrServerClosed {
			close(quit)
			log.Fatal("Server died:", err)
		}
	}()

	<-quit
	log.Print("Shutting down HTTP Server...")
	log.Print("Waiting up to 10 seconds for HTTP connections to close.")
	ttl, ttl_cancel := context.WithTimeout(context.Background(), 10*time.Second)
	code := srv.Shutdown(ttl)
	ttl_cancel()
	log.Print("HTTP server exit status:", code)
}

// Wraps another [http.Handler] and only calls the wrapped handler if BasicAuth
// passed for one of the registered users. Optionally can call
// [BasicAuthHandler.Check] in as many handlers as you want, and then you don't
// have to use the handler wrapping option.
//
//   - Options must be setup before any requests and then not changed.
//   - Methods may be called at any time, it's thread safe.
//   - This type must not be copied after first use (it holds sync containers)
type BasicAuthHandler struct {
	// Realm is passed to the browser and the browser will automatically send the
	// same credentials for a realm it has logged into before. Optional.
	Realm   string
	Handler http.Handler

	// If set, auth checks are performed using this function instead of the
	// default. CheckPassword is responsible for parsing the encoded parameters
	// from the authHash string and doing any base64 decoding, as well as doing
	// the hash comparison (which should be a constant time comparison).
	//
	// This allows for using any hash function that's needed with
	// BasicAuthHandler, or even accept multiple at once. Many are available in
	// [golang.org/x/crypto].
	//
	// If not set [CheckPassword] will be used.
	// The default will always accept the hashes from [HashPassword] and will
	// continue to accept hashes from old versions for compatibility.
	CheckPassword func(authHash, userPassword string) bool

	users      sync.Map // map from username string to password hash []byte
	init       sync.Once
	authHeader string
}

// Authorizes the given user to access the pages protected by this handler.
//
// The passwordHash must be a SHA256 [base64.URLEncoding] encoded string. You
// can generate this with [HashPassword].
func (h *BasicAuthHandler) SetUser(username string, passwordHash string) error {
	if username == "" {
		return errors.New("username must not be empty")
	}
	h.users.Store(username, passwordHash)
	return nil
}

// Authorizes a user with this handler using a "username:password_hash" string
//
// The password_hash must be a SHA256 [base64.URLEncoding] encoded string. You
// can generate this with [HashPassword].
func (h *BasicAuthHandler) SetLogin(login string) error {
	split := strings.Split(login, ":")
	if len(split) != 2 {
		return errors.New("Invalid login string. It should be username:password_hash.")
	}
	return h.SetUser(split[0], split[1])
}

// Unauthorize a given username from pages protected by this handler.
func (h *BasicAuthHandler) RemoveUser(username string) {
	h.users.Delete(username)
}

// Check HTTP basic auth and reply with Unauthorized if authentication failed.
// Returns true if authentication passed and then the users can handle the
// request.
//
// If it returns false auth failed the response has been sent and you can't
// write more.
//
// If you want to log authentication failures, you can use this call instead of
// wrapping your handler.
func (h *BasicAuthHandler) Check(w http.ResponseWriter, r *http.Request) bool {
	// Read the header and if it's not there tell the browser to prompt the user
	username, password, ok := r.BasicAuth()
	if !ok {
		h.init.Do(func() { // Just a cache for the string so we don't malloc every time
			if h.Realm == "" {
				h.authHeader = "Basic charset=\"UTF-8\""
			} else {
				h.authHeader = fmt.Sprintf(`Basic realm="%v", charset="UTF-8"`, h.Realm)
			}
		})
		w.Header().Set("WWW-Authenticate", h.authHeader)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return false
	}
	// Look up the user's password
	wantHash, ok := h.users.Load(username)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return false
	}

	passMatch := false
	if h.CheckPassword == nil {
		passMatch = CheckPassword(wantHash.(string), password)
	} else {
		passMatch = h.CheckPassword(wantHash.(string), password)
	}
	if !passMatch {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return false
	}
	return true // Auth passed
}

// The [http.Handler] interface function. Only calls the wrapped handler if the
// request has passed basic auth.
func (h *BasicAuthHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.Check(w, r) {
		h.Handler.ServeHTTP(w, r) // Auth passed! Call the wrapped handler
	}
}

// SecureHTTPDir is a replacement for [http.Dir] for use with
// [http.FileServer]. It allows you to turn off serving directory listings
// and hidden dotfiles.
//
// These settings are not thread safe so set them up before serving.
type SecureHTTPDir struct {
	http.Dir

	// If false, do not serve or list files or directories starting with '.'
	AllowDotfiles bool

	// If true, serve a page listing all the files in a directory for any
	// directories that do not have index.html. If false serve 404 instead, and
	// index.html will still be served for directories containing it.
	AllowDirectoryListing bool

	// If you're using [SecureHTTPDir.CheckPasswordsFiles] set this to an
	// application identifier string e.g. "daemon". The browser will remember the
	// realm after a successful login so the user won't have to keep typing the
	// password, and this works across multiple paths as well.
	BasicAuthRealm string
}

// Wraps a given handler and only calls it if [SecureHTTPDir.CheckPasswordsFiles]
// passes.  It probably doesn't make sense to use this with anything other than
// [http.FileServer]
func (s SecureHTTPDir) CheckPasswordsHandler(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		err := s.CheckPasswordsFiles(w, r)
		if err == nil {
			h.ServeHTTP(w, r)
		}
	})
}

// Call this before handling the request with [http.FileServer] in order to
// authenticate the user if the directory requested (or parent directories)
// contains a file named the value of [PasswordsFile] (default is .passwords).
// If the returned error is not nil, then authentication failed and an
// unauthorized http response has been written and sent. Otherwise nothing is
// written to the [http.ResponseWriter].
//
// The passwords file that is checked is the first one found when searching
// starting with the current directory, then the parent directory, and so on.
//
// This search ordering means that adding a [PasswordsFile] file somewhere in
// the directory tree makes access more restrictive than the parent directory.
// If you want to make a subdirectory allow more users than the parent
// directory, then you must copy all of the parent directory passwords into the
// [PasswordsFile] of the subdirectory, and then add extra users to that list.
//
// You can generate hashes with [HashPassword] and the format of the files is:
//
//	username1:password_hash1
//	user2:password_hash2
//
// The easiest way to use this is [SecureHTTPDir.CheckPasswordsHandler], but you
// will need to call this directly if you want to log errors for example.
func (s SecureHTTPDir) CheckPasswordsFiles(w http.ResponseWriter, r *http.Request) error {
	// Never serve the PasswordsFile
	if path.Base(r.URL.Path) == PasswordsFile {
		http.NotFound(w, r)
		return fmt.Errorf("requested a passwords file!")
	}
	auth := BasicAuthHandler{
		Realm: s.BasicAuthRealm,
	}
	if s.registerPasswords(&auth, s.cleanRequest(r.URL.Path)) == 0 {
		return nil // if there were no passwords, allow the request
	}
	passed := auth.Check(w, r)
	if passed {
		return nil
	}
	if username, _, ok := r.BasicAuth(); ok {
		return fmt.Errorf("failed auth as %v", username)
	} else {
		return fmt.Errorf("requested protected directory. Got login page.")
	}
}

// Recursively scan parent directories for the PasswordsFile file and add
// passwords to the auth checker only from the first passwords file seen on the
// way down
func (s SecureHTTPDir) registerPasswords(auth *BasicAuthHandler, name string) int {
	// Just assume name is a dir and look for the .passwords file. If it is a
	// file, then trying to load a file under it won't work which is fine. Not
	// checking if it is a dir saves us 2 syscalls per level!

	// Check for the password file
	passwords, err := s.Dir.Open(path.Join(name, PasswordsFile))
	if err != nil {
		// No password file, check the parent dirs
		parentRegistered := 0
		if name != "/" {
			parentRegistered = s.registerPasswords(auth, path.Dir(name))
		}
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
	return registered
}

// Clean the request to get a filepath
func (s SecureHTTPDir) cleanRequest(request string) string {
	if !strings.HasPrefix(request, "/") {
		request = "/" + request
	}
	return path.Clean(request)
}

// Test if we can open the given file.
//
// It's good to call this when you start up a file server because
// [http.FileServer] doesn't log anything on open errors.
func (s SecureHTTPDir) TestOpen(path string) error {
	webrootFile, err := s.Dir.Open(path) // Use the internal to bypass no dir listing
	if err == nil {
		_, err = webrootFile.Stat()
		webrootFile.Close() // if err != nil then the file is nil
	}
	return err
}

// Returns the file size in bytes that will be served for a given request path.
// This means that if it's a directory with index.html we return the size of
// index.html. Without the index, directories get size 0.
//
// You can safely ignore the error, it's just there in case you want to know why
// we returned 0
func (s SecureHTTPDir) FileSize(request string) (int64, error) {
	request = s.cleanRequest(request)
	f, err := s.Open(request)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return 0, err
	}
	if !stat.IsDir() {
		return stat.Size(), nil
	}
	idx, err := s.Open(path.Join(request, "/index.html"))
	if err != nil {
		return 0, err
	}
	defer idx.Close()
	iStat, err := idx.Stat()
	if err != nil {
		return 0, err
	}
	return iStat.Size(), nil
}

// Returns [fs.ErrNotExist] for files and directories that should not be
// accessed depending on the settings.
//
// This is the override over [http.Dir] that allows this class to work
func (s SecureHTTPDir) Open(name string) (http.File, error) {
	filename := filepath.Base(name)

	if !s.AllowDotfiles && strings.HasPrefix(filename, ".") {
		return nil, fs.ErrNotExist
	}

	if !s.AllowDirectoryListing {
		// Checks the same conditions that http.FileServer does when it decides if
		// it will serve the index page. So we only allow the Open(dir) call we're
		// handling to succeed if http.FileServer would serve the index.html file.
		//
		// See: https://cs.opensource.google/go/go/+/refs/tags/go1.19.1:src/net/http/fs.go;l=598,606,639,643;drc=d7df872267f9071e678732f9469824d629cac595
		f, err := s.Dir.Open(name)
		if err != nil {
			return f, err
		}
		stat, err := f.Stat()
		if err != nil {
			f.Close() // Don't defer to avoid opening the dir twice
			return nil, err
		}
		if stat.IsDir() {
			index := strings.TrimSuffix(name, "/") + "/index.html"
			indexFile, err := s.Dir.Open(index)
			if err != nil {
				f.Close()
				return nil, fs.ErrNotExist
			}
			defer indexFile.Close()
			if _, err := indexFile.Stat(); err != nil {
				f.Close()
				return nil, fs.ErrNotExist
			}
			// We can't return the index file, we were requested to open the dir
			return f, nil
		}
	}

	if s.AllowDirectoryListing && !s.AllowDotfiles {
		f, err := s.Dir.Open(name)
		if err != nil {
			return nil, err
		}
		return noDotfilesHTTPFile{f}, nil
	}

	return s.Dir.Open(name)
}

// Makes http.FileServer directory listings not include dot files
// Implements the http.File interface
type noDotfilesHTTPFile struct {
	http.File
}

// Unfortunately I have to have this implemented even though http.FileServer
// won't call it (since I have the other function). Technically we would still
// meet the interface without it because the original is available but I want to
// make sure no one gets a dotfile listing from this class.
func (f noDotfilesHTTPFile) Readdir(n int) ([]fs.FileInfo, error) {
	realResults, err := f.File.Readdir(n)
	if err != nil {
		return realResults, err
	}
	var trimmedResults []fs.FileInfo
	for _, result := range realResults {
		if strings.HasPrefix(result.Name(), ".") {
			continue
		}
		trimmedResults = append(trimmedResults, result)
	}
	return trimmedResults, nil
}

// http.FileServer will call this to do a directory listing page
// See: https://cs.opensource.google/go/go/+/refs/tags/go1.19.1:src/net/http/fs.go;l=134;drc=d7df872267f9071e678732f9469824d629cac595
func (f noDotfilesHTTPFile) ReadDir(n int) ([]fs.DirEntry, error) {
	d, ok := f.File.(fs.ReadDirFile)
	if !ok {
		// http.Dir supports this and so does anything using os.Open so no one
		// should get this error except maybe on old versions of go
		return nil, errors.New("Sorry noDotfilesHTTPFile requires the ReadDir function")
	}
	realResults, err := d.ReadDir(n)
	if err != nil {
		return realResults, err
	}
	var trimmedResults []fs.DirEntry
	for _, result := range realResults {
		if strings.HasPrefix(result.Name(), ".") {
			continue
		}
		trimmedResults = append(trimmedResults, result)
	}
	return trimmedResults, nil
}

// RedirectToHTTPS is an [http.Handler] which redirects any requests to the same
// url but with https instead of http.
type RedirectToHTTPS struct{}

// Unconditionally sets the url to https:// and then serves an HTTP 303 response
func (r RedirectToHTTPS) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	var url url.URL = *req.URL // make a copy
	url.Scheme = "https"
	url.Host = req.Host
	url.Host = url.Hostname() // strip the port if one exists
	http.Redirect(w, req, url.String(), http.StatusSeeOther)
}

type SizeTrackerHTTPResponseWriter struct {
	http.ResponseWriter
	bytesRead *atomic.Uint64
}

func NewSizeTrackerHTTPResponseWriter(w http.ResponseWriter) SizeTrackerHTTPResponseWriter {
	return SizeTrackerHTTPResponseWriter{
		ResponseWriter: w,
		bytesRead:      &atomic.Uint64{},
	}
}

func (w SizeTrackerHTTPResponseWriter) Write(input []byte) (n int, err error) {
	n, err = w.ResponseWriter.Write(input)
	w.bytesRead.Add(uint64(n))
	return
}

func (w SizeTrackerHTTPResponseWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w SizeTrackerHTTPResponseWriter) BytesRead() uint64 {
	return w.bytesRead.Load()
}
