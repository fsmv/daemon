/*
Host is a basic file server suitable for hosting static websites using portal.

Host serves index.html in place of the directory name and optionally serves
directory listing pages.

Install host standalone with:

	CGO_ENABLED=0 go install ask.systems/daemon/host@latest

You can also use host as a subcommand of the combined [ask.systems/daemon]
binary.
*/
package main

import (
	"flag"
	"os"

	"ask.systems/daemon/host/embedhost"
	"ask.systems/daemon/tools/flags"
)

func main() {
	flags.CopyrightNotice = "" +
		"Provided under the MIT License https://mit-license.org\n" +
		"Copyright 2017-2022 Andrew Kallmeyer"
	embedhost.Run(flag.CommandLine, os.Args)
}
