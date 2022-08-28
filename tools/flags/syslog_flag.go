package flags

import (
  "io"
  "os"
  "flag"
  "log"
  "log/syslog"
  "path/filepath"

  "ask.systems/daemon/tools"
)

var (
  // Use this if you want to use the log severity methods. This is only
  // initialized if the syslogTag flag is set and flag.Parse() has been called.
  //
  // Using this writer directly will not also log to stdout
  Syslog *syslog.Writer
)

func init () {
  flag.Var(tools.BoolFuncFlag(handleSyslogFlag), "use_syslog",
    "If set, log to the syslog service in addition to stdout when using the go\n"+
    "log package. Logs under user.info (facility.severity). See also: man syslog.\n"+
    "To use this in a chroot configure syslogd with the -l flag to create the <chroot>/dev/log file.")
}

func handleSyslogFlag(value string) error {
  log.Print("Loading syslog...")
  var err error
  // TODO: it would be nice to read the flag value as an address to optionally
  // do remote logging
  Syslog, err = syslog.New(syslog.LOG_INFO | syslog.LOG_USER, filepath.Base(os.Args[0]))
  if err != nil {
    return err
  }
  log.SetFlags(0) // Just use the syslog built in timestamp
  log.SetOutput(io.MultiWriter(Syslog,
    tools.NewTimestampWriter(os.Stdout)))
  return nil
}
