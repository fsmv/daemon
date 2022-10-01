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
