package tools

import (
	"context"
	"crypto/tls"
	"log"
	"net/http"
	"strconv"
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
	log.Print("Waiting 10 seconds for HTTPS connections to close.")
	ttl, _ := context.WithTimeout(context.Background(), 10*time.Second)
	code := srv.Shutdown(ttl)
	log.Print("HTTPS server exit status:", code)
}

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
	log.Print("Waiting 10 seconds for HTTP connections to close.")
	ttl, _ := context.WithTimeout(context.Background(), 10*time.Second)
	code := srv.Shutdown(ttl)
	log.Print("HTTP server exit status:", code)
}
