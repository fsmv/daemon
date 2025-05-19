package gate_test

import (
	"context"
	"flag"
	"log"
	"net/http"
	"sync"

	_ "ask.systems/daemon/portal/flags" // -portal_addr and -portal_token
	_ "ask.systems/daemon/tools/flags"  // for the -version and -syslog flags

	"ask.systems/daemon/portal/gate"
	"ask.systems/daemon/tools"
)

var pattern = flag.String("pattern", "/hello/", "The path to register with portal")

func Example() {
	flag.Parse()
	// Setup graceful stopping
	//   - Call stop() to trigger a full server graceful shutdown
	//   - When ctrl+C or other OS quit signals are sent ctx is cancelled
	//   - If you start other background tasks, use wg.Add(1) and wg.Done() and
	//     exit when ctx.Done() is closed to participate in the graceful shutdown.
	ctx, stop := context.WithCancel(context.Background())
	ctx = tools.ContextWithQuitSignals(ctx)
	wg := &sync.WaitGroup{}
	defer func() {
		stop()
		wg.Wait()
		log.Print("Goodbye.")
	}()

	// Register with portal, generate a TLS cert signed by portal, and keep the
	// registration and cert renewed in the background (until ctx is cancelled)
	reg, err := gate.AutoRegister(ctx, &gate.RegisterRequest{
		Pattern: *pattern,
	}, wg)
	if err != nil {
		log.Fatal(err)
	}

	// Remove the optional URL prefix from the pattern (http.Handle doesn't understand it)
	_, path := gate.ParsePattern(*pattern)
	// Serve Hello World with standard go server functions
	http.Handle(path, http.HandlerFunc(
		func(w http.ResponseWriter, req *http.Request) {
			w.Write([]byte("Hello World!"))
			// portal adds these headers to tell you who sent the request to portal
			log.Printf("Hello from %v:%v",
				req.Header.Get("X-Forwarded-For"),
				req.Header.Get("X-Forwarded-For-Port"))
		}))

	// Run the server and block until ctx is cancelled and the graceful stop is
	// done.
	//
	// Uses the handlers registered with the http package global system.  The last
	// argument allows you to set http.Server options etc for more advanced
	// configuration.
	//
	// If you have a more complex main function you might want to run this in a
	// goroutine and use wg.Add(1) before and wg.Done() after. You may also want
	// to turn on Quiet mode and do your own error logging using the return error.
	tools.HTTPServer(ctx.Done(), reg.Lease.Port, reg.TLSConfig, nil)
}
