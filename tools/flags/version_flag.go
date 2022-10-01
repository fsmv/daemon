/*
Import this package to get useful flags every server should have

	import (
		_ "ask.systems/daemon/tools/flags
	)

Provides:

  - -version which prints the module name, version and the [CopyrightNotice],
    and for development builds prints version control information.
  - -syslog and -syslog_remote which enable directly sending [log] package
    logs to the syslogd service directly for collecting all your logs
    together.

If you'd like to have the -version flag but exclude the syslog flags then you
can compile your binary with:

	CGO_ENABLED=0 go build -tags nosyslog
*/
package flags

import (
	"flag"
	"fmt"
	"os"
	"runtime/debug"
	"strings"

	"ask.systems/daemon/tools"
)

// Set this to information about your license and copyright to print in the
// -version flag results
//
// Must be set before calling [flag.Parse]
var CopyrightNotice string

func init() {
	flag.Var(tools.BoolFuncFlag(handleVersionFlag), "version",
		"If set, print version info and exit")
}

func handleVersionFlag(value string) error {
	out := flag.CommandLine.Output()
	buildInfo, ok := debug.ReadBuildInfo()
	if !ok {
		fmt.Fprintln(out, "Failed to read build info.")
		os.Exit(1)
	}

	// Print the main version info with copyright notice
	if CopyrightNotice != "" {
		CopyrightNotice = "\n" + CopyrightNotice
	}
	fmt.Fprintf(out, "%v %v\tCompiler: %v%v\n",
		buildInfo.Path, buildInfo.Main.Version, buildInfo.GoVersion, CopyrightNotice)

	// Print the version control info if it's available with nice formatting
	maxLen := 0 // find the maximum key length to pad the spaces correctly
	const prefix = "vcs."
	for _, setting := range buildInfo.Settings {
		if !strings.HasPrefix(setting.Key, prefix) {
			continue
		}
		if l := len(setting.Key); l > maxLen {
			maxLen = l
		}
	}
	if maxLen > 0 {
		fmt.Fprintf(out, "\n")
	}
	format := fmt.Sprintf("  %%%dv:  %%v\n", maxLen-len(prefix))
	for _, setting := range buildInfo.Settings {
		if !strings.HasPrefix(setting.Key, prefix) {
			continue
		}
		fmt.Fprintf(out, format, setting.Key[len(prefix):], setting.Value)
	}

	os.Exit(0)
	return nil
}
