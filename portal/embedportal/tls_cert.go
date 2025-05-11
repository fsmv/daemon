package embedportal

import (
	"bufio"
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"ask.systems/daemon/tools"
	"golang.org/x/crypto/acme"
)

type tlsRefresher struct {
	cache     []*atomic.Value
	quit      chan struct{}
	generator *acmeCertGenerator // nil if there were no acme certs to register
}

type acmeCertGenerator struct {
	AccountKey crypto.Signer
	Client     *acme.Client
	Account    *acme.Account
	Challenges *acmeChallenges
	acmeMut    sync.Mutex
	state      *stateManager
}

func (c *acmeCertGenerator) Certificate(domain string) (*tls.Certificate, error) {
	// obtainACMECert is a multi-step process over the network and it is confusing
	// if we do multiple certs at the same time. All of the cert memory storage is
	// thread safe but we want to do one network call chain at a time.
	c.acmeMut.Lock()
	defer c.acmeMut.Unlock()

	newCert, err := obtainACMECert(domain, c.Client, c.Account, c.Challenges)
	if err != nil {
		log.Printf("Error getting TLS cert for %v: %v", domain, err)
		return nil, err
	}
	log.Printf("Saving new TLS cert for %v.", domain)
	c.state.SaveTLSCert(domain, newCert)
	return newCert, nil
}

func makeCertGenerator(state *stateManager, challenges *acmeChallenges) (*acmeCertGenerator, error) {
	accountKey := state.ACMEAccount()
	if accountKey == nil {
		var err error
		accountKey, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, err
		}
		if err := state.SaveACMEAccount(accountKey); err != nil {
			return nil, err
		}
	}
	client := acmeClient(accountKey)
	account, err := fetchACMEAccount(client)
	if err != nil {
		return nil, err
	}
	return &acmeCertGenerator{
		Challenges: challenges,
		AccountKey: accountKey,
		Client:     client,
		Account:    account,
		state:      state,
	}, nil
}

func isPipeFile(files ...*os.File) bool {
	pipe := true
	for _, file := range files {
		pipe = pipe && (file.Name() == "fd")
	}
	return pipe
}

// Works for both direct tls cert files / pipes from spawn and auto acme certs
// via the generator. For auto acme certs initially loads from the stateManager
// into the cert cache.
func startTLSRefresher(
	tlsCert, tlsKey []*os.File,
	domains []string, challenges *acmeChallenges,
	state *stateManager, quit chan struct{}) *tlsRefresher {

	t := &tlsRefresher{
		quit:  quit,
		cache: make([]*atomic.Value, len(tlsCert)+len(domains)),
	}

	// Handle the direct cert files / pipes from spawn
	if len(tlsCert) != len(tlsKey) {
		log.Fatal("-tls_cert and -tls_key must have the same number of entries.")
	}
	for i := 0; i < len(tlsCert); i++ {
		t.cache[i] = &atomic.Value{}
		cert := tlsCert[i]
		key := tlsKey[i]
		if cert.Name() != key.Name() && (isPipeFile(cert) || isPipeFile(key)) {
			log.Fatalf("Entry #%v: -tls_cert and -tls_key must being either both paths or both OS pipes for -auto_tls_certs.", i)
		}

		var startCert *tls.Certificate
		var err error
		if !isPipeFile(cert, key) {
			startCert, err = loadTLSCertFiles(cert, key)
		} else {
			startCert, err = loadTLSCertScanner(bufio.NewScanner(cert), bufio.NewScanner(key))
			// Close the pipes on a separate goroutine on quit in-case we get blocked
			// on a pipe read
			go func() {
				<-t.quit
				cert.Close()
				key.Close()
			}()
		}
		if err != nil || startCert == nil {
			log.Fatalf("Failed to load tls cert and key entry #%v: %v", i, err)
		}
		idx := i // go loop variables are reused
		t.cache[idx].Store(startCert)
		go t.keepCertRefreshed(
			startCert,
			func() (*tls.Certificate, error) {
				return t.refreshCertFile(idx, cert, key)
			})
	}

	// Handle the auto acme certs
	if len(domains) > 0 {
		var err error
		// Note: This registers an acme account with the provider
		t.generator, err = makeCertGenerator(state, challenges)
		if err != nil {
			log.Fatalf("Failed to start acme cert generator: %v", err)
		}
	}
	for i, domain := range domains {
		idx := i + len(tlsCert)
		d := domain // go loop variables are reused
		t.cache[idx] = &atomic.Value{}
		startCert := state.TLSCert(d)
		if startCert == nil {
			var err error
			startCert, err = t.generator.Certificate(d)
			if err != nil || startCert == nil {
				log.Fatalf("Failed to get initial acme cert for %v: %v", d, err)
			}
		}
		t.cache[idx].Store(startCert)
		go t.keepCertRefreshed(
			startCert,
			func() (*tls.Certificate, error) {
				newCert, err := t.generator.Certificate(d)
				if err != nil {
					return nil, err
				}
				t.cache[idx].Store(newCert)
				return newCert, nil
			})
	}
	return t
}

func (t *tlsRefresher) refreshCertFile(idx int, cert, key *os.File) (*tls.Certificate, error) {
	log.Printf("Starting TLS certificate refresh #%v...", idx+1)
	var newCert *tls.Certificate
	var err error
	if !isPipeFile(cert, key) { // Handle reopening by filename if we aren't doing pipes
		newCertFile, errCert := os.Open(cert.Name())
		newKeyFile, errKey := os.Open(key.Name())
		if errCert != nil || errKey != nil {
			err := fmt.Errorf("Failed to reopen tls cert (%w) or key (%w) files", errCert, errKey)
			log.Print(err.Error())
			newCertFile.Close()
			newKeyFile.Close()
			return nil, err
		}
		newCert, err = loadTLSCertFiles(newCertFile, newKeyFile)
	} else {
		newCert, err = loadTLSCertScanner(bufio.NewScanner(cert), bufio.NewScanner(key))
	}
	if err != nil {
		log.Printf("Failed to load TLS cert for refresh #%v: %v", idx+1, err)
		return nil, err
	}
	if newCert.Leaf == nil {
		newCert.Leaf, err = x509.ParseCertificate(newCert.Certificate[0])
	}
	t.cache[idx].Store(newCert)
	log.Printf("Sucessfully refreshed TLS certificate #%v.", idx+1)
	return newCert, nil
}

func refreshTime(c *tls.Certificate) time.Duration {
	return time.Until(c.Leaf.NotAfter) * 2 / 3
}

// Assumes startCert is not nil and startCert.Leaf is not nil
func (t *tlsRefresher) keepCertRefreshed(startCert *tls.Certificate, refresh func() (*tls.Certificate, error)) {
	timer := time.NewTimer(refreshTime(startCert))
	sig := t.refreshSignal()

	for {
		select {
		case <-t.quit:
			return
		case <-timer.C:
		case <-sig:
			cert, err := refresh()
			if err != nil {
				log.Print("Error on cert refresh: ", err)
				// TODO: exponential backoff or something?
				timer.Reset(time.Minute)
			} else {
				timer.Reset(refreshTime(cert))
			}
		}
	}
}

func (t *tlsRefresher) GetCertificate(hi *tls.ClientHelloInfo) (*tls.Certificate, error) {
	for _, c := range t.cache {
		cert, ok := c.Load().(*tls.Certificate)
		if !ok || cert == nil {
			continue
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

// Like tls.X509KeyPair but it tries swapping the order of the key and cert
// And it sets the Leaf field on all go versions.
func certFromBytes(certBytes, keyBytes []byte) (*tls.Certificate, error) {
	cert, err := tls.X509KeyPair(certBytes, keyBytes)
	if err != nil {
		// Try it the other way too in case they got mixed up
		forwardErr := err
		cert, err = tls.X509KeyPair(keyBytes, certBytes)
		if err != nil {
			return nil, fmt.Errorf("invalid TLS file format: %v", forwardErr)
		}
	}
	// Set the Leaf value. Before Go 1.23 tls.X509KeyPair doesn't set this
	if cert.Leaf == nil && len(cert.Certificate) > 0 {
		leaf, err := x509.ParseCertificate(cert.Certificate[0])
		// There shouldn't be an error because tls.X509KeyPair does actually parse
		// and validate the cert and then it just discards the leaf.
		if err != nil {
			return nil, fmt.Errorf("failed to parse leaf cert: %w", err)
		}
		cert.Leaf = leaf
	}
	return &cert, nil
}

func loadTLSCertScanner(tlsCert, tlsKey *bufio.Scanner) (*tls.Certificate, error) {
	tlsCert.Split(scanEOT)
	tlsKey.Split(scanEOT)
	tlsCert.Scan()
	if err := tlsCert.Err(); err != nil {
		return nil, err
	}
	tlsKey.Scan()
	if err := tlsKey.Err(); err != nil {
		return nil, err
	}
	certBytes, keyBytes := tlsCert.Bytes(), tlsKey.Bytes()
	return certFromBytes(certBytes, keyBytes)
}

func loadTLSCertFiles(tlsCert, tlsKey *os.File) (*tls.Certificate, error) {
	defer tlsCert.Close()
	defer tlsKey.Close()
	certBytes, err := io.ReadAll(tlsCert)
	if err != nil {
		return nil, fmt.Errorf("failed to read TLS cert file: %w", err)
	}
	keyBytes, err := io.ReadAll(tlsKey)
	if err != nil {
		return nil, fmt.Errorf("failed to read TLS key file: %w", err)
	}
	return certFromBytes(certBytes, keyBytes)
}

func loadTLSConfig(
	tlsCertSpec, tlsKeySpec []string,
	domains []string, challenges *acmeChallenges,
	state *stateManager, quit chan struct{}) (*tls.Config, error) {
	if len(tlsCertSpec) != len(tlsKeySpec) {
		return nil, fmt.Errorf("-tls_cert and -tls_key must have the same number of entries.")
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
	if len(tlsCert) == 0 && len(domains) == 0 {
		log.Printf("Warning: no TLS certificate loaded. Using a self-signed certificate.")
		return tools.AutorenewSelfSignedCertificate( /*hostname*/ "*",
			3*24*time.Hour, false /*isCA*/, nil /*onRenew*/, quit)
	}

	refresher := startTLSRefresher(tlsCert, tlsKey, domains, challenges, state, quit)
	return &tls.Config{
		GetCertificate: refresher.GetCertificate,
	}, nil
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
