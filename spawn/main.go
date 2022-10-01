/*
Spawn is a launcher with a web dashboard that runs commands with arguments
listed in the config.pbtxt file.

Spawn is run with root permissions so that it can open privileged files and
ports for child servers, place child servers in a chroot, and set the user to
run them as. This means we can avoid running any servers as root.

The dashboard provides a convenient way to restart servers and read the logs in
real time.

Install spawn standalone with:

	CGO_ENABLED=0 go install ask.systems/daemon/spawn@latest

You can also use spawn as a subcommand of the combined [ask.systems/daemon]
binary.
*/
package main

import (
	"flag"
	"os"

	"ask.systems/daemon/spawn/embedspawn"
	"ask.systems/daemon/tools/flags"
)

//go:generate protoc -I ./ embedspawn/config.proto --go_out ./ --go_opt=paths=source_relative

func main() {
	flags.CopyrightNotice = "" +
		"Provided under the MIT License https://mit-license.org\n" +
		"Copyright 2017-2022 Andrew Kallmeyer"
	embedspawn.Run(flag.CommandLine, os.Args)
}
