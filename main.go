package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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

var namePadding string

func init() {
	maxLen := 0
	for _, cmd := range commands {
		// Tell spawn what commands it can use in case we are running spawn
		embedspawn.MegabinaryCommands = append(embedspawn.MegabinaryCommands, cmd.name)
		if len(cmd.name) > maxLen {
			maxLen = len(cmd.name)
		}
	}
	// Set the field width to the longest command name
	namePadding = "  %-" + strconv.Itoa(maxLen) + "s  "
}

func main() {
	// If the binary has been renamed to start with one of the subcommand names,
	// act as if it is just that one binary.
	binName := filepath.Base(os.Args[0])
	for _, cmd := range commands {
		if !strings.HasPrefix(binName, cmd.name) {
			continue
		}
		cmd.run(flag.CommandLine, os.Args)
		return
	}
	// The binary name didn't match, operate in subcommands mode

	// Setup the help text and parse the flags
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), ""+
			"Usage: %s [global flags] [subcommand] [subcommand flags]\n"+
			"Run any subcommand with -help for the subcommand's flags.\n\nSubcommands:\n",
			flag.CommandLine.Name())
		for _, cmd := range commands {
			paddedDescription := strings.ReplaceAll(cmd.description,
				"\n", fmt.Sprintf("\n"+namePadding, ""))
			fmt.Fprintf(flag.CommandLine.Output(),
				namePadding+"%s\n", cmd.name, paddedDescription)
		}
		fmt.Fprintf(flag.CommandLine.Output(), "\nGlobal flags (these apply to all subcommands):\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	args := flag.Args()
	if len(args) < 1 { // print the help if there's no subcommand specified
		flag.Usage()
		os.Exit(2)
	}
	// Run the subcommand if it matches
	subcommand := args[0]
	flags := flag.NewFlagSet(subcommand, flag.ExitOnError)
	for _, cmd := range commands {
		if subcommand != cmd.name {
			continue
		}
		cmd.run(flags, args)
		return
	}
	fmt.Fprintf(flag.CommandLine.Output(), "Invalid subcommand %#v\n\n", subcommand)
	flag.Usage()
	os.Exit(1)
}
