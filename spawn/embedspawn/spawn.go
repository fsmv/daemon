// Embedspawn lets you run the spawn binary main function inside another program
//
// This is used by [ask.systems/daemon], but feel free to use it if you want to!
package embedspawn

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	_ "embed"

	"ask.systems/daemon/tools"
	"ask.systems/daemon/tools/flags"

	"google.golang.org/protobuf/encoding/prototext"
)

//go:embed config.proto
var configSchema string

//go:embed example_config.pbtxt
var exampleConfig []byte

//go:generate protoc -I ../ ../embedspawn/config.proto --go_out ../ --go_opt=paths=source_relative

var (
	configFilename   *string
	searchPath       *string
	spawningDelay    *time.Duration
	dontKillChildren *bool
)

func Run(flagset *flag.FlagSet, args []string) {
	configFilename = flagset.String("config", "config.pbtxt",
		"The path to the config file")
	searchPath = flagset.String("path", "",
		"A single path to use for relative paths in the config file")
	spawningDelay = flagset.Duration("spawning_delay", 200*time.Millisecond, ""+
		"The amount of time to wait between starting processes.\n"+
		"Useful especially for portal which should go first and be given time\n"+
		"to start up so others can connect.")
	dontKillChildren = flagset.Bool("dont_kill_children", false, ""+
		"When not set, send a SIGHUP to child processes when this process dies. This\n"+
		"is on by default so that it is easy to setup restarting your daemon with an\n"+
		"init system.")
	dashboardUrlFlag = flagset.String("dashboard_url", "/daemon/", ""+
		"The url to serve the dashboard for this spawn instance. If you have\n"+
		"multiple servers running spawn, they need different URLs.\n"+
		"Slashes are required.")
	adminLogins := flagset.String("dashboard_logins", "", ""+
		"A comma separated list of username:password_hash for admins that can access\n"+
		"the dashboard.")
	flagset.Var(
		tools.BoolFuncFlag(func(string) error {
			fmt.Print(configSchema)
			os.Exit(2)
			return nil
		}),
		"config_schema",
		"Print the config schema in proto format, for reference, and exit.",
	)
	flagset.Var(
		tools.BoolFuncFlag(func(string) error {
			fmt.Print(string(exampleConfig))
			os.Exit(2)
			return nil
		}),
		"example_config",
		"Print the example config.pbtxt and exit.",
	)
	flagset.Parse(args[1:])

	// Capture panics in spawn and log them in syslog and then just crash
	defer func() {
		if value := recover(); value != nil {
			if flags.Syslog != nil {
				var panicOut strings.Builder
				panicOut.WriteString(kLogsTag)
				panicOut.WriteString(" panic: ")
				fmt.Fprint(&panicOut, value, "\n\n")
				panicOut.Write(debug.Stack())
				io.WriteString(flags.Syslog, panicOut.String())
			}
			panic(value)
		}
	}()

	usedExampleConf := false
	commands, conferr := ReadConfig(*configFilename)
	if conferr != nil {
		if errors.Is(conferr, fs.ErrNotExist) {
			log.Printf("Writing the example config to %v", *configFilename)
			err := os.WriteFile(*configFilename, exampleConfig, 0640)
			if err != nil {
				log.Printf("Failed to write example config at %v: %v",
					*configFilename, err)
				log.Print("Continuing with the example config in memory only")
			}
			commands, conferr = loadConfig(exampleConfig)
			usedExampleConf = true
		}
		if conferr != nil {
			log.Fatalf("Failed to read config file. error: \"%v\"", conferr)
		}
	}

	quit := make(chan struct{})
	tools.CloseOnQuitSignals(quit)

	children := newChildren(quit)
	go children.MonitorDeaths(quit)
	if errcnt := children.StartPrograms(commands); errcnt != 0 {
		log.Printf("%v errors occurred in spawning", errcnt)
	}

	// Give portal time to start up if portal was the only child to start
	time.Sleep(*spawningDelay)

	adminAuth := &tools.BasicAuthHandler{Realm: "daemon"}
	var logins []string
	if *adminLogins == "" {
		log.Print("-dashboard_logins not set, prompting on stdin for a temporary password to hash.")
		fmt.Printf("Temporary password to login on the dashboard with: ")
		scan := bufio.NewScanner(os.Stdin)
		scan.Scan()
		if err := scan.Err(); err != nil {
			log.Printf("Failed to read stdin: %v", err)
			log.Println("You won't be able to log into the dashboard.")
		} else {
			hash := tools.HashPassword(scan.Text())
			fmt.Println("You can login on the dashboard with username admin and the password you entered.")
			login := fmt.Sprintf("admin:%v", hash)
			fmt.Printf("To keep these settings set -dashboard_logins '%v'\n", login)
			logins = append(logins, login)
			log.Print("Temporary dashboard password configured.")
		}
	} else {
		logins = strings.Split(*adminLogins, ",")
	}
	for i, login := range logins {
		if err := adminAuth.SetLogin(login); err != nil {
			log.Printf("Failed to authorize login %v: %v", i, err)
		}
	}

	if _, err := startDashboard(children, adminAuth, quit); err != nil {
		log.Print("Failed to start dashboard: ", err)
		// TODO: retry it? Also check the dashboardQuit signal for retries
	} else {
		if usedExampleConf {
			// Sleep for the async "Starting server..." log
			time.Sleep(5 * time.Millisecond)
			log.Printf("Since you used the example conf, the dashboard url is:\n"+
				"\thttps://127.0.0.1:8080%v", *dashboardUrlFlag)
		}
	}

	<-quit
	if !*dontKillChildren {
		shutdownErr := errors.New("Shutting down.")
		for _, child := range children.ByPID {
			proc := child.Proc
			if proc == nil {
				continue
			}
			log.Print("Killing ", child.Name)
			proc.Signal(syscall.SIGTERM)
			proc.Wait()
			// Note: this deletes the chroot files (and logs messages)
			children.ReportDown(proc.Pid, shutdownErr)
		}
	}
	log.Print("Goodbye.")
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
	return loadConfig(configText)
}

func loadConfig(configText []byte) ([]*Command, error) {
	config := &Config{}
	if err := prototext.Unmarshal(configText, config); err != nil {
		return nil, err
	}
	err := resolveRelativePaths(*searchPath, config.Command)
	return config.Command, err
}
