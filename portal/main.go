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
	"context"
	"flag"
	"os"

	"ask.systems/daemon/portal/embedportal"
	"ask.systems/daemon/tools/flags"
)

func main() {
	flags.CopyrightNotice = "" +
		"Provided under the MIT License https://mit-license.org\n" +
		"Copyright 2017-2024 Andy Kallmeyer"
	embedportal.Run(context.Background(), flag.CommandLine, os.Args)
}
