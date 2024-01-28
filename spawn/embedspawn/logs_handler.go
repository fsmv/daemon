package embedspawn

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"strings"

	"ask.systems/daemon/tools/flags"
)

const (
	kLogLinesBufferSize      = 256 // Per tag
	kSubscriptionChannelSize = 5 * kLogLinesBufferSize
	kPublishChannelSize      = 32
	// The tag to use for Stdout of this binary
	kLogsTag = "spawn"
)

type subscribeRequest struct {
	sub            chan<- logMessage
	includeHistory bool
}

type logHandler struct {
	logLines map[string]*ringBuffer
	// Broadcasting system
	quit        chan struct{}
	publish     chan logMessage
	subscribe   chan subscribeRequest
	history     chan chan<- map[string]string
	subscribers map[chan<- logMessage]struct{}
}

func newLogHandler(quit chan struct{}) *logHandler {
	h := &logHandler{
		quit:        quit,
		logLines:    make(map[string]*ringBuffer),
		subscribers: make(map[chan<- logMessage]struct{}),
		publish:     make(chan logMessage, kPublishChannelSize),
		subscribe:   make(chan subscribeRequest),
		history:     make(chan chan<- map[string]string),
	}
	go h.run()
	return h
}

// Broadcasts all the lines sent to the publish channel from HandleLogs to all
// of the subscribers
func (h *logHandler) run() {
	for {
		select {
		case <-h.quit:
			return
		case req := <-h.subscribe:
			// New subscribers get the history buffer
			//
			// TODO: if we send id: int after the data in the SSE stream then after
			// reconnecting it will send a Last-Event-ID header so we can restart
			// from the place we stopped at
			if req.includeHistory {
				for tag, log := range h.logLines {
					log.Write(req.sub, tag)
				}
			}

			if _, ok := h.subscribers[req.sub]; ok {
				delete(h.subscribers, req.sub)
				close(req.sub)
			} else {
				h.subscribers[req.sub] = struct{}{}
			}
		case out := <-h.history:
			ret := make(map[string]string)
			for tag, ring := range h.logLines {
				ret[tag] = ring.Dump()
			}
			out <- ret
			close(out)
		case m := <-h.publish:
			// Make a new ring buffer if needed and push to the buffer
			log, ok := h.logLines[m.Tag]
			if !ok {
				log = &ringBuffer{}
				h.logLines[m.Tag] = log
			}
			log.Push(m.Line)

			// Push to subscribers
			for sub, _ := range h.subscribers {
				// Non-blocking send in case a client somehow blocked and ran out of
				// buffer so we don't lock up all the other clients. Realistically this
				// will never happen.
				select {
				case sub <- m:
				default:
				}
			}
		}
	}
}

// Publish a logs file or pipe to all of the subscribers of the handler
func (h *logHandler) HandleLogs(logs io.ReadCloser, tag string) {
	panicLog := h.panicHandleLogs(logs, tag)
	if panicLog != "" && flags.Syslog != nil {
		// Note: this only needs to go to syslog because the panic is already
		// printed to stdout and displayed on the dashboard.
		//
		// It's only syslog that misses it because spawn doesn't normally syslog for
		// children, they syslog on their own.
		io.WriteString(flags.Syslog, panicLog)
	}
}

// Publish a logs file or pipe to all of the subscribers of the handler
//
// If a panic was detected it returns the panic logs, otherwise it returns empty
// string.
func (h *logHandler) panicHandleLogs(logs io.ReadCloser, tag string) string {
	defer logs.Close()
	r := bufio.NewReader(logs)
	var panicLog strings.Builder
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			log.Print("Failed reading logs: ", err)
			return panicLog.String()
		}

		// Capture panic logs so spawn can send them over syslog
		//
		// The children can't automatically capture and syslog all panics because
		// you would have to have a defer func in main() which I can't magically add
		// with an include. So spawn has to do it.
		if strings.HasPrefix(line, "panic:") { // don't log this!
			log.Printf("Panic detected in %v!", tag)
			panicLog.Reset()
			panicLog.WriteString(tag)
			panicLog.WriteString(" ")
			// The if statement below writes the first line of the panic
		}
		if panicLog.Len() > 8*1024*1024 /* 8 mb */ {
			log.Printf("False positive panic in %v? Giving up on capturing since it's over 8mb.", tag)
			panicLog.Reset()
		}
		if panicLog.Len() > 0 { // After we see the start take the rest until crash
			panicLog.WriteString(line)
		}

		// For running spawn on commandline and not using syslog, print all the
		// child logs. Also don't print spawn's logs here because the original log
		// writer is already printing it, which we want to use so syslog works.
		if tag != kLogsTag {
			// Use fmt instead of log so we don't syslog the client's logs
			fmt.Printf("%v: %v", tag, line)
		}
		h.publish <- logMessage{Line: line, Tag: tag}
		select {
		case <-h.quit:
			return panicLog.String()
		default:
		}
	}
	return panicLog.String()
}

func (h *logHandler) StreamLogs(includeHistory bool) (stream <-chan logMessage, cancel func()) {
	sub := make(chan logMessage, kSubscriptionChannelSize)
	req := subscribeRequest{
		sub:            sub,
		includeHistory: includeHistory,
	}
	h.subscribe <- req
	return sub, func() {
		h.subscribe <- req
	}
}

func (h *logHandler) DumpLogs() map[string]string {
	result := make(chan map[string]string, 1)
	h.history <- result
	return <-result
}

type logMessage struct {
	Line string
	Tag  string
}

// Not thread safe
type ringBuffer struct {
	buffer   [kLogLinesBufferSize]string
	nextLine int
	filled   bool
}

func (r *ringBuffer) Push(line string) {
	r.buffer[r.nextLine] = line
	r.nextLine++
	if r.nextLine == len(r.buffer) {
		r.filled = true
		r.nextLine = 0
	}
}

func (r *ringBuffer) Dump() string {
	var out strings.Builder
	if r.filled {
		for _, line := range r.buffer[r.nextLine:] {
			out.WriteString(line)
		}
	}
	for _, line := range r.buffer[:r.nextLine] {
		out.WriteString(line)
	}
	return out.String()
}

// Not thread safe, simultaneous push will break it
func (r *ringBuffer) Write(out chan<- logMessage, tag string) {
	if r.filled {
		for _, line := range r.buffer[r.nextLine:] {
			out <- logMessage{Line: line, Tag: tag}
		}
	}
	for _, line := range r.buffer[:r.nextLine] {
		out <- logMessage{Line: line, Tag: tag}
	}
}
