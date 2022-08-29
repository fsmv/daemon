package flags

import (
  "io"
  "os"
  "flag"
  "log"
  "log/syslog"
  "path/filepath"
  "strings"
  "errors"

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
  flag.Var(tools.BoolFuncFlag(handleSyslogFlag), "syslog",
    "If set, log to the syslog service in addition to stdout when using the go\n"+
    "log package. Logs under user.info (facility.severity). See also: man syslog.\n"+

    "\nTo use this in a chroot you can setup networking and use the -syslog_remote flag\n"+
    "or configure syslogd with the -l flag to create the <chroot>/dev/log file.")
  flag.Func("syslog_remote",
    "Set to your url or ip for UDP remote logging. Optionally prefix with\n"+
    "tcp:// or any other https://pkg.go.dev/net#Dial supported protocols to connect\n"+
    "to syslog servers that support other protocols. For syslogd configuration, make\n"+
    "sure to use :* in your -a option for example -a 192.168.1.1/24:*\n"+

    "\nIf you're using -syslog_remote, do not set -syslog.",
    handleSyslogFlag)
}

func handleSyslogFlag(value string) error {
  if Syslog != nil {
    return errors.New("Syslog was already loaded, use only one of -syslog or -syslog_remote.")
  }
  log.Print("Loading syslog...")
  var err error
  var network, addr string
  if value != "true" && value != "" {
    network_idx := strings.Index(value, "://")
    if network_idx != -1 {
      network = value[:network_idx]
      addr = value[network_idx+3:]
    } else {
      network = "udp"
      addr = value
      if strings.Index(addr, ":") == -1 {
        addr += ":514" // default syslog port
      }
    }
  }
  // if network and addr are empty it tries using the system files
  Syslog, err = syslog.Dial(network, addr,
    syslog.LOG_INFO | syslog.LOG_USER, filepath.Base(os.Args[0]))
  if err != nil {
    return err
  }
  log.SetFlags(0) // Don't add timestamps because syslog adds them
  log.SetOutput(io.MultiWriter(Syslog,
    tools.NewTimestampWriter(os.Stdout))) // But still print timestamps
  return nil
}
