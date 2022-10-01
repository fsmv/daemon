// Defines the -portal_addr and -portal_token flags
package flags

import (
	"flag"

	"ask.systems/daemon/portal"
)

func init() {
	portal.Address = flag.String("portal_addr", "127.0.0.1:2048",
		"Address and port for the portal server")
	portal.Token = flag.String("portal_token", "", ""+
		"API Token for authorization with the portal server.\n"+
		"Printed in the portal logs on startup.")
}
