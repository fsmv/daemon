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
