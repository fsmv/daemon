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
		"Copyright 2017-2022 Andrew Kallmeyer"
	embedassimilate.Run(flag.CommandLine, os.Args)
}
