package embedspawn

import (
	"bufio"
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"runtime/debug"
	"time"

	_ "ask.systems/daemon/portal/flags"

	"ask.systems/daemon/portal/gate"
	"ask.systems/daemon/tools"
)

const javascriptStreamDelay = 8 * time.Millisecond

var (
	//go:embed *.tmpl.html
	templatesFS embed.FS

	dashboardUrlFlag *string

	// Setup in StartDashboard
	dashboardUrl string
	logsUrl      string
)

type logStream struct {
	Children *children
}

func (l *logStream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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

	// Give javascript some time to set up the event listeners, seriously
	time.Sleep(javascriptStreamDelay)
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
	Children *children

	templates *template.Template
	adminAuth *tools.BasicAuthHandler
}

func (d *dashboard) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !d.adminAuth.Check(w, r) {
		if user, _, ok := r.BasicAuth(); ok {
			log.Printf("%v failed authentication for %v on %v%v", r.RemoteAddr, user, r.Host, r.URL.Path)
		}
		return
	}

	if r.Method == "POST" {
		err := r.ParseForm()
		if err != nil {
			log.Print("Recieved invaid form data: ", err)
			http.Error(w, "Invalid form data", http.StatusBadRequest)
			return
		}
		switch r.Form.Get("submit") {
		case "restart":
			name := r.Form.Get("name")
			log.Print("Restart request for ", name)
			go d.Children.RestartChild(name)
		case "reload-config":
			log.Print("Reloading config")
			go d.Children.ReloadConfig()
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

func startDashboard(children *children, adminAuth *tools.BasicAuthHandler, quit chan struct{}) (dashboardQuit chan struct{}, err error) {
	pattern := *dashboardUrlFlag
	_, dashboardUrl = gate.ParsePattern(pattern)
	logsUrl = dashboardUrl + "logs"

	// If the main  quit closes, shut down the dashboard. But, if the dashboard
	// crashes don't shut down the main process.
	dashboardQuit = make(chan struct{})
	go func() {
		select {
		case <-quit:
			close(dashboardQuit)
		case <-dashboardQuit: // If it gets closed, don't close it again
		}
	}()
	lease, tlsConf, err := gate.StartTLSRegistration(&gate.RegisterRequest{
		Pattern: pattern,
	}, dashboardQuit)
	if err != nil {
		close(dashboardQuit)
		return dashboardQuit, err
	}

	templates := template.New("templates")
	templates = templates.Funcs(map[string]interface{}{
		"VersionInfo": versionInfo,
	})
	templates, err = templates.ParseFS(templatesFS, "*.tmpl.html")
	if err != nil {
		close(dashboardQuit)
		return dashboardQuit, err
	}
	http.Handle(dashboardUrl, &dashboard{children, templates, adminAuth})
	http.Handle(logsUrl, &logStream{children})

	go tools.RunHTTPServerTLS(lease.Port, tlsConf, dashboardQuit)
	return dashboardQuit, nil
}

type versionResult struct {
	Version       string
	UpdateVersion string
}

// Note: go templates do not allow multiple return values from functions, so we
// have to use a struct
func versionInfo() versionResult {
	buildInfo, ok := debug.ReadBuildInfo()
	if !ok {
		log.Print("Failed to read build info.")
		return versionResult{"", ""}
	}
	version := buildInfo.Main.Version
	if version == "(devel)" {
		// TODO: it would be nice to print some of the extra info that's in the
		// -version flag like return the revision hash here
		return versionResult{"development version", ""}
	}

	// TODO: make this into a helper function in tools when I'm sure about the API
	latestVersion := ""
	{
		resp, err := http.Get("https://proxy.golang.org/ask.systems/daemon/@v/list")
		if err != nil || resp.StatusCode != http.StatusOK {
			log.Print("Failed to fetch the latest version from GOPROXY: %v %v", resp.Status, err)
			return versionResult{version, ""}
		}
		defer resp.Body.Close()
		scanner := bufio.NewScanner(resp.Body)
		if scanner.Scan() {
			latestVersion = scanner.Text()
		}
	}

	if latestVersion > version {
		return versionResult{
			Version:       version,
			UpdateVersion: latestVersion,
		}
	}
	return versionResult{version, ""}
}
