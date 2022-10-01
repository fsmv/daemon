package tools_test

import (
	"flag"
	"fmt"
	"os"

	"ask.systems/daemon/tools"
)

func HandleHello(value string) error {
	fmt.Println("Hello!")
	return nil
}

func ExampleBoolFuncFlag() {
	flag.Var(tools.BoolFuncFlag(HandleHello), "hello",
		"If set, print hello")

	// The handler function is called when flag.Parse sees the flag
	os.Args = []string{"bin", "-hello"}
	flag.Parse()
	// Output:
	// Hello!
}
