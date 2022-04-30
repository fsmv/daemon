package tools

import (
  "log"
  "log/syslog"
  "flag"
  "time"
  "io"
  "os"
)

var (
  useSyslog = flag.Bool("use_syslog", false,
    "If set, log to the syslog service in addition to stdout when using the go log package. "+
    "Logs under user.info (facility.severity). See man syslog.")

  // Use this if you want to use the log severity methods. This is only
  // initialized if the syslogTag flag is set.
  //
  // Using this writer directly will not also log to stdout
  Syslog *syslog.Writer
)

type timestampWriter struct {
    io.Writer
    // Don't forget to include whitespace at the end to separate the message
    TimeFormat string
}

func (w *timestampWriter) Write(in []byte) (int, error) {
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
  time.Now().AppendFormat(b, w.TimeFormat)
  n1, err := w.Writer.Write(b)
  if err != nil {
    return n1, err
  }
  // Do two Write calls to avoid copying the input
  n2, err := w.Writer.Write(in)
  return n1+n2, err
}

func init () {
  if !flag.Parsed() {
    flag.Parse()
  }
  if !*useSyslog {
    return
  }
  var err error
  Syslog, err = syslog.New(syslog.LOG_INFO | syslog.LOG_USER, os.Args[0])
  if err != nil {
    log.Print("Failed to create syslog writer:", err)
  }
  log.SetFlags(0) // Just use the syslog built in timestamp
  log.SetOutput(io.MultiWriter(
    &timestampWriter{os.Stdout, "2006/01/02 15:04:05 "},
    Syslog))
}
