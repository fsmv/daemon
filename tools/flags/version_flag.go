package flags

import (
	"flag"
	"fmt"
	"os"
	"runtime/debug"
	"strings"

	"ask.systems/daemon/tools"
)

var (
	// Set this to information about your license and copyright to print in the
	// -version flag results
	//
	// Must be set before calling flag.Parse()
	CopyrightNotice string
)

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
	if CopyrightNotice != "" {
		CopyrightNotice = "\n" + CopyrightNotice
	}
	fmt.Fprintf(out, "%v %v%v\n\n",
		buildInfo.Main.Path, buildInfo.Main.Version, CopyrightNotice)
	format := "%12v:  %v\n"
	fmt.Fprintf(out, format, "Go Version", buildInfo.GoVersion)
	for _, setting := range buildInfo.Settings {
		if !strings.HasPrefix(setting.Key, "vcs.") {
			continue
		}
		fmt.Fprintf(out, format, setting.Key, setting.Value)
	}
	os.Exit(0)
	return nil
}
