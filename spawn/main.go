/*
Spawn is a launcher with a web dashboard that runs commands with arguments
listed in the textproto config.pbtxt file.

Install spawn standalone with:

	go install ask.systems/daemon/spawn@latest

You can also use spawn as a subcommand of the combined [ask.systems/daemon]
binary.

Spawn is run with root permissions so that it can open privileged files and
ports for child servers, place child servers in a chroot, and set the user to
run them as. This means we can avoid running any servers as root.

The dashboard provides a convenient way to restart servers and read the logs in
real time.

Example [textproto] config.pbtxt: (here showing two different repeated field styles)

	command {
		binary: "portal"
		user: "www"
		ports: [80, 443]
		files: [
			"/etc/letsencrypt/live/ask.systems/fullchain.pem",
			"/etc/letsencrypt/live/ask.systems/privkey.pem"
		]
		auto_tls_certs: true
		args: [
			"-http_port=-3",
			"-https_port=-4",
			"-tls_cert=5",
			"-tls_key=6",
			"-auto_tls_certs",
			"-cert_challenge_webroot=/cert-challenge/"
		]
	}
	# Serve the favicon.ico for the URL.
	# Appears on the dashboard and in logs as host-favicon.
	command {
		binary: "host"
		user: "www"
		name: "favicon"

		args: "-portal_token=YOUR TOKEN HERE"
		args: "-web_root=/"
		args: "-url_path=/favicon.ico"
	}

[textproto]: https://developers.google.com/protocol-buffers/docs/text-format-spec
*/
package main

import (
	"context"
	"flag"
	"os"

	"ask.systems/daemon/spawn/embedspawn"
	"ask.systems/daemon/tools/flags"
)

func main() {
	flags.CopyrightNotice = "" +
		"Provided under the MIT License https://mit-license.org\n" +
		"Copyright 2017-2024 Andy Kallmeyer"
	embedspawn.Run(context.Background(), flag.CommandLine, os.Args)
}
