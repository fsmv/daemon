/*
Daemon is a webserver that securely organizes any internal webservers under a
single URL. Internal servers register a path on the URL with the main
[ask.systems/daemon/portal] server via gRPC. Portal then acts as a
reverse proxy, accepting all requests to the URL and forwarding requests and
responses to and from the server that registered for the requested path.

This package is a single binary which combines all of the binaries shipped with
the daemon system into one simple package using subcommand arguments.

# Installing

	CGO_ENABLED=0 go install ask.systems/daemon@latest

Why turn off cgo?

With the focus on security, daemon supports running servers in chroot, which
means system libraries are not available to load by dynamic linking. Using the
CGO_ENABLED=0 turns off C implementations used by certain go standard library
packages, this produces a fully static linked binary that works in the chroot.

So you should also compile your own go server binaries with CGO_ENABLED=0.
For more information and options see: https://www.arp242.net/static-go.html

# Setup and explanation

First you need to purchase a domain name to host your website. Without a domain
name, you cannot get a TLS certificate signed by a Certificate Authority that is
accepted by all major web browsers. This means a domain name is required to get
encryption in transit that works without big scary security warnings in
browsers. Once you have a domain set up a DNS A record pointing to your server's
public IP address (search "what is my IP" online if at home) using your
registrar's interface. Finally, if you're home hosting, set up port forwarding
in your router settings page (usually accessible at http://192.168.1.1 with
some manufacturer specific default username and password) to forward all
requests to port 80 and port 443 to your server's local IP address (it will
usually look like 192.168.1.xxx and on linux will be printed, among other
things, by ifconfig).

TODO: When portal supports self signed certificates, explain it here

Then the main thing you need is [ask.systems/daemon/portal], the reverse proxy
server that is configured via gRPC. Portal will accept all connections to your
domain name, instead of any servers like Apache or NGINX. To encrypt this
traffic portal needs your TLS cert, the easiest way to get one is using the
https://letsencrypt.org/ Certificate Authority (CA). Install their certbot tool
with your operating system's package manager then we can use it to get the
certificate. This next step won't work if you didn't correctly set up your DNS
and port forwarding settings.

Note: in all examples below I've used my domain name ask.systems, replace this
with your domain name. It will give you an error if you use mine.

The first time you get your certificate run:
(and make sure you don't have any server's binding port 80)

	sudo certbot certonly --standalone -d ask.systems

Let's Encrypt will then sign a certificate for your URL and store the two
certificate and key files in the standard location, which will be printed. Save
these two filepaths for configuring portal arguments.

These cert files will only be readable by the root user, certbot configures it
this way because it is critical that no one ever gains access to your cert keys,
if they did they could impersonate your server and decrypt data in transit.
However, you do not want to run a web server, like portal, accessible to the
internet using the root user permissions. If a server running as root had a
vulnerability, attackers would immediately have root permissions on your server!

To solve this problem, and make it convenient to run many servers, daemon
includes a launcher program [ask.systems/daemon/spawn]. Spawn uses a [textproto]
configuration file to list the server binaries to run and the commandline
arguments to run them with, as well the user to run them as. Most editors
recognize the .pbtxt extension, the default name is config.pbtxt in the
spawn working dir.

Spawn has a -config_schema help argument to print the fields accepted in the
config file and documentation on the meaning of the options.

Example spawn config.pbtxt for running portal only: (change my domain to yours)

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

This will tell spawn to, while running as root, bind the privileged ports
(linux only allows root to use these ports) and open the root owned cert files,
then use the OS to securely pass these resources to portal, which we tell portal
about using the file descriptor numbers in the arguments. Also as a security
measure by default spawn runs all servers in a chroot so the cannot access
files outside of the user's home directory (or the working_dir set in the
config) in the event it did get hacked.

The rest of the config options are for automatically renewing the Let's Encrypt
TLS certificate. If you don't want to bother you can just restart the portal
server (from the spawn dashboard page) whenever you renew the cert. To renew
without any down-time, both spawn and portal need to coordinate to refresh the
privileged files and the two auto_tls_certs flags set this up on both sides.
The -cert_challenge_webroot flag is a local directory path inside the chroot,
which means / is actually /home/www/, this directory
/home/www/cert-challenge/ is where you will tell certbot to put the
temporary challenge files to prove you own the domain.

So to renew your TLS certificate you need to put the following commands in an
executable shell script e.g. /root/renew-cert.sh, which we will run
periodically with cron: (change the domain name)

	certbot certonly -n --webroot -w /home/www/cert-challenge/ -d ask.systems
	killall -SIGUSR1 {spawn,portal}

Then to set it up to run regularly, first run:

	sudo crontab -e # edit the root crontab file with $EDITOR

Then add to the cron file:

	@weekly /root/renew-cert.sh

Finally we're ready to run the server. For the first time, simply run it in the
terminal, assuming your $GOPATH/bin in in your $PATH, with:

	sudo daemon spawn

This will run spawn as root which will run portal as www and importantly, portal
will create a save state file (in /home/www/) and print the newly generated
API token for registering paths. You will need to pass this to client binaries
as an argument.

You can now set up running spawn at boot and enable the spawn dashboard which
streams logs to the browser and has server restart buttons!

The easiest thing to do is to copy the daemon binary to /root/daemon, or you
could set up a GOPATH for the root user and use go install to update daemon.
Then move the config file to /root/config.pbtxt as well. Finally set up
running spawn at boot. I think the easiest way is using cron again with
@reboot, but you can use your system init service if you like.

To use cron to run spawn run sudo crontab -e again and add:

	@reboot /root/daemon -portal_token $TOKEN spawn -password_hash $HASH

The $HASH is for the dashboard password protection. You can use
https://go.dev/play/p/swuUb50vdyq to generate the hash for your password.
Currently the username is not configurable, it's admin. The default dashboard
path is example.com/daemon/ you can configure it with -dashboard_url.

TODO: I plan to support authentication more generically so it won't be a spawn
flag in the future. I think it will be an option in the portal registration
request.

Optionally I recommend using syslog, which is a service that collects, combines
and compresses logs which most unix operating systems have by default. With go
binaries using the daemon library, if you're using chroots use the
-syslog_remote flag (the help text has information on setting it up). For third
party servers can pipe output to the logger binary which most distributions come
with.

Now it's all done ready! Check out the dashboard page!

# Running built in servers

Daemon includes a basic file server (with index.html serving for directory
paths) to serve a local path for files and static web pages:
[ask.systems/daemon/host]. Simply add an entry to your config file. For example
to serve favicon.ico in /home/www/favicon.ico add:

	command {
		binary: "host"
		user: "www"
		name: "favicon"
		args: [
			"-syslog_remote=127.0.0.1",
			"-portal_token=YOUR TOKEN HERE",
			"-web_root=/",
			"-url_path=/favicon.ico"
		]
	}

# Running third party servers

For third party web servers that don't know how to talk to portal, daemon
includes [ask.systems/daemon/assimilate]. You can add assimilate to your config
and it accepts arguments for any number of registrations to send to portal for
a fixed port that the third party server listens on locally. You can then host
for example a minecraft map listening on :8080/ to example.com/minecraft/ with:

	command {
		binary: "assimilate"
		user: "www"
		args: [
			"-portal_token=YOUR TOKEN HERE",
			"-syslog_remote=127.0.0.1",

			"pattern: '/minecraft/' fixed_port: 8080 strip_pattern: true"
		]
	}

Additionally you could use spawn to launch third party binaries as well and pass
them a fixed port in the commandline arguments which assimilate will register
for them. If you don't want to copy the binary to your spawn path just use an
absolute file path for the binary field. Also you may need to set
no_chroot: true unless it's a statically linked binary. Or just use your system
init system, whatever you like.

# Running custom go servers

For servers written in go, you can use the portal client library
[ask.systems/daemon/portal/gate] to register with portal, automatically select a
port to listen on that won't conflict and even automatically use a newly
generated TLS certificate to encrypt local traffic (this time it's easy!). To do
this you will call [ask.systems/daemon/portal/gate.StartTLSRegistration], set up
any application handlers with [net/http.Handle] then call
[ask.systems/daemon/tools.RunHTTPServerTLS].

Make sure to take a look at the utility functions in [ask.systems/daemon/tools]!

Take a look at the package example for the client library
[ask.systems/daemon/portal/gate] for a simple go client of portal with encrypted
internal traffic. It uses the standard [net/http.Handle] system.

Remember: Make sure to compile your binaries with CGO_ENABLED=0 go build to
allow them to run in a chroot.

You can then copy your binary to /root/ next to daemon and add an entry to
your /root/config.pbtxt with binary name and arguments. By default spawn
checks the working dir for binaries named in the config and you can set the
spawn -path argument to change it.

[textproto]: https://developers.google.com/protocol-buffers/docs/text-format-spec
*/
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	_ "ask.systems/daemon/portal/flags"
	"ask.systems/daemon/tools/flags"

	"ask.systems/daemon/assimilate/embedassimilate"
	"ask.systems/daemon/host/embedhost"
	"ask.systems/daemon/portal/embedportal"
	"ask.systems/daemon/spawn/embedspawn"
)

//go:generate protoc -I ./portal/ portal/embedportal/storage.proto --go_out ./portal --go_opt=paths=source_relative
//go:generate protoc -I ./portal/ portal/gate/service.proto --go_out ./portal --go-grpc_out ./portal --go_opt=paths=source_relative --go-grpc_opt=paths=source_relative
//go:generate protoc -I ./spawn/ spawn/embedspawn/config.proto --go_out ./spawn --go_opt=paths=source_relative

type command struct {
	name        string
	run         func(*flag.FlagSet, []string)
	description string
}

var commands = []command{
	{"spawn", embedspawn.Run, "" +
		"Launches other processes in a chroot and as different users. Manages\n" +
		"privileged files."},
	{"portal", embedportal.Run, "" +
		"The reverse proxy RPC server that controls all of the paths of a URL\n" +
		"and port reservation for other binaries."},
	{"assimilate", embedassimilate.Run, "" +
		"Registers third party servers with portal on a fixed port if they\n" +
		"don't have the client library."},
	{"host", embedhost.Run,
		"Hosts a file server for a local folder registered on any path with portal."},
}

var namePadding string

func init() {
	flags.CopyrightNotice = "" +
		"Provided under the MIT License https://mit-license.org\n" +
		"Copyright 2017-2022 Andrew Kallmeyer"

	maxLen := 0
	for _, cmd := range commands {
		// Tell spawn what commands it can use in case we are running spawn
		embedspawn.MegabinaryCommands = append(embedspawn.MegabinaryCommands, cmd.name)
		if len(cmd.name) > maxLen {
			maxLen = len(cmd.name)
		}
	}
	// Set the field width to the longest command name
	namePadding = "  %-" + strconv.Itoa(maxLen) + "s  "
}

func main() {
	// If the binary has been renamed to start with one of the subcommand names,
	// act as if it is just that one binary.
	binName := filepath.Base(os.Args[0])
	for _, cmd := range commands {
		if !strings.HasPrefix(binName, cmd.name) {
			continue
		}
		cmd.run(flag.CommandLine, os.Args)
		return
	}
	// The binary name didn't match, operate in subcommands mode

	// Setup the help text and parse the flags
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), ""+
			"Usage: %s [global flags] [subcommand] [subcommand flags]\n"+
			"Run any subcommand with -help for the subcommand's flags.\n\nSubcommands:\n",
			flag.CommandLine.Name())
		for _, cmd := range commands {
			paddedDescription := strings.ReplaceAll(cmd.description,
				"\n", fmt.Sprintf("\n"+namePadding, ""))
			fmt.Fprintf(flag.CommandLine.Output(),
				namePadding+"%s\n", cmd.name, paddedDescription)
		}
		fmt.Fprintf(flag.CommandLine.Output(), "\nGlobal flags (these apply to all subcommands):\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	args := flag.Args()
	if len(args) < 1 { // print the help if there's no subcommand specified
		flag.Usage()
		os.Exit(2)
	}
	// Run the subcommand if it matches
	subcommand := args[0]
	flags := flag.NewFlagSet(subcommand, flag.ExitOnError)
	for _, cmd := range commands {
		if subcommand != cmd.name {
			continue
		}
		cmd.run(flags, args)
		return
	}
	fmt.Fprintf(flag.CommandLine.Output(), "Invalid subcommand %#v\n\n", subcommand)
	flag.Usage()
	os.Exit(1)
}
