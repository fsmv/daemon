/*?sr/bin/env go run "$0" "$@"; exit $? #*/
// You can copy this to a file and make it executable and run it locally with
// ./pass.go for privacy if you like
package tools_test

import (
	"bufio"
	"crypto/sha256"
	"encoding/base64"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
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
	hash := sha256.Sum256([]byte(*Password))
	fmt.Println(base64.URLEncoding.EncodeToString(hash[:]))
	// Output:
	// 9S-9MrKzuG_4jvbEkGKChfSCrxXdyylUH5S89Saj9sc=
}
