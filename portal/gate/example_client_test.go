package gate_test

import (
	"flag"
	"log"
	"net/http"

	_ "ask.systems/daemon/portal/flags" // -portal_addr and -portal_token
	_ "ask.systems/daemon/tools/flags"  // for the -version and -syslog flags

	"ask.systems/daemon/portal/gate"
	"ask.systems/daemon/tools"
)

var pattern = flag.String("pattern", "/hello/", "The path to register with portal")

func Example() {
	flag.Parse()
	quit := make(chan struct{})
	tools.CloseOnQuitSignals(quit) // close the channel when the OS says to stop

	// Remove the optional URL prefix from the pattern, portal can serve multiple URLs
	_, path := gate.ParsePattern(*pattern)
	// Register with portal, generate a TLS cert signed by portal, and keep the
	// registration and cert renewed in the background (until quit is closed)
	lease, tlsConf := gate.MustStartTLSRegistration(&gate.RegisterRequest{
		Pattern: *pattern,
	}, quit)

	// Serve Hello World with standard go server functions
	http.Handle(path, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Write([]byte("Hello World!"))
		// portal adds this header to tell you who sent the request to portal
		log.Printf("Hello from %v:%v",
			req.Header.Get("X-Forwarded-For"),
			req.Header.Get("X-Forwarded-Port"))
	}))

	// Run the server and block until the channel is closed and the graceful stop
	// is done. Uses the handlers registered with the http package global system
	tools.RunHTTPServerTLS(lease.Port, tlsConf, quit)
	log.Print("Goodbye")
}
