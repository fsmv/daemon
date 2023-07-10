package embedportal

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"sync/atomic"
	"time"

	"ask.systems/daemon/tools"
)

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
		go t.keepCertRefreshed(i, cert, key, pipeFiles)
	}
	return t
}

func (t *tlsRefresher) refreshCert(idx int, cert, key *os.File, pipes bool) (*tls.Certificate, error) {
	log.Printf("Starting TLS certificate refresh #%v...", idx+1)
	var newCert *tls.Certificate
	var err error
	if !pipes { // Handle reopening by filename if we aren't doing pipes
		newCertFile, err := os.Open(cert.Name())
		if err != nil {
			log.Printf("Failed to reopen TLS cert for refresh #%v: %v", idx+1, err)
			newCertFile.Close()
			return nil, err
		}
		cert = newCertFile
		newKeyFile, err := os.Open(key.Name())
		if err != nil {
			log.Printf("Failed to reopen TLS key for refresh #%v: %v", idx+1, err)
			newCertFile.Close()
			newKeyFile.Close()
			return nil, err
		}
		key = newKeyFile
		newCert, err = loadTLSCertFiles(cert, key) // closes the files
	} else {
		certScanner := bufio.NewScanner(cert)
		certScanner.Split(scanEOT)
		keyScanner := bufio.NewScanner(key)
		keyScanner.Split(scanEOT)
		newCert, err = loadTLSCertScanner(certScanner, keyScanner)
	}
	if err != nil {
		log.Printf("Failed to load TLS cert for refresh #%v: %v", idx+1, err)
		return nil, err
	}
	t.cache[idx].Store(newCert)
	log.Printf("Sucessfully refreshed TLS certificate #%v.", idx+1)
	return newCert, nil
}

func (t *tlsRefresher) keepCertRefreshed(idx int, cert, key *os.File, pipes bool) {
	if !pipes {
		// Close the files because we will reopen in the refresh loop
		cert.Close()
		key.Close()
	}

	timer := time.NewTimer(time.Duration(0))
	sig := t.refreshSignal()

	refresh := func() {
		tlsCert, err := t.refreshCert(idx, cert, key, pipes)
		if err == nil && len(tlsCert.Certificate) > 0 {
			if tlsCert.Leaf == nil {
				tlsCert.Leaf, err = x509.ParseCertificate(tlsCert.Certificate[0])
			}
			// Try to refresh 1 hour before cert expiration
			if err != nil {
				timer.Reset(time.Until(tlsCert.Leaf.NotAfter) - time.Hour)
			}
		}
	}
	// Start the first refresh immediately
	refresh()

	// Close in a separate go routine in case we're blocked on pipe read
	go func() {
		<-t.quit
		cert.Close()
		key.Close()
	}()
	for {
		select {
		case <-t.quit:
			return
		case <-timer.C:
		case <-sig:
			refresh()
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
