package main

import (
  "fmt"
  "flag"
  "bytes"
  "html"
  "net/http"
  "crypto/sha256"
  "crypto/subtle"
  "path/filepath"
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
)

const (
  dashboardUrl = "/spawn/"
)

type dashboard struct {
  Children Children
  WantPasswordHash []byte
}

func (d *dashboard) ServeHTTP(w http.ResponseWriter, r *http.Request) {
  u, p, ok := r.BasicAuth()
  if !ok {
    w.Header().Set("WWW-Authenticate", `Basic realm="daemon", charset="UTF-8"`)
    http.Error(w, "Unauthorized", http.StatusUnauthorized)
    return
  }
  uh := sha256.Sum256([]byte(u))
  ph := sha256.Sum256([]byte(p))
  userMatch := (1 == subtle.ConstantTimeCompare(uh[:], wantUsernameHash[:]))
  passMatch := (1 == subtle.ConstantTimeCompare(ph[:], d.WantPasswordHash))
  if !(userMatch && passMatch) {
    http.Error(w, "Unauthorized", http.StatusUnauthorized)
    return
  }
  // Auth OK

  d.Children.Lock()
  defer d.Children.Unlock()
  var out bytes.Buffer
  out.WriteString("<html><body><ul>")
  for _, child := range d.Children.ByPID {
    out.WriteString("<li>")
    if child.Up {
      out.WriteString("<span>&nbsp;UP&nbsp;</span>")
    } else {
      out.WriteString("<span color=\"red\">DOWN</span>")
    }
    out.WriteString(fmt.Sprintf("<span>%v</span>", filepath.Base(child.Cmd.Filepath)))
    if !child.Up {
      out.WriteString("<p>")
      out.WriteString(html.EscapeString(child.Message))
      out.WriteString("</p>")
    }
    out.WriteString("</li>")
  }
  out.WriteString("</ul></body></html>")
  w.Write(out.Bytes())
}

func StartDashboard(children Children, quit chan struct{}) (dashboardQuit chan struct{}, err error) {
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
  wantPasswordHash, err := base64.StdEncoding.DecodeString(*passwordHash)
  if err != nil {
    close(dashboardQuit)
    return dashboardQuit, err
  }
  http.Handle(dashboardUrl, &dashboard{children, wantPasswordHash})

  go tools.RunHTTPServer(lease.Port, dashboardQuit)
  return dashboardQuit, nil
}
