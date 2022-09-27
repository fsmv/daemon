package main

import (
	"flag"
	"os"

	"ask.systems/daemon/spawn/embedspawn"
	"ask.systems/daemon/tools/flags"
)

//go:generate protoc embedspawn/config.proto --go_out ./ --go_opt=paths=source_relative

func main() {
	flags.CopyrightNotice = "" +
		"Provided under the MIT License https://mit-license.org\n" +
		"Copyright 2017-2022 Andrew Kallmeyer"
	embedspawn.Run(flag.CommandLine, os.Args)
}
