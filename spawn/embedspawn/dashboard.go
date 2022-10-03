package embedspawn

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"time"

	_ "ask.systems/daemon/portal/flags"

	"ask.systems/daemon/portal/gate"
	"ask.systems/daemon/tools"
)

const javascriptStreamDelay = 4 * time.Millisecond

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
}

func (d *dashboard) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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

func startDashboard(children *children, quit chan struct{}) (dashboardQuit chan struct{}, err error) {
	pattern := *dashboardUrlFlag
	_, dashboardUrl = gate.ParsePattern(pattern)
	logsUrl = dashboardUrl + "logs"

	// If the main  quit closes, shut down the dashboard. But, if the dashboard
	// crashes don't shut down the main process.
	dashboardQuit = make(chan struct{})
	go func() {
		<-quit
		close(dashboardQuit)
	}()
	lease, tlsConf, err := gate.StartTLSRegistration(&gate.RegisterRequest{
		Pattern:   pattern,
		AdminOnly: true,
	}, dashboardQuit)
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

	go tools.RunHTTPServerTLS(lease.Port, tlsConf, dashboardQuit)
	return dashboardQuit, nil
}
