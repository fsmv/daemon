package tools

import (
  "os"
  "log"
  "flag"
  "strings"
  "runtime/debug"
)

var (
  version = flag.Bool("version", false, "If set, print version info and exit")
)

// Note: these are processed in lexicographic filename order, so version_flag.go
// is likely to be last
func init() {
  if !flag.Parsed() {
    flag.Parse()
  }
  if !*version {
    return
  }
  buildInfo, ok := debug.ReadBuildInfo()
  if !ok {
    log.Print("Error: No version stamp found.")
    os.Exit(1)
  }
  log.Print("Go version:", buildInfo.GoVersion)
  for _, setting := range buildInfo.Settings {
    if !strings.HasPrefix(setting.Key, "vcs") {
      continue
    }
    log.Printf("%v:\t%v", setting.Key, setting.Value)
  }
  os.Exit(2)
}
