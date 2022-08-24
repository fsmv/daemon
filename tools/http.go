package tools

import (
  "log"
  "time"
  "net/http"
  "strconv"
  "context"
)

func RunHTTPServer(port uint32, quit chan struct{}) {
  log.Print("Starting server...")
  var srv http.Server
  srv.Addr = ":" + strconv.Itoa(int(port))
  go func () {
    err := srv.ListenAndServe()
    if err != nil && err != http.ErrServerClosed {
      close(quit)
      log.Fatal("Server died:", err)
    }
  }()

  <-quit
  log.Print("Shutting down HTTP Server...")
  log.Print("Waiting 10 seconds for HTTP connections to close.")
  ttl, _ := context.WithTimeout(context.Background(), 10 * time.Second)
  code := srv.Shutdown(ttl)
  log.Print("HTTP server exit status:", code)
}
