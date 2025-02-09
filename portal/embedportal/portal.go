// Embedportal lets you run the portal binary main function inside another
// program
//
// This is used by [ask.systems/daemon], but feel free to use it if you want to!
package embedportal

import (
	"crypto/tls"
	"errors"
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
	leaseTTL         = 24 * time.Hour
	ttlRandomStagger = 0.05
)

func Run(flags *flag.FlagSet, args []string) {
	rpcPort := flags.Uint("rpc_port", 2048, ""+
		"The port to bind for the portal RPC server that clients use to register\n"+
		"with. You shouldn't need to change this unless there's a conflict or you\n"+
		"run multiple instances of portal.")
	portRangeStart := flags.Uint("port_range_start", 2050, ""+
		"The (inclusive) start of the port range to lease-out to clients when they\n"+
		"register.")
	portRangeEnd := flags.Uint("port_range_end", 4096, ""+
		"The (inclusive) end of the port range to lease-out to clients when they\n"+
		"register. A separate list of of used ports is kept per-backend-IP.\n")
	reservedPorts := portList(make(map[uint16]bool))
	flags.Var(&reservedPorts, "reserved_ports", ""+
		"A comma separated list of ports (in the port_range) that should not be\n"+
		"issued to clients. Use this if you have non-portal-client services\n"+
		"running on ports in the range.")
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
	// Note: these are signed ints because of the -fd feature
	// see: listenerFromPortOrFD
	// TODO: maybe take it out for flags or document it
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
	var saveData []byte
	if *saveFilepath != "" {
		saveData, err = os.ReadFile(*saveFilepath)
		if err != nil {
			log.Print("No save data: ", err)
		}
	}

	state := newStateManager(*saveFilepath)
	onCertRenew := func(cert *tls.Certificate) {
		if err := state.NewRootCA(cert.Certificate[0]); err != nil {
			log.Print("Error saving new root CA, new backend connections may not work: ", err)
		} else {
			log.Print("Renewed root CA cert.")
		}
	}

	rootCert, err := tools.AutorenewSelfSignedCertificate("portal",
		10*leaseTTL, true /*isCA*/, onCertRenew, quit)
	if err != nil {
		log.Fatalf("Failed to create a self signed certificate for the RPC server: %v", err)
	}

	l := makeClientLeasor(uint16(*portRangeStart), uint16(*portRangeEnd), reservedPorts, quit)
	tcpProxy := startTCPProxy(l, serveCert, quit)
	httpProxy, err := makeHTTPProxy(l, serveCert, rootCert,
		httpListener, httpsListener,
		*defaultHost, *certChallengeWebRoot,
		state)

	// Note: State file gets loaded here (because only the RPC server knows how to
	// register both the TCP and HTTP proxies, and the state stores RPC requests)
	//
	// This needs to go last because it prints the token string spawn looks for
	_, err = startRPCServer(l,
		tcpProxy, httpProxy, uint16(*rpcPort),
		rootCert, saveData, state, quit)
	log.Print("Started rpc server on port ", *rpcPort)
	if err != nil {
		log.Fatal("Failed to start RPC server:", err)
	}

	httpProxy.Start(quit)
	log.Print("Started HTTP proxy server")
	if err != nil {
		log.Fatalf("Failed to start HTTP proxy server: %v", err)
	}

	<-quit // Wait for quit
}

type portList map[uint16]bool

func (l portList) String() string {
	var ret strings.Builder
	first := true
	for port, _ := range l {
		if !first {
			ret.WriteString(", ")
		} else {
			first = false
		}
		ret.WriteString(strconv.Itoa(int(port)))
	}
	return ret.String()
}

func (l portList) Set(in string) error {
	if l == nil {
		return errors.New("nil map in portList flag")
	}
	for in != "" {
		var portStr string
		portStr, in, _ = strings.Cut(in, ",")
		port, err := strconv.ParseUint(strings.TrimSpace(portStr), 10, 16)
		if err != nil {
			return err
		}
		l[uint16(port)] = true
	}
	return nil
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
