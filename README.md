![daemon logo](images/text-logo.png)

[![Go Reference](https://pkg.go.dev/badge/ask.systems/daemon.svg)](https://pkg.go.dev/ask.systems/daemon)
[![chatroom icon](https://patrolavia.github.io/telegram-badge/chat.png)](https://t.me/daemonserver)

daemon is a web server system made of separate processes focused on a reverse
proxy server that is configured automatically over RPC (or one line of config
for pre-existing servers). It also has a launcher program that can run your
(go) servers in a minimal chroot under a separated user.

It's especially good if you want to write go servers and share one URL between
them easily with automatic TLS support.

# Features

 - 🔀 Run many backend web servers and securely share a single TLS protected
   domain name (via reverse proxy)
 - 🧑‍💻 Read the logs of each server streaming real time to the dashboard
   which also lets you reload the config file and restart servers
 - 🔛 Launch backend servers isolated in a minimal chroot as unprivileged users,
   configured in a simple
   [textproto](https://developers.google.com/protocol-buffers/docs/text-format-spec)
   file
 - 🤖 Backends automatically register their paths on the domain via gRPC
   (there's also support for servers that can't send the RPCs) and receive a
   port assignment and get their TLS certificate signed by the portal
   Certificate Authority
 - 📚 There's a
   [client library](https://pkg.go.dev/ask.systems/daemon/portal/gate) to do all
   this in one function call, and a
   [tools library](https://pkg.go.dev/ask.systems/daemon/tools) full of helpful
   functions for writing a go webserver
 - 👾 Install as one binary that runs each server, or as individual binaries

# Programs

 - [spawn](https://pkg.go.dev/ask.systems/daemon/spawn) a launcher program that
   runs processes isolated in a chroot as unprivileged users configured with
   [textproto](https://developers.google.com/protocol-buffers/docs/text-format-spec)
   (which supports comments!)
   - Gives access to root owned TLS certs and ports without running other
     servers as root
   - Runs the dashboard to restart the processes or view logs
 - [portal](https://pkg.go.dev/ask.systems/daemon/portal), the reverse proxy,
   sets up the forwarding rules via gRPC dynamically instead of a using a static
   config file
   - So you can run a development server and have it register a temporary
     path without needing access to your TLS certificate
   - It works over the internet too (everything is TLS encrypted), so you can
     put many servers behind one URL without needing to configure all of the
     backends centrally
 - [host](https://pkg.go.dev/ask.systems/daemon/host) a simple
   static website or file server that supports password protection
 - [assimilate](https://pkg.go.dev/ask.systems/daemon/assimilate) which
   registers with portal on behalf of third-party servers that don't know about
   daemon (as in: open source tools with a web UI, or raw TCP services you need
   to wrap in TLS encryption)

# Quick Start

## Test it out with no setup

Make sure you have go installed then

1. Install it with `go install ask.systems/daemon@latest`
   - This will leave the binary at `~/go/bin/daemon` by default
   - I like to configure my `$GOPATH/bin` to be in my `$PATH`
2. Run `daemon spawn` (or use `~/go/bin/daemon` if it isn't in your `$PATH`)
   - This will run spawn which will print logs and write an example
     `config.pbtxt` in the current directory
   - It will run portal and the dashboard, prompt for a dashboard login
     password, and then print the address of the dashboard at the end.
   - Note: since this doesn't include getting an officially recognized TLS
     certificate, portal will generate a new self-signed certificate which
     you will have to accept warnings about in your browser.

You can then edit the config file which has comments about how it works. You can
add an instance of [host](https://pkg.go.dev/ask.systems/daemon/host) to try out
hosting a static website (there's an example in the file commented out).

## Setting it up to keep it

Note: while daemon runs on most operating systems, the latest versions of macOS,
and all versions of Windows do not support chroots. Windows also doesn't support
changing the user.

So if you're not using Linux or BSD you will need to make some adjustments to
your config.

See [Operating Systems Support](#operating-systems-support)

### 1. You need to run spawn as root

This is so that it can access the TLS certs, open port 80,
and run the other servers as less privileged users in a chroot. The easiest way
to set it up so that you can quickly update daemon is to install with:

    sudo go install ask.systems/daemon@latest

This will run the go compiler as root and download from the official goproxy
server but I think it's trustworthy. If you prefer you can just copy the binary
to a root owned place after installing it with non-root.

### 2. Setup the spawn config

In the example config delete the test portal config and uncomment the one that's
meant for ports 80 & 443

You can print the example config with `daemon spawn -example_config`. Spawn also
will create the file if you run it with no arguments (and run portal too).

You should make the `config.pbtxt` only accessible to root, because anyone who
can write it can run arbitrary binaries as any user, also you might have API
keys in it so you probably don't want people reading it either.

You will also need to add any new users to your system. It's best to set up a
home dir for the user because daemon uses it  to store save files or files to
host by default. I recommend making a dedicated user for portal in particular,
but you can use the usually-existing www user for host and assimilate, or you
can make new users for each server. It's best to keep servers isolated so they
can't access each others files in case one has a vulnerability or bug,
especially with servers you download.

### 3. Setup a https://letsencrypt.org/ TLS certificate for your domain name

With your domain registrar, configure your DNS A/AAAA records to point at the
IP of the computer running portal (consider if you need a dynamic DNS setup if
at home).

Then install the letsencrypt tool certbot. If you don't have portal running you
can use: `sudo certbot certonly --standalone -d <domain>` to register the
certificate for the first time. Certbot will make your certificate only readable
by root and you should leave it that way and leave it in the path certbot puts
it in.

To setup auto-renewing I prefer to use cron (but you can use systemd timers or
whatever else) to run the following script weekly:

    certbot certonly -n --webroot -w /home/portal/cert-challenge/ -d <domain>
    killall -SIGUSR1 {spawn,portal}

This renews using the webroot method where certbot puts a file in a directory
owned by portal to verify the domain. This path assumes you have portal setup
to run under the dedicated user named portal. The killall line sends the SIGUSR1
signal to both binaries to notify them that you have renewed the certificate so
they can refresh the files with no downtime.

If you have multiple domains pointing to the same server you can just run
certbot multiple times in the script and you only need to send the SIGUSR1 once
to refresh all certificates.

### 4. Set it up to run at boot with your favorite init service

I think `@reboot daemon spawn -dashboard_logins admin:<hash> > /dev/null` in
your `sudo crontab -e` is good enough but you can run it with systemd or
whatever else if you like.

Check out the flags for spawn with `daemon spawn -help` so you can configure
the location of the config file (the default is the working directory), etc
if needed.

### 5. Firewall and syslog

If you're hosting at home you need to configure port forwarding on your router
for port 80 and port 443 to the machine running portal. Also you need to make
sure your server machine's firewall settings allow public access to those ports.

portal listens on port 2048 for the RPC server to register paths. There's an
authorization token and portal RPCs use TLS, so it is safe to leave open to
the public if you want to run servers behind multiple IPs all routed through
the same portal server. Otherwise it might be nice to prevent non-local
connections to 2048. Portal also assigns backend clients ports in the range
2049-4096 so it would be good to prevent external access to those ports,
since they should only be accessed via portal. Also don't run non-portal
clients on those ports, or portal may assign a conflicting port.

Look into setting up syslog. all the daemon binaries support (command line
argument) and most server operating systems come with a service that supports
the protocol (systemd does) which saves all the logs in one place and supports
automatic log rotation. Since spawn runs binaries in chroot the best way to do
it is to set up the network syslogging protocol and allow only local
connections. daemon uses the user syslog facility since usually that is kept
unused but you can configure syslog to move it to it's own file.

# Installing a single binary

If you want to deploy only specific daemon binaries instead of the combined
package you can install any of them like:

```
go install ask.systems/daemon/host@latest
```

Tip: The portal client library reads the `PORTAL_ADDR` and `PORTAL_TOKEN`
environment variables automatically. So if you set those up in your shell
dotfiles you can just run daemon binaries or go servers you wrote yourself with
no other setup. That way it will automatically serve on your domain name with
TLS publicly and internally, even if you're on a completely different network
from your portal machine.

# Operating Systems Support

## Linux and BSD

All features of daemon work on Linux and BSD-based operating systems.

## macOS

Everything works before v11.0.1, which is when they started to use the
[dynamic linker cache](https://developer.apple.com/documentation/macos-release-notes/macos-big-sur-11_0_1-release-notes#Kernel),
which prevents us from building a working chroot environment (also
[SIP](https://developer.apple.com/documentation/security/hardened_runtime)
would prevent using the libraries even if we could extract them from the cache).
Additionally it's not possible to statically link the system libraries in macOS.
I believe as of Big Sur it is not reasonably possible to create a chroot in
macOS.

So you will have to use the `no_chroot: true` config setting on all commands in
newer versions.

## Windows

In windows chroot doesn't exist. Additionally I believe there's no way using the
go process API to change to a less privileged user as we start a process.

There are also no signals sent to processes in Windows, so to renew the TLS
certificate we can't use that killall command in the script. Luckily, there is
also no root user in windows so when you run certbot the files will be
accessible by any process you run. So, you can just have portal open the files
directly using the `-tls_cert` and `-tls_key` flags, portal will reopen the
files automatically 1 hour before expiration. Also you can't pass socket FDs to
subprocesses in windows.

So the config changes you need to make are:

 1. Use the `no_chroot: true` option on all commands
 2. Do not set the `user: "username"` field on any command
 3. For the permanent portal config, instead of using the files field in the
    command to configure your TLS cert paths, use the `-tls_cert` and `-tls_key`
    args. Also there's no reason to use the ports field on windows, you can
    just use the default values for the port flags (so delete that line).
 4. In textproto syntax any \ characters need to be escaped so windows paths
    will look like `"-tls_cert=C:\\Certbot\\..."`
 5. Don't use the ports field in the command message of the spawn config file.
    Instead just have portal open the ports by setting them in the flags for
    portal. This feature is for unix systems that require root to open port 80
    and 443 anyway.
