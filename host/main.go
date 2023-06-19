/*
Host is a basic file server suitable for hosting static websites using portal.

Host serves index.html in place of the directory name and optionally serves
directory listing pages.

You can password protect a directory using HTTP basic auth by placing a
.passwords file in the directory containing username:password_hash lines for
each authorized user. Passwords in subdirectories are recursively added on top
of the list from the parent directory.  Password hashes must be compatible with
[ask.systems/daemon/tools.DefaultCheckPassword]. To generate a password hash you
can use host -hash_password. Be careful about who has write access to
.passwords!

Install host standalone with:

	CGO_ENABLED=0 go install ask.systems/daemon/host@latest

You can also use host as a subcommand of the combined [ask.systems/daemon]
binary.
*/
package main

import (
	"flag"
	"os"

	"ask.systems/daemon/host/embedhost"
	"ask.systems/daemon/tools/flags"
)

func main() {
	flags.CopyrightNotice = "" +
		"Provided under the MIT License https://mit-license.org\n" +
		"Copyright 2017-2023 Andrew Kallmeyer"
	embedhost.Run(flag.CommandLine, os.Args)
}
