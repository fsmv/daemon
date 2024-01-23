package gate_test

import (
	"context"
	"log"

	"ask.systems/daemon/portal/gate"
	"google.golang.org/protobuf/types/known/emptypb"
)

func ExampleClient() {
	// This call sets the globals from flags and environment variables.
	// import _ "ask.systems/daemon/portal/flags" to get the flag definitions.
	if err := gate.ResolveFlags(); err != nil {
		log.Fatal(err)
	}
	client, err := gate.Connect(*gate.Address, *gate.Token)
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()
	// Call the MyHostname RPC directly. This is used by [StartTLSRegistration] to
	// set the hostname in the TLS certificate request.
	resp, err := client.RPC.MyHostname(context.Background(), &emptypb.Empty{})
	if err != nil {
		log.Fatal(err)
	}
	log.Print(resp.Hostname)
}

func ExampleResolveFlags() {
	if err := gate.ResolveFlags(); err != nil {
		log.Fatal(err)
	}
	c, err := gate.Connect(*gate.Address, *gate.Token)
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()
	log.Print("Connected to portal!")
}
