package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
)

type logHandler struct {
	logLines map[string]*ringBuffer
	// Broadcasting system
	quit        chan struct{}
	publish     chan LogMessage
	subscribe   chan chan<- LogMessage
	subscribers map[chan<- LogMessage]struct{}
}

func NewLogHandler(quit chan struct{}) *logHandler {
	h := &logHandler{
		quit:        quit,
		logLines:    make(map[string]*ringBuffer),
		subscribers: make(map[chan<- LogMessage]struct{}),
		publish:     make(chan LogMessage, kPublishChannelSize),
		subscribe:   make(chan chan<- LogMessage),
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
				// TODO: maybe timeout or non-blocking with select default
				//   - if we send id: int after the data in the SSE stream then after
				//     reconnecting it will send a Last-Event-ID header so we can
				//     restart from the place we stopped at
				sub <- m
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
		fmt.Printf("%v: %v", tag, line) // for running spawn on commandline and not using syslog
		h.publish <- LogMessage{Line: line, Tag: tag}
		select {
		case <-h.quit:
			return
		default:
		}
	}
}

func (h *logHandler) StreamLogs() (stream <-chan LogMessage, cancel func()) {
	sub := make(chan LogMessage, kSubscriptionChannelSize)
	h.subscribe <- sub
	return sub, func() {
		h.subscribe <- sub
		close(sub)
	}
}

type LogMessage struct {
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
func (r *ringBuffer) Write(out chan<- LogMessage, tag string) {
	if r.filled {
		for _, line := range r.buffer[r.nextLine:] {
			out <- LogMessage{Line: line, Tag: tag}
		}
	}
	for _, line := range r.buffer[:r.nextLine] {
		out <- LogMessage{Line: line, Tag: tag}
	}
}
