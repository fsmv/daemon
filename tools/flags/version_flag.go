package flags

import (
	"flag"
	"log"
	"os"
	"runtime/debug"
	"strings"

	"ask.systems/daemon/tools"
)

func init() {
	flag.Var(tools.BoolFuncFlag(handleVersionFlag), "version",
		"If set, print version info and exit")
}

func handleVersionFlag(value string) error {
	buildInfo, ok := debug.ReadBuildInfo()
	if !ok {
		log.Print("Error: No version stamp found.")
		os.Exit(1)
	}
	log.Print("Compiled with:    ", buildInfo.GoVersion)
	for _, setting := range buildInfo.Settings {
		if !strings.HasPrefix(setting.Key, "vcs") {
			continue
		}
		log.Printf("%13v:    %v", setting.Key, setting.Value)
	}
	os.Exit(2)
	return nil
}
