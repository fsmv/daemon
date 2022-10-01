package embedassimilate

import (
	"context"
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

func Run(flags *flag.FlagSet, args []string) {
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

	var wg sync.WaitGroup
	quit := make(chan struct{})
	if *gate.Token == "" {
		log.Fatal("-portal_token is required to connect to gate. The value is printed in the portal logs on startup.")
	}
	fe, err := gate.Connect(*gate.Address, *gate.Token)
	if err != nil {
		log.Fatal(err)
	}
	errCount := 0
	for i, requestText := range flag.Args() {
		registration := &gate.RegisterRequest{}
		err := prototext.Unmarshal([]byte(requestText), registration)
		if err != nil {
			log.Printf("Failed to unmarshal RegisterRequest %#v: %v", requestText, registration)
			errCount++
			continue
		}
		lease, err := fe.RPC.Register(context.Background(), registration)
		if err != nil {
			log.Printf("Failed to register #%v %+v: %v", i, registration, err)
			errCount++
			continue
		}
		log.Printf("Obtained lease for %#v, port: %v, timeout: %v",
			lease.Pattern, lease.Port, lease.Timeout.AsTime())
		wg.Add(1)
		go func() {
			fe.KeepLeaseRenewed(quit, lease)
			wg.Done()
		}()
	}
	if errCount == len(flag.Args()) {
		close(quit)
		wg.Wait()
		log.Fatal("None of the registrations were successful.")
	}

	tools.CloseOnQuitSignals(quit)
	<-quit
	wg.Wait()
	log.Print("Goodbye.")
}
