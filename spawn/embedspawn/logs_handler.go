package embedspawn

import (
	"bufio"
	"fmt"
	"io"
	"log"
)

const (
	kLogLinesBufferSize      = 256 // Per tag
	kSubscriptionChannelSize = 5 * kLogLinesBufferSize
	kPublishChannelSize      = 32
	// The tag to use for Stdout of this binary
	kLogsTag = "spawn"
)

type logHandler struct {
	logLines map[string]*ringBuffer
	// Broadcasting system
	quit        chan struct{}
	publish     chan logMessage
	subscribe   chan chan<- logMessage
	subscribers map[chan<- logMessage]struct{}
}

func newLogHandler(quit chan struct{}) *logHandler {
	h := &logHandler{
		quit:        quit,
		logLines:    make(map[string]*ringBuffer),
		subscribers: make(map[chan<- logMessage]struct{}),
		publish:     make(chan logMessage, kPublishChannelSize),
		subscribe:   make(chan chan<- logMessage),
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
		case sub := <-h.subscribe:
			if _, ok := h.subscribers[sub]; ok {
				delete(h.subscribers, sub)
			} else {
				// New subscribers get the history buffer
				//
				// TODO: if we send id: int after the data in the SSE stream then after
				// reconnecting it will send a Last-Event-ID header so we can restart
				// from the place we stopped at
				for tag, log := range h.logLines {
					log.Write(sub, tag)
				}

				h.subscribers[sub] = struct{}{}
			}
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
	defer logs.Close()
	r := bufio.NewReader(logs)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			log.Print("Failed reading logs: ", err)
			return
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
			return
		default:
		}
	}
}

func (h *logHandler) StreamLogs() (stream <-chan logMessage, cancel func()) {
	sub := make(chan logMessage, kSubscriptionChannelSize)
	h.subscribe <- sub
	return sub, func() {
		h.subscribe <- sub
		close(sub)
	}
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
