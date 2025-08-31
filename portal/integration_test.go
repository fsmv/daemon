package main_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto"
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ask.systems/daemon/portal/embedportal"
	"ask.systems/daemon/portal/gate"
	"ask.systems/daemon/tools"
)

// TODO: another top level Test function that checks if restarting portal
// properly restores state. Use a longer TTL for that one.
//
// That way we cover testing that portal saves the state file without actually
// having to read the state file in the integration test

// TODO: test getting multiple results using the client.AutoRegister function
// directly. Also test passing in a nil result channel.

// TODO: Should I test a TCP registration and do a full TCP server like I did
// with HTTP? Is this testing portal or gate? I guess both...
//
// Can use FreePort to get the external port to register on portal (as well as
// the internal one) but we would have to close it and hope nobody listens on it
// to snipe it from us. We can't pass the FD to portal over gRPC.
//
// Should we test non-FixedPort leases just because coverage?

// TODO: I guess we should add a check that the non-FixedPort tests actually get
// a port in the configured range

// TODO: test the deprecated client functions?

// TODO: test starting AutoRegister before portal starts

// TODO: More thorough portal testing
//  - When restarting we still accept the old root CA and the new one at the
//    same time
//  - port_leasor not issuing ports that conflict with FixedPort registrations
//    maybe this needs to be a embedportal package unit test using internal
//    functions
//    - Also test per-client leases probably with internal function tests
//  - Amazing crash tests for atomic state file writing???
//  - AllowHTTP and HTTP redirecting
//  - All the X-Forwarded headers
//  - The default host flag and setting the host in the pattern request
//  - The hostname feature of RegisterRequest
//  - More explicit StripPattern than sneaking it into other tests?
//  - Not using CertificateRequest
//  - Unit testing for gate.ResolveFlags() (gate_test.go)
//  - Unit testing for gate.ParsePattern() (gate_test.go)

func init() {
	log.SetFlags(log.Ltime | log.Lshortfile)
}

func call(method reflect.Method, args ...any) []reflect.Value {
	reflectArgs := make([]reflect.Value, len(args))
	for i, arg := range args {
		reflectArgs[i] = reflect.ValueOf(arg)
	}
	return method.Func.Call(reflectArgs)
}

func Subtests(t *testing.T, receiver any) {
	tests := reflect.TypeOf(receiver)
	count := tests.NumMethod()
	for i := 0; i < count; i++ {
		test := tests.Method(i)
		t.Run(test.Name, func(st *testing.T) {
			call(test, receiver, st)
		})
	}
}

// This needs to be run before any t.Cleanup() operations you do in your
// function so that Cleanup from this function runs after any goroutines from
// the test are shut down and waited on.
func CaptureTokenFromLogs(t *testing.T) <-chan string {
	t.Helper() // so the t.Error()s count for the test calling this func
	ret := make(chan string)
	wait := make(chan struct{})

	// Capture the log.Print statements from portal (and everything else)
	logs, logWriter := io.Pipe()
	origOutput := log.Writer()
	log.SetOutput(io.MultiWriter(origOutput, logWriter))
	t.Cleanup(func() {
		log.SetOutput(origOutput)
		logWriter.Close()
		<-wait
	})

	go func() {
		done := false
		s := bufio.NewScanner(logs)
		for s.Scan() {
			if done {
				continue // need to read the logs still to not block log statements
			}

			// Note: parsing copied from
			// spawn/embedspawn/children.go:waitForPortalToken()
			// TODO: how can this be deduplicated? Maybe there should be a standard
			// way to get the token from portal somehow.
			line := s.Text()
			const prefix = "Portal API token: "
			if idx := strings.Index(line, prefix); idx == -1 {
				continue
			} else {
				token := line[idx+len(prefix):]
				endIdx := strings.IndexRune(token, ' ')
				if endIdx == -1 {
					t.Errorf(
						"Failed to parse portal token line: %#v", line)
				}
				token = token[:endIdx]
				ret <- token
				close(ret)
				done = true
			}
		}
		if err := s.Err(); err != nil {
			t.Error("Failed reading portal logs:", err)
		}
		if !done {
			t.Error("Never received portal token!")
		}
		logs.Close()
		close(wait)
	}()

	return ret
}

type PortalTest struct {
	RPCPort   int
	HTTPPort  int
	HTTPSPort int
}

func TestPortal(t *testing.T) {
	token := CaptureTokenFromLogs(t)

	subtests, portArgs := PortalPorts(t)

	fs := flag.NewFlagSet(t.Name(), flag.PanicOnError)
	embedportal.LeaseTTL = 2 * time.Second
	wg := &sync.WaitGroup{}
	wg.Add(1)
	t.Cleanup(wg.Wait) // Needs to be after PortalToken(t)
	go func() {
		embedportal.Run(t.Context(), fs,
			append([]string{
				"portal",
				// These are actually unused because we always use FreePort(t) and
				// FixedPort registrations for any tests that start servers.
				"-port_range_start=9000",
				"-port_range_end=9999",
				"-save_file=",
			}, portArgs...))
		wg.Done()
	}()
	gate.Address, gate.Token = new(string), new(string)
	*gate.Address = fmt.Sprintf("127.0.0.1:%v", subtests.RPCPort)
	*gate.Token = <-token

	// Run all the methods of *PortalTests as subtests
	Subtests(t, subtests)
}

// Test [gate.AutoRegister] by checking that we never drop a full reverse
// proxied HTTP request over a period of multiple lease renewals.
func (p *PortalTest) AutoRegister_HTTPProxy(t *testing.T) {
	t.Parallel()
	const waitForRenewals = 2 // controls the length of the test

	pattern := fmt.Sprintf("/%v/", t.Name())
	port, listener, _ := FreePort(t)

	regctx, killAutoRegister := context.WithCancel(t.Context())
	ret, waitAutoRegister, err := gate.AutoRegister(regctx, &gate.RegisterRequest{
		Pattern:   pattern,
		FixedPort: uint32(port),
	})
	if err != nil {
		t.Error(err)
	}

	// Reset the handlers
	mux := http.NewServeMux()
	srv := &http.Server{Handler: mux}
	got_count := &atomic.Int32{}
	mux.HandleFunc(pattern, func(w http.ResponseWriter, req *http.Request) {
		got_count.Add(1)
		w.WriteHeader(http.StatusOK)
	})

	httpctx, killHTTP := context.WithCancel(t.Context())
	waitHTTP := make(chan struct{})
	go func() {
		err := tools.HTTPServer(httpctx.Done(), ret.Lease.Port, ret.TLSConfig, &tools.HTTPServerOptions{
			Server:          srv,
			ShutdownTimeout: time.Second,
			Listener:        listener,
		})
		if err != nil {
			t.Error(err)
		}
		close(waitHTTP)
	}()

	// Portal uses a self-signed cert since we're not testing ACME
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		},
	}
	url := fmt.Sprintf("https://127.0.0.1:%v%v", p.HTTPSPort, pattern)
	tick := time.Tick(time.Second / 10)
	now := time.Now()
	end := now.Add(embedportal.LeaseTTL * waitForRenewals)
	want_count := int32(0)
	for !time.Now().After(end) {
		<-tick
		resp, err := client.Get(url)
		want_count += 1
		if err != nil {
			t.Error(err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Error("Bad HTTP Status: ", resp.Status)
		}
	}

	killAutoRegister()
	<-waitAutoRegister

	// Check that we unregistered
	resp, err := client.Get(url)
	if err != nil {
		t.Error(err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Error("Still serving requests after unregister:", resp.Status)
	}

	killHTTP()
	<-waitHTTP

	if got_count.Load() != want_count {
		t.Errorf("Got %v proxied HTTP requests. Wanted %v.", got_count.Load(), want_count)
	}
}

// Check that [gate.AutoRegister] can recover the registration even if portal
// looses track of the lease entirely.
//
// This can happen if for example the client machine loses its internet
// connection, the lease expires on the portal server, and then the internet
// comes back on. In that scenario AutoRegister should recover.
func (p *PortalTest) AutoRegister_HTTPRecovery(t *testing.T) {
	t.Parallel()
	const waitForRenewals = 2 // controls the length of the test

	pattern := fmt.Sprintf("/%v/", t.Name())
	port, listener, _ := FreePort(t)

	regctx, killAutoRegister := context.WithCancel(t.Context())
	ret, waitAutoRegister, err := gate.AutoRegister(regctx, &gate.RegisterRequest{
		Pattern:      pattern,
		StripPattern: true,
		FixedPort:    uint32(port),
	})
	if err != nil {
		t.Error(err)
	}

	mux := http.NewServeMux()
	srv := &http.Server{Handler: mux}
	gotCount := &atomic.Int32{}
	mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		gotCount.Add(1)
		w.WriteHeader(http.StatusOK)
	})

	httpctx, killHTTP := context.WithCancel(t.Context())
	waitHTTP := make(chan struct{})
	go func() {
		err := tools.HTTPServer(httpctx.Done(), ret.Lease.Port, ret.TLSConfig, &tools.HTTPServerOptions{
			Server:          srv,
			ShutdownTimeout: time.Second,
			Listener:        listener,
		})
		if err != nil {
			t.Error(err)
		}
		close(waitHTTP)
	}()

	// Portal uses a self-signed cert since we're not testing ACME
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		},
	}
	url := fmt.Sprintf("https://127.0.0.1:%v%v", p.HTTPSPort, pattern)
	portal, err := gate.DefaultClient()
	if err != nil {
		t.Error(err)
	}

	// Make sure it works the first time
	resp, err := client.Get(url)
	if err != nil {
		t.Error(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Error("Bad HTTP Status: ", resp.Status)
	}
	if count := gotCount.Load(); count != 1 {
		t.Error("Expected 1 HTTP request, got: ", count)
	}
	// Force an unregister, so Renew would not work.
	if _, err := portal.RPC.Unregister(t.Context(), ret.Lease); err != nil {
		t.Error(err)
	}
	// Wait until we get a successful HTTP request to our backend
	tick := time.Tick(time.Second / 10)
	for done := false; !done; {
		select {
		case <-t.Context().Done():
			t.Error("AutoRegister failed to recover the registration in time:", t.Context().Err())
			done = true
			continue
		case <-tick:
		}
		resp, err := client.Get(url)
		if err != nil {
			t.Error(err)
		} else {
			if resp.StatusCode == http.StatusOK {
				done = true
			}
		}
	}

	killAutoRegister()
	<-waitAutoRegister

	// Check that we unregistered
	resp, err = client.Get(url)
	if err != nil {
		t.Error(err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Error("Still serving requests after unregister:", resp.Status)
	}

	killHTTP()
	<-waitHTTP

	if count := gotCount.Load(); count != 2 {
		t.Error("Expected 2 HTTP requests, got: ", count)
	}
}

func equalCerts(lhs, rhs [][]byte) bool {
	if len(lhs) != len(rhs) {
		return false
	}
	for i := 0; i < len(lhs); i++ {
		if !bytes.Equal(lhs[i], rhs[i]) {
			return false
		}
	}
	return true
}

// Test the auto renew [tls.Config] result feature of [gate.AutoRegister].
// Validates the certificate returned by portal and waits until the certificate
// has been renewed and updated in the config at least once.
//
// Technically the HTTP test covers this because portal does validate client
// certs during proxied requests but this is much more direct.
func (*PortalTest) AutoRegister_ClientCert(t *testing.T) {
	t.Parallel()
	ctx, done := context.WithDeadline(t.Context(), time.Now().Add(15*time.Second))
	ret, wait, err := gate.AutoRegister(ctx, &gate.RegisterRequest{
		Pattern: fmt.Sprintf("/%v/", t.Name()),
	})
	if err != nil {
		t.Fatal(err)
	}

	refreshed := false
	cert, err := ret.TLSConfig.GetCertificate(nil)
	if err != nil {
		t.Error(err)
	}
	// For the internal parseAndValidateCert function
	if _, err := tools.TLSCertificateFromBytes(cert.Certificate, cert.PrivateKey.(crypto.Signer)); err != nil {
		t.Error(err)
	}
	tick := time.Tick(time.Second / 10)
	run := true
	for run {
		select {
		case <-ctx.Done():
			run = false
			break
		case <-tick:
		}
		newCert, err := ret.TLSConfig.GetCertificate(nil)
		if err != nil {
			t.Error(err)
		}
		if _, err := tools.TLSCertificateFromBytes(newCert.Certificate, newCert.PrivateKey.(crypto.Signer)); err != nil {
			t.Error(err)
		}
		if !equalCerts(cert.Certificate, newCert.Certificate) {
			refreshed = true
			done()
			break
		}
	}

	<-wait
	if !refreshed {
		t.Error("The certificate was not renewed in time.")
	}
}

// Check that the [gate.AutoRegister] wait mechanism works
func (*PortalTest) AutoRegister_Wait(t *testing.T) {
	t.Parallel()
	const testTime = time.Second
	start := time.Now()
	ctx, _ := context.WithTimeout(t.Context(), testTime)
	_, wait, err := gate.AutoRegister(ctx, &gate.RegisterRequest{
		Pattern: fmt.Sprintf("/%v/", t.Name()),
	})
	if err != nil {
		t.Error(err)
	}

	<-wait
	if t.Context().Err() == nil && time.Since(start) < testTime {
		t.Error("wait exited earlier than it should.")
	}
}
