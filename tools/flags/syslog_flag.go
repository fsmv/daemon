//go:build !nosyslog && !windows

package flags

import (
	"errors"
	"flag"
	"io"
	"log"
	"log/syslog"
	"os"
	"path/filepath"
	"strings"

	"ask.systems/daemon/tools"
)

// Use this if you want to use the log severity methods. This is only
// initialized if the -syslog or the -syslog_remote flag is set and [flag.Parse]
// has been called.
//
// Using this writer directly will not also log to stdout
var Syslog *syslog.Writer

func init() {
	flag.Var(tools.BoolFuncFlag(handleSyslogFlag), "syslog", ""+
		"If set, log to the syslog service in addition to stdout when using the go\n"+
		"log package. Logs under user.info (facility.severity). See also: man syslog.\n\n"+

		"To use this in a chroot you can setup networking and use the -syslog_remote\n"+
		"flag or configure syslogd with the -l flag to create the <chroot>/dev/log\n"+
		"file.")
	flag.Func("syslog_remote", ""+
		"Set to your URL or IP for UDP remote logging. Optionally prefix with\n"+
		"tcp:// or any other https://pkg.go.dev/net#Dial supported protocols to\n"+
		"connect to syslog servers that support other protocols. For syslogd\n"+
		"configuration, make sure to use :* in your -a option\n"+
		"for example -a 192.168.1.1/24:*\n\n"+

		"Warning: the go syslog package does not support using TLS so these logs are\n"+
		"not encrypted in transit. So it is only recommended to use this locally for\n"+
		"servers in chroots. You can then use the syslog service to send to another\n"+
		"server securely.\n\n"+

		"If you're using -syslog_remote then do not also set -syslog;\n"+
		"it is undefined which one wins the race.",
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
		syslog.LOG_INFO|syslog.LOG_USER, filepath.Base(os.Args[0]))
	if err != nil {
		return err
	}
	log.SetFlags(0) // Don't add timestamps because syslog adds them
	writer := io.MultiWriter(Syslog,
		tools.NewTimestampWriter(os.Stdout))
	log.SetOutput(writer)    // But still print timestamps
	writeVersionInfo(writer) // Print the version info always
	return nil
}
