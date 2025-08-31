// Embedassimilate lets you run the assimilate binary main function inside
// another program
//
// This is used by [ask.systems/daemon], but feel free to use it if you want to!
package embedassimilate

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"

	_ "embed"

	_ "ask.systems/daemon/portal/flags"
	_ "ask.systems/daemon/tools/flags"

	"ask.systems/daemon/portal/gate"
	"ask.systems/daemon/tools"
	"google.golang.org/protobuf/encoding/prototext"
)

// go:embed ../portal/service.proto
var schemaText string

func Run(ctx context.Context, flags *flag.FlagSet, args []string) {
	flags.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(),
			"Usage: %s [flags] \"[textproto gate.RegisterRequest]\"...\n", flags.Name())
		fmt.Fprintf(flag.CommandLine.Output(), ""+
			"Example:\n"+
			"  %v -portal_addr localhost:9999 \\\n"+
			"    \"pattern: '/test/' fixed_port: 8080 strip_pattern: true\" \\\n"+
			"    \"pattern: ':tcp:8181' fixed_port: 1337\"\n\n", flags.Name())
		flags.PrintDefaults()
	}
	flags.Var(
		tools.BoolFuncFlag(func(string) error {
			// Print the RegisterRequest message out of the embedded proto file
			start := strings.Index(gate.ServiceProto, "message RegisterRequest")
			end := strings.Index(gate.ServiceProto[start:], "}")
			fmt.Println(gate.ServiceProto[start : start+end+1])
			os.Exit(2)
			return nil
		}),
		"register_request_schema",
		"Print the schema for gate.RegisterRequest in proto form and exit.")
	flags.Parse(args[1:])

	client, err := gate.DefaultClient()
	if err != nil {
		log.Fatal(err)
	}
	var wg sync.WaitGroup
	ctx = tools.ContextWithQuitSignals(context.Background())
	errCount := 0
	for i, requestText := range flags.Args() {
		registration := &gate.RegisterRequest{}
		err := prototext.Unmarshal([]byte(requestText), registration)
		if err != nil {
			log.Printf("Failed to unmarshal RegisterRequest #%v %#v: %v",
				i, requestText, registration)
			errCount++
			continue
		}
		idx := i
		wg.Add(1)
		go func() {
			err := client.AutoRegisterChan(ctx, registration, nil)
			wg.Done()
			if err != nil && !errors.Is(err, context.Cause(ctx)) {
				log.Printf("Error for registration #%v: %v", idx, err)
			}
		}()
	}
	if errCount == len(flag.Args()) {
		client.Close()
		log.Fatal("None of the registrations were successful.")
	}

	wg.Wait()
	client.Close()
	log.Print("Goodbye.")
}
