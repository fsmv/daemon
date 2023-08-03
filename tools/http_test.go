package tools_test

import (
	"log"
	"net/http"

	"ask.systems/daemon/tools"
)

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
