// Embedportal lets you run the portal binary main function inside another
// program
//
// This is used by [ask.systems/daemon], but feel free to use it if you want to!
package embedportal

import (
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	_ "ask.systems/daemon/tools/flags"

	"ask.systems/daemon/tools"
)

//go:generate protoc -I ../ ../embedportal/storage.proto --go_out ../ --go_opt=paths=source_relative
//go:generate protoc -I ../ ../gate/service.proto --go_out ../ --go-grpc_out ../ --go_opt=paths=source_relative --go-grpc_opt=paths=source_relative

const (
	rpcPort        = 2048
	portRangeStart = 2049
	portRangeEnd   = 4096
	leaseTTL       = 24 * time.Hour
)

func Run(flags *flag.FlagSet, args []string) {
	defaultHost := flags.String("default_hostname", "", ""+
		"Set this to the domain name that patterns registered without a hostname\n"+
		"should be served under. If unset, patterns without a hostname will match\n"+
		"requests for any hostname that arrives at the server.")
	tlsCertSpec := flags.String("tls_cert", "", ""+
		"The filepath to the tls cert file (fullchain.pem).\n"+
		"Accepts multiple certificates with a comma separated list.\n"+
		"This is not needed with spawn because it uses the SPAWN_FILES env var.")
	tlsKeySpec := flags.String("tls_key", "", ""+
		"The filepath to the tls key file (privkey.pem).\n"+
		"Accepts multiple keys with a comma separated list.\n"+
		"This is not needed with spawn because it uses the SPAWN_FILES env var.")
	autoTLSCerts := flags.Bool("auto_tls_certs", true, ""+
		"If true update the tls files when SIGUSR1 is received. The\n"+
		"-tls_cert and -tls_key paths must either both be file paths or both be\n"+
		"OS pipe fd numbers produced by the auto_tls_certs spawn config option.\n")
	certChallengeWebRoot := flags.String("cert_challenge_webroot", "./cert-challenge/", ""+
		"Set to a local folder path to enable hosting the let's encrypt webroot\n"+
		"challenge path ("+certChallengePattern+") so you can auto-renew with\n"+
		"certbot. Set to empty string to turn this off.")
	httpPort := flags.Int("http_port", 80, ""+
		"The port to bind to for http traffic.\n"+
		"This is overridden if spawn provides ports.")
	httpsPort := flags.Int("https_port", 443, ""+
		"The port to bind to for https traffic.\n"+
		"This is overridden if spawn provides ports.")
	saveFilepath := flags.String("save_file", "state.protodata", ""+
		"The path to the file to store active lease information in so that\n"+
		"the portal server can safely restart without disrupting proxy service.\n")
	flags.Parse(args[1:])

	quit := make(chan struct{})
	tools.CloseOnQuitSignals(quit)

	serveCert, err := loadTLSConfig(
		strings.Split(*tlsCertSpec, ","),
		strings.Split(*tlsKeySpec, ","),
		*autoTLSCerts, quit)
	if err != nil {
		log.Fatalf("failed to load TLS config: %v", err)
	}

	httpListener, httpsListener, err := openWebListeners(*httpPort, *httpsPort)
	if err != nil {
		log.Fatalf("%v", err)
	}

	// Load the previous save data from the file before we overwrite it
	saveData, err := os.ReadFile(*saveFilepath)
	if err != nil {
		log.Print("No save data: ", err)
	}

	state := newStateManager(*saveFilepath)
	onCertRenew := func(cert *tls.Certificate) {
		if err := state.NewRootCA(cert.Certificate[0]); err != nil {
			log.Print("Error saving new root CA, new backend connections may not work: ", err)
		}
	}

	rootCert, err := tools.AutorenewSelfSignedCertificate("portal",
		10*leaseTTL, true /*isCA*/, onCertRenew, quit)
	if err != nil {
		log.Fatalf("Failed to create a self signed certificate for the RPC server: %v", err)
	}

	l := makeClientLeasor(portRangeStart, portRangeEnd, leaseTTL, quit)
	tcpProxy := startTCPProxy(l, serveCert, quit)
	httpProxy, err := startHTTPProxy(l, serveCert, rootCert,
		httpListener, httpsListener,
		*defaultHost, *certChallengeWebRoot,
		state, quit)
	log.Print("Started HTTP proxy server")
	if err != nil {
		log.Fatalf("Failed to start HTTP proxy server: %v", err)
	}

	_, err = startRPCServer(l,
		tcpProxy, httpProxy, rpcPort,
		rootCert, saveData, state, quit)
	log.Print("Started rpc server on port ", rpcPort)
	if err != nil {
		log.Fatal("Failed to start RPC server:", err)
	}

	<-quit // Wait for quit
}

func openWebListeners(httpPort, httpsPort int) (httpListener net.Listener, httpsListener net.Listener, err error) {
	// Read 2 ports passed in from spawn, in either order
	spawnPorts, _ := strconv.Atoi(os.Getenv("SPAWN_PORTS"))
	if spawnPorts > 0 {
		if fdListener, err := listenerFromPortOrFD(-3); err == nil {
			addr := fdListener.Addr().String()
			if strings.HasSuffix(addr, strconv.Itoa(httpPort)) {
				httpListener = fdListener
			}
			if strings.HasSuffix(addr, strconv.Itoa(httpsPort)) {
				httpsListener = fdListener
			}
		}
	}
	if spawnPorts > 1 {
		if fdListener, err := listenerFromPortOrFD(-4); err == nil {
			addr := fdListener.Addr().String()
			if strings.HasSuffix(addr, strconv.Itoa(httpPort)) {
				httpListener = fdListener
			}
			if strings.HasSuffix(addr, strconv.Itoa(httpsPort)) {
				httpsListener = fdListener
			}
		}
	}

	// If we didn't get passed in ports from spawn try just listening ourselves
	if httpListener == nil {
		httpListener, err = listenerFromPortOrFD(httpPort)
		if err != nil {
			return nil, nil, fmt.Errorf("Failed to listen on http port (%v): %v", httpPort, err)
		}
	}
	if httpsListener == nil {
		httpsListener, err = listenerFromPortOrFD(httpsPort)
		if err != nil {
			return nil, nil, fmt.Errorf("Failed to listen on https port (%v): %v", httpsPort, err)
		}
	}
	return httpListener, httpsListener, nil
}

func listenerFromPortOrFD(portOrFD int) (net.Listener, error) {
	if portOrFD < 0 {
		fdFile := os.NewFile(uintptr(-portOrFD), "fd")
		if fdFile == nil {
			return nil, fmt.Errorf("file descriptor %v is not valid.", -portOrFD)
		}
		return net.FileListener(fdFile)
	}
	return net.Listen("tcp", fmt.Sprintf(":%v", portOrFD))
}
