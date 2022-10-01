package gate_test

import (
	"context"
	"log"

	"ask.systems/daemon/portal/gate"
	"google.golang.org/protobuf/types/known/emptypb"
)

func ExampleClient() {
	// import _ "ask.systems/daemon/portal/flags" to easily set these values
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
