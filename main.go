/*
# Installing

This package is a single binary which combines all of the binaries shipped with
the daemon system into one simple package and you can run the servers using
subcommand arguments.

	sudo go install ask.systems/daemon@latest

You want to install it as root because you will run spawn as root so it is best
for security to make the binary owned by root, otherwise the user that owns the
binary could edit it and run any code as root. The easiest way to do this and
allow for updating daemon is to just run go install as root.

If you run spawn with no arguments it will create the example [textproto] config
and run portal plus the dashboard. If you want to just print the example config
run:

	daemon spawn -example_config

For more info read the README! Expand it above on the go docs site.

# Making custom go servers

For servers written in go, you can use the portal client library
[ask.systems/daemon/portal/gate] to register with portal, automatically select a
port to listen on that won't conflict and even automatically use a newly
generated TLS certificate to encrypt local traffic. Check the example for the
gate package, it's easy!. To do this you will call
[ask.systems/daemon/portal/gate.StartTLSRegistration], set up any application
handlers with [net/http.Handle] then call
[ask.systems/daemon/tools.RunHTTPServerTLS].

The easiest way to configure access to portal registration RPCs is via the
environment variables PORTAL_ADDR and PORTAL_TOKEN, which spawn can set
automatically. You can find the portal token printed in the portal logs on
startup. You can set the env vars up in your shell dotfiles so you can run test
binaries that register with portal.

Aside from the env vars you can also import _ [ask.systems/daemon/portal/flags]
if you'd like to configure the portal address and token with flags instead of
the environment variables.

Make sure to take a look at the other utility functions in
[ask.systems/daemon/tools] too! There's a second flags flags package which
provides the version stamp flag and the syslog support via the [log] package:
[ask.systems/daemon/tools/flags].

The source code of [ask.systems/daemon/host] is a good basic server example if
you want more than the [ask.systems/daemon/portal/gate] package example.

You can then sudo go install your own binary (or copy your binary to /root/) and
add an entry to your config.pbtxt with binary name and arguments. By default
spawn checks the working dir for binaries named in the config and you can set
the spawn -path argument to change it.

# Megabinary

The way the daemon binary works is each of the individual binaries in daemon
have all of their code packed into the <bin>/embed<bin> packages. Each of them
have a standard Run function that accepts commandline arguments. Then the
packages you actually install, such as [ask.systems/daemon/assimilate] or
[ask.systems/daemon] have a simple main function that just calls the Run
function from the appropriate embed package.

If you would like to, you can use the public interfaces in the embed packages
for your applications as well, so you could for example embed a copy of
[ask.systems/daemon/host] instead of calling the helper functions in
[ask.systems/daemon/tools] (which cover pretty much all of host's
functionality).

Also if you rename the [ask.systems/daemon] binary to one of the subcommands, it
will act as if it just that individual binary. Spawn actually uses this when
copying the megabinary to chroots so it will show in your process list and
syslog as the correct name.

[textproto]: https://developers.google.com/protocol-buffers/docs/text-format-spec
*/
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	_ "ask.systems/daemon/portal/flags"
	"ask.systems/daemon/tools/flags"

	"ask.systems/daemon/assimilate/embedassimilate"
	"ask.systems/daemon/host/embedhost"
	"ask.systems/daemon/portal/embedportal"
	"ask.systems/daemon/spawn/embedspawn"
)

//go:generate protoc -I ./ internal/portalpb/storage.proto --go_out ./ --go_opt=paths=source_relative
//go:generate protoc -I ./ internal/portalpb/service.proto --go_out ./ --go-grpc_out ./ --go_opt=paths=source_relative --go-grpc_opt=paths=source_relative
//go:generate protoc -I ./ internal/spawnpb/config.proto --go_out ./ --go_opt=paths=source_relative

type command struct {
	name        string
	run         func(context.Context, *flag.FlagSet, []string)
	description string
}

var commands = []command{
	{"spawn", embedspawn.Run, "" + //                            stop here: |
		"Launches other processes in a chroot and as different users. Manages\n" +
		"privileged files."},
	{"portal", embedportal.Run, "" +
		"The reverse proxy RPC server that controls all of the paths of a URL\n" +
		"and port reservation for other binaries."},
	{"assimilate", embedassimilate.Run, "" +
		"Registers third party servers with portal on a fixed port if they\n" +
		"don't have the client library."},
	{"host", embedhost.Run, "" +
		"Hosts a file server for a local folder registered on any path with\n" +
		"portal."},
}

var namePadding string

func init() {
	flags.CopyrightNotice = "" +
		"Provided under the MIT License https://mit-license.org\n" +
		"Copyright 2017-2024 Andy Kallmeyer <ask@ask.systems>"

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
	// TODO: maybe we should handle the close on quit signals in the mains
	ctx := context.Background()
	// If the binary has been renamed to start with one of the subcommand names,
	// act as if it is just that one binary.
	binName := filepath.Base(os.Args[0])
	for _, cmd := range commands {
		if !strings.HasPrefix(binName, cmd.name) {
			continue
		}
		cmd.run(ctx, flag.CommandLine, os.Args)
		return
	}
	// The binary name didn't match, operate in subcommands mode

	// Setup the help text and parse the flags
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), ""+
			"Usage: %s [global flags] [subcommand] [subcommand flags]\n"+
			"Run any subcommand with -help for the subcommand's flags.\n\n"+
			"** Start by running %s spawn! It will give you an example config. **\n\n"+
			"Subcommands:\n",
			flag.CommandLine.Name(), flag.CommandLine.Name())
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
		cmd.run(ctx, flags, args)
		return
	}
	fmt.Fprintf(flag.CommandLine.Output(), "Invalid subcommand %#v\n\n", subcommand)
	flag.Usage()
	os.Exit(1)
}
