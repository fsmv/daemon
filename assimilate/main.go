package main

import (
	"flag"
	"os"

	"ask.systems/daemon/assimilate/embedassimilate"
)

func main() {
	embedassimilate.Run(flag.CommandLine, os.Args)
}
