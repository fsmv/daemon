/*
Assimilate registers paths with portal on behalf of third-party HTTP servers
that listen on a fixed port.

Assimilate does not itself run any server, it's only a client for portal. It
stays running so that it can keep the lease for the fixed port and path renewed.

Install assimilate standalone with:

	CGO_ENABLED=0 go install ask.systems/daemon/assimilate@latest

You can also use assimilate as a subcommand of the combined [ask.systems/daemon]
binary.
*/
package main

import (
	"flag"
	"os"

	"ask.systems/daemon/assimilate/embedassimilate"
	"ask.systems/daemon/tools/flags"
)

func main() {
	flags.CopyrightNotice = "" +
		"Provided under the MIT License https://mit-license.org\n" +
		"Copyright 2017-2023 Andrew Kallmeyer"
	embedassimilate.Run(flag.CommandLine, os.Args)
}
