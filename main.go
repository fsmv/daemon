package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	_ "ask.systems/daemon/portal/flags"
	_ "ask.systems/daemon/tools/flags"

	"ask.systems/daemon/assimilate/embedassimilate"
	"ask.systems/daemon/host/embedhost"
	"ask.systems/daemon/portal/embedportal"
	"ask.systems/daemon/spawn/embedspawn"
)

type command struct {
	name        string
	run         func(*flag.FlagSet, []string)
	description string
}

const namePadding = "  %-10s  "

var commands = []command{
	{"spawn", embedspawn.Run, "" +
		"Launches other processes in a chroot and as different users. Manages\n" +
		"privileged files."},
	{"portal", embedportal.Run, "" +
		"The reverse proxy RPC server that controls all of the paths of a URL\n" +
		"and port reservation for other binaries."},
	{"assimilate", embedassimilate.Run, "" +
		"Registers third party servers with portal on a fixed port if they\n" +
		"don't have the client library."},
	{"host", embedhost.Run,
		"Hosts a file server for a local folder registered on any path with portal."},
}

func main() {
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), ""+
			"Usage: %s [global flags] [sub-command] [sub-command flags]\n"+
			"Run any subcommand with -help for the sub-command's flags.\n\nSub-commands:\n",
			flag.CommandLine.Name())
		for _, cmd := range commands {
			paddedDescription := strings.ReplaceAll(cmd.description,
				"\n", fmt.Sprintf("\n"+namePadding, ""))
			fmt.Fprintf(flag.CommandLine.Output(),
				namePadding+"%s\n", cmd.name, paddedDescription)
		}
		fmt.Fprintf(flag.CommandLine.Output(), "\nGlobal flags:\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	args := flag.Args()
	if len(args) < 1 {
		flag.Usage()
		os.Exit(-2)
	}

	name := args[0]
	flags := flag.NewFlagSet(name, flag.ExitOnError)
	for _, cmd := range commands {
		if name != cmd.name {
			continue
		}
		cmd.run(flags, args)
		return
	}
}
