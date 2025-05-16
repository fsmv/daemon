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
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	_ "ask.systems/daemon/tools/flags"
	"golang.org/x/crypto/acme"

	"ask.systems/daemon/tools"
)

//go:generate protoc -I ../ ../embedportal/storage.proto --go_out ../ --go_opt=paths=source_relative
//go:generate protoc -I ../ ../gate/service.proto --go_out ../ --go-grpc_out ../ --go_opt=paths=source_relative --go-grpc_opt=paths=source_relative

const (
	leaseTTL         = 24 * time.Hour
	ttlRandomStagger = 0.05
)

var kACMEAddress string

func Run(flags *flag.FlagSet, args []string) {

	flags.Usage = func() {
		fmt.Fprintf(flags.Output(), ""+
			"Usage: %s [flags]\n"+
			"Portal is a reverse proxy HTTPS server configured via gRPC.\n"+
			"\n"+
			"If you use no options portal will use a self-signed TLS certificate\n"+
			"for external clients. To automatically obtain an authoritative TLS\n"+
			"certificate, list the domains and subdomains in -autocert_domains:\n"+
			"\n"+
			"    %s -autocert_domains='example.com,test.example.com'\n"+
			"\n"+
			"If you use multiple domains, consider setting -default_hostname.\n"+
			"Clients that register '/example/' as a reverse proxy pattern will by\n"+
			"default serve on both example.com and test.example.com, leaving it to\n"+
			"the backends to decide to respond or not. By setting\n"+
			"-default_hostname=example.com portal will not send requests for\n"+
			"test.example.com/example/ to the backend that registered '/example/'.\n"+
			"Note: the above url can also be registered to as a pattern separately.\n"+
			"\n"+
			"Lastly the -reserved_ports flag may be useful if you're running\n"+
			"non-portal-client servers in the ports 2050-4096 (by default).\n"+
			"The other flags only need to be changed in unusual configurations.\n"+
			"\n"+
			"All Flags:\n",
			flags.Name(), flags.Name())
		flags.PrintDefaults()
	}
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
	var domains autocertDomains
	flags.Var(&domains, "autocert_domains", ""+
		"A comma separated list of domain names to automatically obtain TLS\n"+
		"certificates for. By default provided freely by https://letsencrypt.org.\n"+
		"By using this feature you accept their TOS.\n\n"+
		"If you use this you don't need to set any other tls or cert related flags.")
	acmeAddress := flags.String("autocert_server", acme.LetsEncryptURL, ""+
		"The ACME server directory URL to use when automatically obtaining TLS\n"+
		"certificates. This is uncommon. There are alternate providers, private\n"+
		"servers, and this is useful for testing. For testing only you can also\n"+
		"set the ACME_SERVER_CERT env var to a PEM encoded CA cert file for\n"+
		"connecting to the autocert server over HTTPS.\n")
	tlsCertSpec := flags.String("tls_cert", "", ""+
		"The filepath to the tls cert file (fullchain.pem).\n"+
		"Accepts multiple certificates with a comma separated list.\n"+
		"Files are automatically re-read when portal receives SIGUSR1\n"+
		"or 2/3 of the expiration date.\n"+
		"This is not needed with spawn because it uses the SPAWN_FILES env var.")
	tlsKeySpec := flags.String("tls_key", "", ""+
		"The filepath to the tls key file (privkey.pem).\n"+
		"Accepts multiple keys with a comma separated list.\n"+
		"Files are automatically re-read when portal receives SIGUSR1\n"+
		"or 2/3 of the expiration date.\n"+
		"This is not needed with spawn because it uses the SPAWN_FILES env var.")
	// TODO: default to empty string eventually?
	// I want to so that we don't make a folder we don't need, but that might
	// break people's configs if they're not using autocert_domains yet.
	certChallengeWebRoot := flags.String("cert_challenge_webroot", "./cert-challenge/", ""+
		"Set to a local folder path to enable hosting the webroot auto TLS cert\n"+
		"(ACME) challenge path ("+certChallengePattern+") so you can auto-renew\n"+
		"with certbot or other clients. Set to empty string to turn this off.\n")
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
	kACMEAddress = *acmeAddress

	quit := make(chan struct{})
	tools.CloseOnQuitSignals(quit)

	httpListener, httpsListener, err := openWebListeners(*httpPort, *httpsPort)
	if err != nil {
		log.Fatalf("%v", err)
	}

	state := newStateManager(*saveFilepath)
	if err := state.Load(); err != nil {
		log.Print("Failed to load state: ", err)
	} else {
		log.Print("Successfully loaded state.")
	}

	// Set up the new CA root cert for signing API client TLS certs
	onCertRenew := func(cert *tls.Certificate) {
		if err := state.SaveRootCA(cert.Certificate[0]); err != nil {
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

	challenges := &acmeChallenges{}
	leasor := makeClientLeasor(uint16(*portRangeStart), uint16(*portRangeEnd), reservedPorts, quit)

	httpProxy, err := makeHTTPProxy(leasor, rootCert,
		httpListener, httpsListener,
		*defaultHost, challenges, *certChallengeWebRoot,
		state)
	if err != nil {
		log.Fatalf("Failed to start HTTP proxy server: %v", err)
	}
	// Start HTTP first for cert challenges
	httpProxy.StartHTTP(quit)
	log.Print("Started HTTP proxy server")

	// Load the serving TLS certs.
	// This may cause acme cert challenges over HTTP.
	serveCert, err := loadTLSConfig(
		strings.Split(*tlsCertSpec, ","),
		strings.Split(*tlsKeySpec, ","),
		domains, challenges, state, quit)
	if err != nil {
		log.Fatalf("Failed to load TLS config: %v", err)
	} else {
		log.Print("Successfully loaded TLS config.")
	}

	// Doesn't actually do anything until there are registrations (there are no
	// ports to open if clients haven't requested any)
	tcpProxy := makeTCPProxy(leasor, serveCert, quit)

	// Starts serving the rpc server port.
	// First loads the registrations from the state into the two proxy servers.
	_, err = startRPCServer(leasor,
		tcpProxy, httpProxy, uint16(*rpcPort),
		rootCert, state, quit)
	if err != nil {
		log.Fatal("Failed to start RPC server:", err)
	} else {
		log.Print("Started rpc server on port ", *rpcPort)
	}

	// Wait until after we have loaded the registrations so we don't serve a bunch
	// of 404s during startup
	httpProxy.StartHTTPS(serveCert, quit)
	log.Print("Started HTTPS proxy server")

	// Spawn looks for this string to know when portal has started. So we need to
	// have the API port listening before we print this.
	//
	// If this changes you have to update the string in spawn so it can find it
	log.Printf("**** Portal API token: %v ****", state.Token())

	<-quit // Wait for quit
}

type autocertDomains []string

func (l *autocertDomains) String() string {
	var ret strings.Builder
	first := true
	for _, domain := range *l {
		if !first {
			ret.WriteString(", ")
		} else {
			first = false
		}
		ret.WriteString(domain)
	}
	return ret.String()
}

func (ret *autocertDomains) Set(in string) error {
	fields := strings.Split(in, ",")
	*ret = make([]string, len(fields))
	for i, field := range fields {
		trimmed := strings.TrimSpace(field)
		_, err := url.Parse(trimmed)
		if err != nil {
			return fmt.Errorf("failed to parse domain #%v: %w", i+1, err)
		}
		(*ret)[i] = trimmed
	}
	return nil
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
