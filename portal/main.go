/*
Portal is a reverse proxy HTTPS server configured via gRPC

A reverse proxy means that portal accepts all requests to your domain name and
then forwards the requests to backend servers, which have been configured via
gRPC, and then relays the backend's response back to the client.

Install portal standalone with:

	go install ask.systems/daemon/portal@latest

You can also use portal as a subcommand of the combined [ask.systems/daemon]
binary.

The client library for portal is [ask.systems/daemon/portal/gate]
*/
package main

import (
	"flag"
	"os"

	"ask.systems/daemon/portal/embedportal"
	"ask.systems/daemon/tools/flags"
)

//go:generate protoc -I ./ embedportal/storage.proto --go_out ./ --go_opt=paths=source_relative
//go:generate protoc -I ./ gate/service.proto --go_out ./ --go-grpc_out ./ --go_opt=paths=source_relative --go-grpc_opt=paths=source_relative

func main() {
	flags.CopyrightNotice = "" +
		"Provided under the MIT License https://mit-license.org\n" +
		"Copyright 2017-2023 Andrew Kallmeyer"
	embedportal.Run(flag.CommandLine, os.Args)
}
