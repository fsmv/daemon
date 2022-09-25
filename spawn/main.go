package main

import (
	"flag"
	"os"

	"ask.systems/daemon/spawn/embedspawn"
)

//go:generate protoc embedspawn/config.proto --go_out ./ --go_opt=paths=source_relative

func main() {
	embedspawn.Run(flag.CommandLine, os.Args)
}
