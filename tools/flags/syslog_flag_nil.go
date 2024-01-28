//go:build nosyslog || windows

package flags

import "io"

// This is provided so that we can still compile on windows and if people use
// -nosyslog, since flags.Syslog is a public API.

var Syslog io.WriteCloser = nil
