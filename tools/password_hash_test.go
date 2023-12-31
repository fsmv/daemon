/*?sr/bin/env go run "$0" "$@"; exit $? #*/
// If you prefer to run it offline copy this to a new folder and run it locally
// using the go sh-bang line above. Just chmod +x hash.go && ./hash.go -pw stdin
package tools_test

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"ask.systems/daemon/tools"
)

// Change the password here to run in online in the docs site
var Password = flag.String("pw", "hunter2", "The password to hash")

func ExampleBasicAuthHandler_generatePasswordHash() {
	flag.Parse()
	if *Password == "stdin" { // so you don't put the password in .bash_history
		fmt.Printf("Type your password (not hidden) then press enter: ")
		if pwStr, err := bufio.NewReader(os.Stdin).ReadString('\n'); err == nil {
			*Password = strings.TrimSpace(pwStr)
		} else {
			log.Fatal(err)
		}
	}
	fmt.Println(tools.HashPassword(*Password))
}
