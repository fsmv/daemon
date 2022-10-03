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
	oldArgs := os.Args
	os.Args = []string{"bin", "-hello"}
	flag.Parse()
	os.Args = oldArgs
	// Output:
	// Hello!
}
