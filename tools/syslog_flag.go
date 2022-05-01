package tools

import (
  "log"
  "log/syslog"
  "flag"
  "time"
  "io"
  "os"
  "path/filepath"
)

var (
  // Use this if you want to use the log severity methods. This is only
  // initialized if the syslogTag flag is set and flag.Parse() has been called.
  //
  // Using this writer directly will not also log to stdout
  Syslog *syslog.Writer
)

func init () {
  flag.Var(BoolFunc(handleSyslogFlag), "use_syslog",
    "If set, log to the syslog service in addition to stdout when using the go\n"+
    "log package. Logs under user.info (facility.severity). See also: man syslog.\n"+
    "To use this in a chroot configure syslogd with the -l flag to create the <chroot>/dev/log file.")
}

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
  b = time.Now().AppendFormat(b, w.TimeFormat)
  n1, err := w.Writer.Write(b)
  if err != nil {
    return n1, err
  }
  // Do two Write calls to avoid copying the input
  n2, err := w.Writer.Write(in)
  return n1+n2, err
}

func handleSyslogFlag(value string) error {
  var err error
  // TODO: it would be nice to read the flag value as an address to optionally
  // do remote logging
  Syslog, err = syslog.New(syslog.LOG_INFO | syslog.LOG_USER, filepath.Base(os.Args[0]))
  if err != nil {
    return err
  }
  log.SetFlags(0) // Just use the syslog built in timestamp
  log.SetOutput(io.MultiWriter(
    &timestampWriter{os.Stdout, "2006/01/02 15:04:05 "},
    Syslog))
  return nil
}
