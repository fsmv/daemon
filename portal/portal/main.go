package main

import (
	"flag"
	"os"

	"ask.systems/daemon/portal/embedportal"
)

//go:generate protoc -I ../ ../embedportal/storage.proto --go_out ../ --go_opt=paths=source_relative
//go:generate protoc -I ../ ../service.proto --go_out ../ --go-grpc_out ../ --go_opt=paths=source_relative --go-grpc_opt=paths=source_relative

func main() {
	embedportal.Run(flag.CommandLine, os.Args)
}
