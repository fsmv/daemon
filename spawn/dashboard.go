package main

import (
  "fmt"
  "log"
  "flag"
  "bytes"
  "embed"
  "net/http"
  "html/template"
  "crypto/sha256"
  "crypto/subtle"
  "encoding/base64"

  "ask.systems/daemon/portal"
  "ask.systems/daemon/tools"
)

var (
  portalAddr = flag.String("portal_addr", "127.0.0.1:2048",
    "Address and port for the portal server")
  passwordHash = flag.String("password_hash", "set me",
    "sha256sum hash of the 'admin' user's basic auth password.")
  wantUsernameHash = sha256.Sum256([]byte("admin"))
  wantPasswordHash []byte
  //go:embed *.tmpl.html
  templatesFS embed.FS
)

const (
  dashboardUrl = "/spawn/"
  logsUrl = "/spawn/logs"
)

type logStream struct {
  Children *Children
}

func (l *logStream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
  if !checkAuth(w, r) {
    return
  }

  flusher, ok := w.(http.Flusher)
  if !ok {
    http.Error(w, "Streaming is not supported.", http.StatusInternalServerError)
    return
  }
  logs, cancel := l.Children.StreamLogs()
  defer cancel()
  log.Print("Logs streaming connection started.")

  w.Header().Set("Content-Type", "text/event-stream")
  w.Header().Set("Connection", "keep-alive")
  w.Header().Set("Cache-Control", "no-cache")
  w.Header().Set("Access-Control-Allow-Origin", "*")
  for {
    select {
    case <-r.Context().Done():
      log.Print("Logs streaming connection closed.")
      return
    case message := <-logs:
      fmt.Fprintf(w, "event: %v\ndata: %v\n\n", message.Tag, message.Line)
      flusher.Flush()
    }
  }
}

type dashboard struct {
  Children *Children

  templates *template.Template
}

func checkAuth(w http.ResponseWriter, r *http.Request) bool {
  u, p, ok := r.BasicAuth()
  if !ok {
    w.Header().Set("WWW-Authenticate", `Basic realm="daemon", charset="UTF-8"`)
    http.Error(w, "Unauthorized", http.StatusUnauthorized)
    return false
  }
  uh := sha256.Sum256([]byte(u))
  ph := sha256.Sum256([]byte(p))
  userMatch := (1 == subtle.ConstantTimeCompare(uh[:], wantUsernameHash[:]))
  passMatch := (1 == subtle.ConstantTimeCompare(ph[:], wantPasswordHash[:]))
  if !(userMatch && passMatch) {
    http.Error(w, "Unauthorized", http.StatusUnauthorized)
    return false
  }
  return true
}

func (d *dashboard) ServeHTTP(w http.ResponseWriter, r *http.Request) {
  if !checkAuth(w, r) {
    return
  }
  // Auth OK

  if r.Method == "POST" {
    err := r.ParseForm()
    if err != nil {
      log.Print("Recieved invaid form data: ", err)
      http.Error(w, "Invalid form data", http.StatusBadRequest)
      return
    }
    if r.Form.Get("submit") == "restart" {
      name := r.Form.Get("name")
      log.Print("Restart request for ", name)
      go d.Children.RestartChild(name)
    }
  }

  d.Children.Lock()
  defer d.Children.Unlock()
  var buff bytes.Buffer
  err := d.templates.ExecuteTemplate(&buff, "dashboard.tmpl.html", d.Children.ByName)
  if err == nil {
    w.Write(buff.Bytes())
  } else {
    log.Print("Error in dashboard template: ", err)
    http.Error(w, "Internal Server Error", http.StatusInternalServerError)
  }
}

func StartDashboard(children *Children, quit chan struct{}) (dashboardQuit chan struct{}, err error) {
  // If the main  quit closes, shut down the dashboard. But, if the dashboard
  // crashes don't shut down the main process.
  dashboardQuit = make(chan struct{})
  go func() {
    <-quit
    close(dashboardQuit)
  }()
  lease, err := portal.StartRegistration(*portalAddr, &portal.RegisterRequest{
    Pattern: dashboardUrl,
  }, dashboardQuit)
  if err != nil {
    close(dashboardQuit)
    return dashboardQuit, err
  }

  // Setup the handler
  wantPasswordHash, err = base64.StdEncoding.DecodeString(*passwordHash)
  if err != nil {
    close(dashboardQuit)
    return dashboardQuit, err
  }
  templates, err := template.ParseFS(templatesFS, "*.tmpl.html")
  if err != nil {
    close(dashboardQuit)
    return dashboardQuit, err
  }
  http.Handle(dashboardUrl, &dashboard{children, templates})
  http.Handle(logsUrl, &logStream{children})

  go tools.RunHTTPServer(lease.Port, dashboardQuit)
  return dashboardQuit, nil
}
