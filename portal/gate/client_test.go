package gate_test

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	_ "ask.systems/daemon/portal/flags"
	"ask.systems/daemon/portal/gate"
	"google.golang.org/protobuf/types/known/emptypb"
)

// Use the all in one helper to obtain a lease and wait for graceful shutdown
//
// Import the [ask.systems/daemon/portal/flags] package to use [AutoRegister].
func ExampleAutoRegister() {
	ctx, stop := context.WithTimeout(context.Background(), 30*time.Second)
	defer stop()
	wg := &sync.WaitGroup{}

	reg, err := gate.AutoRegister(ctx, &gate.RegisterRequest{
		Pattern: "/test/",
	}, wg)
	if err == nil {
		log.Printf("Obtained lease for port %v!", reg.Lease.Port)
	} else {
		log.Print("Failed to obtain initial lease:", err)
		log.Print("Will retry")
	}

	wg.Wait() // wait for the AutoRegister background goroutine to stop
}

// Demonstrates using [DefaultClient] to obtain a client and
// directly calling the [gate.PortalClient.MyHostname] RPC.
//
// This is less common than using [AutoRegister] or [Client.AutoRegister]
//
// Import the [ask.systems/daemon/portal/flags] package to use [DefaultClient].
func ExampleClient_directRPC() {
	// This uses the default portal env vars and flag definitions.
	// import _ "ask.systems/daemon/portal/flags" to get the flag definitions.
	client, err := gate.DefaultClient()
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()
	// Call the MyHostname RPC directly. This is used by Client.AutoRegister to
	// set the hostname in the TLS certificate request.
	resp, err := client.RPC.MyHostname(context.Background(), &emptypb.Empty{})
	if err != nil {
		log.Fatal(err)
	}
	log.Print(resp.Hostname)
}

// Shows using [Client.AutoRegister] to make multiple registrations in parallel
// and gracefully wait for them to clean up when stopped.
//
// In a real application you likely want to use the channel argument to at least
// read the initial registration result to get the randomly assigned port you
// should listen on. See the [Client.AutoRegister] example.
//
// Import the [ask.systems/daemon/portal/flags] package to use [DefaultClient].
func ExampleClient_autoRegister() {
	// This uses the default portal env vars and flag definitions.
	// import _ "ask.systems/daemon/portal/flags" to get the flag definitions.
	client, err := gate.DefaultClient()
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	ctx, stop := context.WithTimeout(context.Background(), 30*time.Second)
	defer stop()
	wg := &sync.WaitGroup{}
	for i := 0; i < 5; i++ {
		idx := i
		go func() {
			wg.Add(1)
			defer wg.Done()
			err := client.AutoRegister(ctx, &gate.RegisterRequest{
				Pattern:   "/",
				FixedPort: uint32(8080 + idx),
			}, nil)
			if err != nil && !errors.Is(err, context.Cause(ctx)) {
				log.Printf("Error in registration #%v stopping all: %v", idx, err)
				stop()
			}
		}()
	}
	wg.Wait() // wait for all registrations to clean up
}

// Shows reading multiple times from the results channel, which you can use to
// do custom certificate handling for example with [gate.Lease.Certificate] or
// simply be notified of renewals.
//
// Import the [ask.systems/daemon/portal/flags] package to use [DefaultClient].
func ExampleClient_AutoRegister() {
	// This uses the default portal env vars and flag definitions.
	// import _ "ask.systems/daemon/portal/flags" to get the flag definitions.
	client, err := gate.DefaultClient()
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := context.WithTimeout(context.Background(), 30*time.Second)
	defer stop()

	results := make(chan *gate.AutoRegisterResult, 0) // closed by AutoRegister
	go func() {
		first := true
		for {
			select {
			case ret := <-results:
				if first {
					log.Printf("Lease for port %v obtained. Expires at: %v",
						ret.Lease.Port, ret.Lease.Timeout.AsTime())
					first = false
				} else {
					log.Print("Lease renewed now expires at:", ret.Lease.Timeout.AsTime())
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	err = client.AutoRegister(ctx, &gate.RegisterRequest{
		Pattern: "/test/",
	}, results)
	log.Print("AutoRegister stopped:", err)
}

// Shows how to use [ResolveFlags] to call [Connect] instead of the usual way of
// simply using [DefaultClient] which calls [ResolveFlags] internally.
//
// Import the [ask.systems/daemon/portal/flags] package to use [ResolveFlags].
func ExampleResolveFlags() {
	// This uses the default portal env vars and flag definitions.
	// import _ "ask.systems/daemon/portal/flags" to get the flag definitions.
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
