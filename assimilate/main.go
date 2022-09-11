package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"

	_ "embed"

	_ "ask.systems/daemon/tools/flags"

	"ask.systems/daemon/portal"
	"ask.systems/daemon/tools"

	"google.golang.org/protobuf/encoding/prototext"
)

var (
	portalAddr = flag.String("portal_addr", "127.0.0.1:2048",
		"Address and port for the portal server")
)

// go:embed ../portal/service.proto
var schemaText string

func init() {
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(),
			"Usage: %s [flags] \"[textproto portal.RegisterRequest]\"...\n", os.Args[0])
		fmt.Fprintf(flag.CommandLine.Output(), ""+
			"Example:\n"+
			"  %v -portal_addr localhost:9999 \"pattern: '/test/' fixed_port: 8080 strip_pattern: true\" \\\n"+
			"    \"pattern: ':tcp:8181' fixed_port: 1337\"\n\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Var(
		tools.BoolFuncFlag(func(string) error {
			// Print the RegisterRequest message out of the embedded proto file
			start := strings.Index(portal.ServiceProto, "message RegisterRequest")
			end := strings.Index(portal.ServiceProto[start:], "}")
			fmt.Println(portal.ServiceProto[start : start+end+1])
			os.Exit(2)
			return nil
		}),
		"register_request_schema",
		"Print the schema for portal.RegisterRequest in proto form and exit.")
	flag.Parse()
}

func main() {
	var wg sync.WaitGroup
	quit := make(chan struct{})
	fe, err := portal.Connect(*portalAddr)
	if err != nil {
		log.Fatal(err)
	}
	errCount := 0
	for i, requestText := range flag.Args() {
		registration := &portal.RegisterRequest{}
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
