package main

import (
	"flag"
	"os"

	"ask.systems/daemon/host/embedhost"
)

func main() {
	embedhost.Run(flag.CommandLine, os.Args)
}
