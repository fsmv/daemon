package tools_test

import (
	"context"
	"crypto/tls"
	"errors"
	"log"
	"net/http"
	"time"

	"ask.systems/daemon/tools"
)

// Shows the usual way of using HTTPServer to make an https cert
//
// For a more realistic usage example check the package example of
// [ask.systems/daemon/portal/gate].
//
// See also: [ask.systems/daemon/portal/gate.AutoRegister] and
// [tools.AutorenewSelfSignedCertificate]
func ExampleHTTPServer_normal() {
	ctx, stop := context.WithTimeout(context.Background(), 30*time.Second)
	defer stop()
	var cert *tls.Config // get this with gate.AutoRegister
	err := tools.HTTPServer(ctx, 8080, cert, nil)
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Print("Server failed:", err)
	}
}

func ExampleHTTPServer_onlyHTTP() {
	ctx, stop := context.WithTimeout(context.Background(), 30*time.Second)
	defer stop()
	err := tools.HTTPServer(ctx, 8080, nil, &tools.HTTPServerOptions{
		Server: &http.Server{Handler: tools.RedirectToHTTPS{}},
	})
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Print("Server failed:", err)
	}
}

func ExampleHTTPServer_turnOffLogging() {
	ctx, stop := context.WithTimeout(context.Background(), 30*time.Second)
	defer stop()
	err := tools.HTTPServer(ctx, 8080, nil, &tools.HTTPServerOptions{
		Quiet: true,
	})
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Print("Server failed:", err)
	}
}

func ExampleRedirectToHTTPS() {
	// Serve an encrypted greeting
	httpsServer := &http.Server{
		Addr: ":443",
		Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			w.Write([]byte("Hello!"))
		}),
	}
	go httpsServer.ListenAndServeTLS("example.cert", "example.key")
	// Redirect any unencrypted connections to the encrypted server
	httpServer := &http.Server{
		Addr:    ":80",
		Handler: tools.RedirectToHTTPS{},
	}
	go httpServer.ListenAndServe()
}

// How to use [http.FileServer] and disallow directory listing
func ExampleSecureHTTPDir() {
	const localDirectory = "/home/www/public/"
	const servePath = "/foo/"
	dir := tools.SecureHTTPDir{
		Dir:                   http.Dir(localDirectory),
		AllowDirectoryListing: false,
	}
	http.Handle(servePath, http.StripPrefix(servePath, http.FileServer(dir)))
}

func ExampleSecureHTTPDir_CheckPasswordsHandler() {
	dir := tools.SecureHTTPDir{
		Dir:            http.Dir("/home/www/public/"),
		BasicAuthRealm: "daemon",
	}
	const servePath = "/filez/"
	http.Handle(servePath,
		http.StripPrefix(servePath, dir.CheckPasswordsHandler(http.FileServer(dir))))
	// Then start the http server
}

func ExampleSecureHTTPDir_CheckPasswordsFiles() {
	dir := tools.SecureHTTPDir{
		Dir:            http.Dir("/home/www/public/"),
		BasicAuthRealm: "daemon",
	}
	fileServer := http.FileServer(dir)
	const servePath = "/filez/"
	http.Handle(servePath, http.StripPrefix(servePath,
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			err := dir.CheckPasswordsFiles(w, r)
			if err == nil {
				fileServer.ServeHTTP(w, r)
			} else {
				// These headers are added by portal (and other reverse proxies)
				log.Printf("%v:%v failed authentication: %v",
					r.Header.Get("X-Forwarded-For"), r.Header.Get("X-Forwarded-For-Port"), err)
			}
		})))
	// Then start the http server
}
