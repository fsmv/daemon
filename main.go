package main

import (
	"flag"
	"log"

	_ "ask.systems/daemon/portal/flags"
	_ "ask.systems/daemon/tools/flags"

	"ask.systems/daemon/assimilate/embedassimilate"
	"ask.systems/daemon/host/embedhost"
	"ask.systems/daemon/portal/embedportal"
	"ask.systems/daemon/spawn/embedspawn"
)

// TODO: print the binaries available with -help

func main() {
	flag.Parse()
	args := flag.Args()
	if len(args) < 1 {
		log.Fatal("Use spawn, portal, assimilate, or host as the first non-flag arg")
	}
	flags := flag.NewFlagSet(args[0], flag.ExitOnError)
	switch args[0] {
	case "spawn":
		embedspawn.Run(flags, args)
	case "portal":
		embedportal.Run(flags, args)
	case "host":
		embedhost.Run(flags, args)
	case "assimilate":
		embedassimilate.Run(flags, args)
	}
}
