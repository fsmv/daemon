package embedspawn

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	_ "embed"

	"ask.systems/daemon/tools"
	_ "ask.systems/daemon/tools/flags"

	"google.golang.org/protobuf/encoding/prototext"
)

//go:embed config.proto
var configSchema string

//go:generate protoc -I ../ ../embedspawn/config.proto --go_out ../ --go_opt=paths=source_relative

var (
	configFilename   *string
	path             *string
	spawningDelay    *time.Duration
	dontKillChildren *bool
)

func Run(flags *flag.FlagSet, args []string) {
	configFilename = flags.String("config", "config.pbtxt",
		"The path to the config file")
	path = flags.String("path", "",
		"A single path to use for relative paths in the config file")
	spawningDelay = flags.Duration("spawning_delay", 2*time.Second, ""+
		"The amount of time to wait between starting processes.\n"+
		"Useful especially for feproxy which should go first and be given time\n"+
		"to start up so others can connect.")
	dontKillChildren = flags.Bool("dont_kill_children", false, ""+
		"When not set, send a SIGHUP to child processes when this process dies. This is\n"+
		"on by default so that it is easy to setup restarting your daemon with an init system.")
	passwordHash = flags.String("password_hash", "set me",
		"sha256sum hash of the 'admin' user's basic auth password.")
	dashboardUrlFlag = flags.String("dashboard_url", "/daemon/", ""+
		"The url to serve the dashboard for this spawn instance. If you have\n"+
		"multiple servers running spawn, they need different URLs.\n"+
		"Slashes are required.")
	flags.Var(
		tools.BoolFuncFlag(func(string) error {
			fmt.Print(configSchema)
			os.Exit(2)
			return nil
		}),
		"config_schema",
		"Print the config schema in proto format, for reference, and exit.",
	)
	flags.Parse(args[1:])

	commands, err := ReadConfig(*configFilename)
	if err != nil {
		log.Fatalf("Failed to read config file. error: \"%v\"", err)
	}

	quit := make(chan struct{})
	tools.CloseOnQuitSignals(quit)

	children := newChildren(quit)
	go children.MonitorDeaths(quit)
	// Mutex to make the death message handler wait for data about the children
	if errcnt := children.StartPrograms(commands); errcnt != 0 {
		log.Printf("%v errors occurred in spawning", errcnt)
	}
	if _, err := startDashboard(children, quit); err != nil {
		log.Print("Failed to start dashboard: ", err)
		// TODO: retry it? Also check the dashboardQuit signal for retries
	}

	<-quit
	log.Print("Goodbye.")
}

func (cmd *Command) FullName() string {
	name := filepath.Base(cmd.Binary)
	if cmd.Name != "" {
		name = fmt.Sprintf("%v-%v", name, cmd.Name)
	}
	return name
}

func resolveRelativePaths(path string, commands []*Command) error {
	for i, _ := range commands {
		cmd := commands[i]
		if len(cmd.Binary) == 0 || cmd.Binary[0] == '/' {
			continue
		}
		if path == "" {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("Failed to get current working directory: %w", err)
			}
			path = cwd
		}
		cmd.Binary = filepath.Join(path, cmd.Binary)
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
	err = resolveRelativePaths(*path, config.Command)
	return config.Command, err
}
