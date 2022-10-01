package tools

import (
	"io"
	"time"
)

func init() {
	// Force load the /etc/localtime file before spawn deletes it for chroots.
	// Not really related to TimestampWriter but I wanted it somewhere in tools.
	_ = time.Local.String()
}

// Wraps an [io.Writer] and prepends a timestamp from [time.Now] to each [Write]
// call.
type TimestampWriter struct {
	io.Writer
	// Don't forget to include whitespace at the end to separate the message
	TimeFormat string
}

// Create a [TimestampWriter] with the default time format (which matches the
// [log] package default format)
func NewTimestampWriter(w io.Writer) *TimestampWriter {
	return &TimestampWriter{w, "2006/01/02 15:04:05 "}
}

// See [io.Writer]
func (w *TimestampWriter) Write(in []byte) (int, error) {
	// Use a stack buffer if the format size is small enough
	// Copied from https://cs.opensource.google/go/go/+/refs/tags/go1.18.1:src/time/format.go;l=587;drc=293ecd87c10eb5eed777d220394ed63a935b2c20
	const bufSize = 64
	var b []byte
	if max := len(w.TimeFormat) + 10; max < bufSize {
		var buf [bufSize]byte
		b = buf[:0]
	} else {
		b = make([]byte, 0, max)
	}

	// Use AppendFormat to avoid having the time package []byte converted to
	// string then immediately back to []byte
	b = time.Now().AppendFormat(b, w.TimeFormat)
	n1, err := w.Writer.Write(b)
	if err != nil {
		return n1, err
	}
	// Do two Write calls to avoid copying the input
	// Return just the n from this write not everything we wrote because
	// MultiWriter checks if the output size is the same as the input size:
	// https://cs.opensource.google/go/go/+/refs/tags/go1.18.1:src/io/multi.go;l=64;drc=112f28defcbd8f48de83f4502093ac97149b4da6
	return w.Writer.Write(in)
}
