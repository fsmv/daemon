// Embedportal lets you run the portal binary main function inside another
// program
//
// This is used by [ask.systems/daemon], but feel free to use it if you want to!
package embedportal

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
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

func openFilePathOrFD(pathOrFD string) (*os.File, error) {
	if fd, err := strconv.Atoi(pathOrFD); err == nil {
		fdFile := os.NewFile(uintptr(fd), "fd")
		if fdFile == nil {
			return nil, fmt.Errorf("file descriptor %v is not valid.", fd)
		}
		return fdFile, nil
	}
	return os.Open(pathOrFD)
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

type tlsRefresher struct {
	cache []*atomic.Value
	quit  chan struct{}
}

func startTLSRefresher(tlsCert, tlsKey []*os.File, quit chan struct{}) *tlsRefresher {
	t := &tlsRefresher{
		quit:  quit,
		cache: make([]*atomic.Value, len(tlsCert)),
	}
	if len(tlsCert) != len(tlsKey) {
		log.Fatal("-tls_cert and -tls_key must have the same number of entries.")
	}
	for i := 0; i < len(tlsCert); i++ {
		t.cache[i] = &atomic.Value{}
		cert := tlsCert[i]
		key := tlsKey[i]
		pipeFiles := false
		if cert.Name() != key.Name() && (cert.Name() == "fd" || key.Name() == "fd") {
			log.Fatalf("Entry #%v: -tls_cert and -tls_key must being either both paths or both OS pipes for -auto_tls_certs.", i)
		} else if cert.Name() == "fd" && key.Name() == "fd" {
			pipeFiles = true
		}
		go t.refreshCert(i, cert, key, pipeFiles)
	}
	return t
}

func (t *tlsRefresher) refreshCert(idx int, cert, key *os.File, pipes bool) {
	var certScanner, keyScanner *bufio.Scanner
	if pipes {
		certScanner = bufio.NewScanner(cert)
		certScanner.Split(scanEOT)
		keyScanner = bufio.NewScanner(key)
		keyScanner.Split(scanEOT)
	} else {
		// Close the files because we will reopen in the refresh loop
		cert.Close()
		key.Close()
	}

	sig := make(chan os.Signal, 1)
	sig <- syscall.SIGUSR1 // Do the first load immidately (store in the chan buffer)
	signal.Notify(sig, syscall.SIGUSR1)

	// Close in a separete go routine in case we're blocked on pipe read
	go func() {
		<-t.quit
		signal.Stop(sig)
		close(sig)
		cert.Close()
		key.Close()
	}()
	for {
		select {
		case <-t.quit:
			return
		case <-sig:
			log.Printf("Starting TLS certificate refresh #%v...", idx+1)
			var newCert *tls.Certificate
			var err error
			if !pipes { // Handle reopening by filename if we aren't doing pipes
				newCertFile, err := os.Open(cert.Name())
				if err != nil {
					log.Printf("Failed to reopen TLS cert for refresh #%v: %v", idx+1, err)
					newCertFile.Close()
					continue
				}
				cert = newCertFile
				newKeyFile, err := os.Open(key.Name())
				if err != nil {
					log.Printf("Failed to reopen TLS key for refresh #%v: %v", idx+1, err)
					newCertFile.Close()
					newKeyFile.Close()
					continue
				}
				key = newKeyFile
				newCert, err = loadTLSCertFiles(cert, key) // closes the files
			} else {
				newCert, err = loadTLSCertScanner(certScanner, keyScanner)
			}
			if err != nil {
				log.Printf("Failed to load TLS cert for refresh #%v: %v", idx+1, err)
				continue
			}
			t.cache[idx].Store(newCert)
			log.Printf("Sucessfully refreshed TLS certificate #%v.", idx+1)
		}
	}
}

func (t *tlsRefresher) GetCertificate(hi *tls.ClientHelloInfo) (*tls.Certificate, error) {
	for _, c := range t.cache {
		cert := c.Load().(*tls.Certificate)
		if cert == nil {
			return nil, errors.New("Internal error: cannot load certificate")
		}
		if err := hi.SupportsCertificate(cert); err == nil {
			return cert, nil
		}
	}
	return nil, errors.New("No supported certificate.")
}

func scanEOT(data []byte, atEOF bool) (advance int, token []byte, err error) {
	// If we found EOF return everything
	if atEOF {
		return len(data), data, nil
	}
	// If we found an EOT, return everything up to it
	if i := bytes.IndexByte(data, '\x04'); i >= 0 {
		return i + 1, data[0:i], nil
	}
	return 0, nil, nil // request more data
}

func loadTLSCertScanner(tlsCert, tlsKey *bufio.Scanner) (*tls.Certificate, error) {
	tlsCert.Scan()
	if err := tlsCert.Err(); err != nil {
		return nil, err
	}
	tlsKey.Scan()
	if err := tlsKey.Err(); err != nil {
		return nil, err
	}
	certBytes, keyBytes := tlsCert.Bytes(), tlsKey.Bytes()
	cert, err := tls.X509KeyPair(certBytes, keyBytes)
	if err != nil {
		// Try it the other way too in case they got mixed up
		forwardErr := err
		cert, err = tls.X509KeyPair(keyBytes, certBytes)
		if err != nil {
			return nil, fmt.Errorf("invalid TLS file format: %v", forwardErr)
		}
	}
	return &cert, nil
}

func loadTLSCertFiles(tlsCert, tlsKey *os.File) (*tls.Certificate, error) {
	defer tlsCert.Close()
	defer tlsKey.Close()
	certBytes, err := io.ReadAll(tlsCert)
	if err != nil {
		return nil, fmt.Errorf("failed to read TLS cert file: %v", err)
	}
	keyBytes, err := io.ReadAll(tlsKey)
	if err != nil {
		return nil, fmt.Errorf("failed to read TLS key file: %v", err)
	}
	cert, err := tls.X509KeyPair(certBytes, keyBytes)
	if err != nil {
		// Try it the other way too in case they got mixed up
		forwardErr := err
		cert, err = tls.X509KeyPair(keyBytes, certBytes)
		if err != nil {
			return nil, fmt.Errorf("invalid TLS file format: %v", forwardErr)
		}
	}
	return &cert, nil
}

func loadTLSConfig(tlsCertSpec, tlsKeySpec []string,
	autoTLSCerts bool, quit chan struct{}) (*tls.Config, error) {
	if len(tlsCertSpec) != len(tlsKeySpec) {
		log.Fatal("-tls_cert and -tls_key must have the same number of entries.")
	}

	// Open the files from the flags
	var tlsCert, tlsKey []*os.File
	for i := 0; i < len(tlsCertSpec); i++ {
		if tlsCertSpec[i] == "" && tlsKeySpec[i] == "" {
			continue // strings.Split returns this unfortunately
		}
		if cert, err := openFilePathOrFD(tlsCertSpec[i]); err != nil {
			return nil, fmt.Errorf("Failed to load TLS cert file (%v): %w",
				tlsCertSpec[i], err)
		} else {
			tlsCert = append(tlsCert, cert)
		}
		if key, err := openFilePathOrFD(tlsKeySpec[i]); err != nil {
			return nil, fmt.Errorf("Failed to load TLS key file (%v): %w",
				tlsKeySpec[i], err)
		} else {
			tlsKey = append(tlsKey, key)
		}
	}

	// Try opening certs passed in by spawn
	spawnPorts, _ := strconv.Atoi(os.Getenv("SPAWN_PORTS"))
	spawnFiles, _ := strconv.Atoi(os.Getenv("SPAWN_FILES"))
	startFD := 3 + spawnPorts // 3 is stdin, stdout, stderr
	numFD := 3 + spawnPorts + spawnFiles
	if (numFD-startFD)%2 == 0 { // must have pairs of files for cert and key
		for fd := startFD; fd < numFD; fd += 2 {
			cert, err := openFilePathOrFD(strconv.Itoa(fd))
			if err != nil {
				break
			}
			if _, err := cert.Stat(); err != nil {
				cert.Close()
				break
			}
			key, err := openFilePathOrFD(strconv.Itoa(fd + 1))
			if err != nil {
				cert.Close()
				break
			}
			if _, err := key.Stat(); err != nil {
				cert.Close()
				break
			}
			tlsCert = append(tlsCert, cert)
			tlsKey = append(tlsKey, key)
		}
	} else {
		log.Print("Warning: spawn passed in an odd number of files, cert and key files must come in pairs.")
	}

	// If there was no certificate we could load, use a self-signed cert
	if len(tlsCert) == 0 {
		log.Printf("Warning: no TLS certificate loaded. Using a self-signed certificate.")
		return tools.AutorenewSelfSignedCertificate( /*hostname*/ "*",
			24*time.Hour, false /*isCA*/, nil /*onRenew*/, quit)
	}

	if autoTLSCerts {
		refresher := startTLSRefresher(tlsCert, tlsKey, quit)
		return &tls.Config{
			GetCertificate: refresher.GetCertificate,
		}, nil
	}
	// No auto refresh requested
	conf := &tls.Config{}
	for i := 0; i < len(tlsCertSpec); i++ {
		cert, err := loadTLSCertFiles(tlsCert[i], tlsKey[i])
		if err != nil {
			return nil, err
		}
		conf.Certificates = append(conf.Certificates, *cert)
	}
	return conf, nil
}
