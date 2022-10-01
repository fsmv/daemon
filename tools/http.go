package tools

import (
	"context"
	"crypto/tls"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

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
	log.Print("Starting server...")
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
	log.Print("Shutting down HTTPS Server...")
	log.Print("Waiting up to 10 seconds for HTTPS connections to close.")
	ttl, _ := context.WithTimeout(context.Background(), 10*time.Second)
	code := srv.Shutdown(ttl)
	log.Print("HTTPS server exit status:", code)
}

// Starts an HTTP server on the specified port and block until the quit channel
// is closed and graceful shutdown has finished.
func RunHTTPServer(port uint32, quit chan struct{}) {
	log.Print("Starting server...")
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
	ttl, _ := context.WithTimeout(context.Background(), 10*time.Second)
	code := srv.Shutdown(ttl)
	log.Print("HTTP server exit status:", code)
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
	if !strings.HasPrefix(request, "/") {
		request = "/" + request
	}
	f, err := s.Open(path.Clean(request))
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
