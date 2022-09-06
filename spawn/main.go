package main

import (
  "os"
  "fmt"
  "log"
  "flag"
  "time"
  "path/filepath"

  _ "embed"
  _ "ask.systems/daemon/tools/flags"

  "ask.systems/daemon/tools"
  "google.golang.org/protobuf/encoding/prototext"
)

const (
  kLogLinesBufferSize = 256 // Per tag
  kSubscriptionChannelSize = 5*kLogLinesBufferSize
  kPublishChannelSize = 32
)

var (
  configFilename = flag.String("config", "config.pbtxt",
    "The path to the config file")
  path = flag.String("path", "",
    "A single path to use for relative paths in the config file")
  spawningDelay = flag.Duration("spawning_delay", 2*time.Second,
    "The amount of time to wait between starting processes.\n" +
    "Useful especially for feproxy which should go first and be given time\n" +
    "to start up so others can connect.")
)

//go:embed config.proto
var configSchema string
//go:generate protoc config.proto --go_out ./ --go_opt=paths=source_relative

func init() {
  flag.Var(tools.BoolFuncFlag(func(string) error {
      fmt.Print(configSchema)
      os.Exit(2)
      return nil
    }), "config_schema",
    "Print the config schema in proto format, for reference, and exit.")
}

func (cmd *Command) FullName() string {
  name := filepath.Base(cmd.Filepath)
  if cmd.Name != "" {
    name = fmt.Sprintf("%v-%v", name, cmd.Name)
  }
  return name
}

func ResolveRelativePaths(path string, commands []*Command) error {
    for i, _ := range commands {
        cmd := commands[i]
        if len(cmd.Filepath) == 0 || cmd.Filepath[0] == '/' {
            continue
        }
        if len(path) == 0 { // Don't error unless there's actually a go path
            return fmt.Errorf(
                "--path flag not set which is required by Command #%v, " +
                "filepath: %v", i, cmd.Filepath)
        }
        cmd.Filepath = filepath.Join(path, cmd.Filepath)
    }
    return nil
}

func ReadConfig(filename string) ([]*Command, error) {
  configText, err := os.ReadFile(filename)
  if err != nil {
    return nil, err
  }
  config := &Config{}
  if err := prototext.Unmarshal(configText, config); err != nil {
    return nil, err
  }
  err = ResolveRelativePaths(*path, config.Command)
  return config.Command, err
}

func main() {
    flag.Parse()
    commands, err := ReadConfig(*configFilename)
    if err != nil {
        log.Fatalf("Failed to read config file. error: \"%v\"", err)
    }

    quit := make(chan struct{})
    tools.CloseOnSignals(quit)

    children := NewChildren(quit)
    go children.MonitorDeaths(quit)
    // Mutex to make the death message handler wait for data about the children
    if errcnt := children.StartPrograms(commands); errcnt != 0 {
        log.Printf("%v errors occurred in spawning", errcnt)
    }
    if _, err := StartDashboard(children, quit); err != nil {
        log.Print("Failed to start dashboard: ", err)
        // TODO: retry it? Also check the dashboardQuit signal for retries
    }

    <-quit
    log.Print("Goodbye.")
}
