// Embedspawn lets you run the spawn binary main function inside another program
//
// This is used by [ask.systems/daemon], but feel free to use it if you want to!
package embedspawn

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
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
	spawningDelay = flags.Duration("spawning_delay", 200*time.Millisecond, ""+
		"The amount of time to wait between starting processes.\n"+
		"Useful especially for portal which should go first and be given time\n"+
		"to start up so others can connect.")
	dontKillChildren = flags.Bool("dont_kill_children", false, ""+
		"When not set, send a SIGHUP to child processes when this process dies. This is\n"+
		"on by default so that it is easy to setup restarting your daemon with an init system.")
	dashboardUrlFlag = flags.String("dashboard_url", "/daemon/", ""+
		"The url to serve the dashboard for this spawn instance. If you have\n"+
		"multiple servers running spawn, they need different URLs.\n"+
		"Slashes are required.")
	adminLogins := flags.String("dashboard_logins", "", ""+
		"A comma separated list of username:password_hash for admins that can access\n"+
		"the dashboard.")
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

	adminAuth := &tools.BasicAuthHandler{Realm: "daemon"}
	logins := strings.Split(*adminLogins, ",")
	for i, login := range logins {
		if err := adminAuth.SetLogin(login); err != nil {
			log.Printf("Failed to authorize login %v: %v", i, err)
		}
	}

	commands, err := ReadConfig(*configFilename)
	if err != nil {
		log.Fatalf("Failed to read config file. error: \"%v\"", err)
	}

	hasChroot := false
	for _, cmd := range commands {
		if !cmd.NoChroot {
			hasChroot = true
			break
		}
	}
	// TODO: we can use buildinfo.Read on the child binaries to check them
	// individually as well. That would be nicer.
	//
	// Technically spawn shouldn't need to be statically linked but on some
	// platforms (linux) apparently os.ProcAttr.Sys.Chroot is ignored when
	// CGO_ENABLED=1. I guess I could just call the syscall.Chroot myself...
	if hasChroot && cgoEnabled() {
		log.Print("Warning: This binary was not compiled with CGO_ENABLED=0 set.\n" +
			"Chroots will not work because the binary is not staticaly linked. All child binaries\n" +
			"without the no_chroot: true setting must be compiled with CGO_ENABLED=0 as well.\n" +
			"To install daemon properly: CGO_ENABLED=0 go install ask.systems/daemon@latest")
	}

	quit := make(chan struct{})
	tools.CloseOnQuitSignals(quit)

	children := newChildren(quit)
	go children.MonitorDeaths(quit)
	if errcnt := children.StartPrograms(commands); errcnt != 0 {
		log.Printf("%v errors occurred in spawning", errcnt)
	}
	if _, err := startDashboard(children, adminAuth, quit); err != nil {
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

// Returns true if this binary was not compiled with CGO_ENABLED=0, which means
// that the binary will not be static linked and won't work in a chroot.
func cgoEnabled() bool {
	buildInfo, ok := debug.ReadBuildInfo()
	if !ok {
		log.Printf("Warning: could not read build tags, assuming CGO_ENABLED=1")
		return true
	}
	for _, setting := range buildInfo.Settings {
		if !strings.HasPrefix(setting.Key, "CGO_ENABLED") {
			continue
		}
		return setting.Value == "1"
	}
	log.Printf("Warning: could not find the build tag, assuming CGO_ENABLED=1")
	return true
}
