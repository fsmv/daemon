package tools

import (
	"context"
	"crypto/tls"
	"log"
	"net/http"
	"strconv"
	"time"
)

func RunHTTPServerTLS(port uint32, config *tls.Config, quit chan struct{}) {
	log.Print("Starting server...")
	var srv http.Server

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
