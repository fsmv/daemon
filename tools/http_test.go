package tools_test

import (
	"net/http"

	"ask.systems/daemon/tools"
)

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
