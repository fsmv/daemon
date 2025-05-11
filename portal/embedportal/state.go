package embedportal

import (
	"crypto"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"ask.systems/daemon/portal/gate"
	"ask.systems/daemon/tools"
	"google.golang.org/protobuf/proto"
)

func leaseKey(lease *gate.Lease) string {
	return fmt.Sprintf("%s:%d:%s", lease.Address, lease.Port, lease.Pattern)
}

type stateManager struct {
	mut          *sync.Mutex
	saveFilepath string

	registrations map[string]*Registration // leaseKey(lease) for the key

	// Store the rootCAs separately so we can write them to a file, because
	// unfortunately CertPool has no non-depracted methods to get the certs back.
	rootCAs map[*x509.Certificate]struct{}
	// This is what tls.Config expects, so we have to maintain this class
	// Unfortunately it is not thread safe
	mutCertPool *x509.CertPool
	// This is how we make it thread safe, we store clones of the CertPool here
	readCertPool *atomic.Value

	token *atomic.Value

	acmeAccount  crypto.Signer
	certificates map[string]*tls.Certificate // domain name key
}

func newStateManager(saveFilepath string) *stateManager {
	s := &stateManager{
		mut:          &sync.Mutex{},
		saveFilepath: saveFilepath,

		registrations: make(map[string]*Registration),

		rootCAs:      make(map[*x509.Certificate]struct{}),
		mutCertPool:  x509.NewCertPool(),
		readCertPool: &atomic.Value{},

		token:        &atomic.Value{},
		certificates: make(map[string]*tls.Certificate),
	}
	if s.saveFilepath == "" {
		log.Print("No save file, state is only saved in memory.")
	}
	return s
}

func (s *stateManager) Load() error {
	// Unmarshal the proto
	if s.saveFilepath == "" {
		return nil
	}
	saveData, err := os.ReadFile(s.saveFilepath)
	if err != nil {
		return fmt.Errorf("No save data: %w", err)
	}
	state := &State{}
	if err := proto.Unmarshal(saveData, state); err != nil {
		return fmt.Errorf("Failed to unmarshal save state file: %w", err)
	}
	s.mut.Lock()
	defer s.mut.Unlock()

	// Load the registrations
	for i, r := range state.Registrations {
		if err := s.unsafeSaveRegistration(r); err != nil {
			return fmt.Errorf("Error saving registration %v for %v: %w",
				i, r.GetRequest().GetPattern(), err)
		}
	}

	// Load the root CA certs
	loaded := 0
	for i, ca := range state.RootCAs {
		if err := s.unsafeSaveRootCA(ca); err != nil {
			return fmt.Errorf("Failed to save root CA #%v: %w", i, err)
		}
		loaded += 1
	}

	// Load the token
	if state.ApiToken == "" {
		state.ApiToken = tools.RandomString(32)
	}
	s.token.Store(state.ApiToken)

	// Load acme account key
	if len(state.AcmeAccount) > 0 {
		accountKeyAny, err := x509.ParsePKCS8PrivateKey(state.AcmeAccount)
		accountKey, ok := accountKeyAny.(crypto.Signer)
		if err != nil || !ok {
			return fmt.Errorf("Failed to load acme account key: %w", err)
		}
		s.acmeAccount = accountKey
	}

	// Load the acme auto certs
	for _, cert := range state.Certificates {
		certKeyAny, err := x509.ParsePKCS8PrivateKey(cert.Key)
		certKey, ok := certKeyAny.(crypto.Signer)
		if err != nil || !ok {
			return fmt.Errorf("Failed to load key for domain %v: %w", cert.Domain, err)
		}
		tlsCert, err := tools.TLSCertificateFromBytes(cert.Der, certKey)
		if err != nil {
			return fmt.Errorf("TLS cert for domain %v not valid: %w", cert.Domain, err)
		}
		s.certificates[cert.Domain] = tlsCert
	}

	return nil
}

func writeFileSync(name string, data []byte, perm os.FileMode) error {
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if errClose := f.Close(); errClose != nil && err == nil {
		return errClose
	}
	return err
}

func (s *stateManager) saveUnsafe() {
	if s.saveFilepath == "" {
		return
	}
	// Build the save state proto from the current in memory state
	state := &State{}

	for _, r := range s.registrations {
		state.Registrations = append(state.Registrations, r)
	}

	for ca, _ := range s.rootCAs {
		if time.Now().After(ca.NotAfter) {
			delete(s.rootCAs, ca)
			continue // Don't save expired root CAs
		}
		state.RootCAs = append(state.RootCAs, ca.Raw)
	}

	if token := s.token.Load(); token != nil {
		state.ApiToken = token.(string)
	}

	// TODO: better error handling in this function
	// Save the ACME Account
	if s.acmeAccount != nil {
		if accountBytes, err := x509.MarshalPKCS8PrivateKey(s.acmeAccount); err != nil {
			log.Print("Failed to marshal acme account key: ", err)
		} else {
			state.AcmeAccount = accountBytes
		}
	}

	// Save all of the certficates per domain
	for domain, cert := range s.certificates {
		keyBytes, err := x509.MarshalPKCS8PrivateKey(cert.PrivateKey)
		if err != nil {
			log.Printf("Failed to marshal the cert key for %v: %v", domain, err)
			continue
		}
		state.Certificates = append(state.Certificates,
			&Certificate{
				Domain: domain,
				Der:    cert.Certificate,
				Key:    keyBytes,
			})
	}
	sort.Slice(state.Certificates, func(i, j int) bool {
		return state.Certificates[i].Domain < state.Certificates[j].Domain
	})

	saveData, err := proto.Marshal(state)
	if err != nil {
		log.Print("Failed to marshal save state: ", err)
		return
	}
	tmpFilepath := s.saveFilepath + ".tmp"
	if err := writeFileSync(tmpFilepath, saveData, 0660); err != nil {
		log.Print("Failed to write temp save state file: ", err)
		return
	}
	if err := atomicReplaceFile(tmpFilepath, s.saveFilepath); err != nil {
		log.Print("Failed to overwrite save state file: ", err)
	}
	log.Print("Saved state file")
}

func (s *stateManager) Token() string {
	return s.token.Load().(string)
}

func (s *stateManager) unsafeSaveRootCA(rawCert []byte) error {
	newRoot, err := x509.ParseCertificate(rawCert)
	if err != nil {
		return err
	}

	s.rootCAs[newRoot] = struct{}{}
	s.mutCertPool.AddCert(newRoot)
	s.readCertPool.Store(s.mutCertPool.Clone())
	return nil
}

func (s *stateManager) SaveRootCA(rawCert []byte) error {
	s.mut.Lock()
	defer s.mut.Unlock()

	if err := s.unsafeSaveRootCA(rawCert); err != nil {
		return err
	}

	s.saveUnsafe()
	return nil
}

func (s *stateManager) RootCAs() *x509.CertPool {
	return s.readCertPool.Load().(*x509.CertPool)
}

func (s *stateManager) ForEachRegistration(body func(*Registration)) {
	s.mut.Lock()
	defer s.mut.Unlock()
	for _, r := range s.registrations {
		body(r)
	}
}

func (s *stateManager) LookupRegistration(lease *gate.Lease) *Registration {
	s.mut.Lock()
	defer s.mut.Unlock()

	return s.registrations[leaseKey(lease)]
}

func (s *stateManager) unsafeSaveRegistration(r *Registration) error {
	key := leaseKey(r.Lease)

	if _, ok := s.registrations[key]; ok {
		return fmt.Errorf("Programming error: a matching lease is already registered: %v", key)
	}

	s.registrations[key] = r
	return nil
}

func (s *stateManager) SaveRegistration(r *Registration) error {
	s.mut.Lock()
	defer s.mut.Unlock()
	if err := s.unsafeSaveRegistration(r); err != nil {
		return err
	}
	s.saveUnsafe()
	return nil
}

func (s *stateManager) RenewRegistration(lease *gate.Lease) error {
	s.mut.Lock()
	defer s.mut.Unlock()

	key := leaseKey(lease)
	r, ok := s.registrations[key]
	if !ok {
		return fmt.Errorf("Programming Error: no registration found to renew: %v", key)
	}

	r.Lease = lease
	s.saveUnsafe()
	return nil
}

func (s *stateManager) Unregister(oldLease *gate.Lease) {
	s.mut.Lock()
	defer s.mut.Unlock()

	delete(s.registrations, leaseKey(oldLease))

	s.saveUnsafe()
}

func (s *stateManager) TLSCert(domain string) *tls.Certificate {
	s.mut.Lock()
	defer s.mut.Unlock()

	cert, ok := s.certificates[domain]
	if !ok {
		return nil
	}
	return cert
}

func (s *stateManager) SaveTLSCert(domain string, cert *tls.Certificate) error {
	s.mut.Lock()
	defer s.mut.Unlock()

	s.certificates[domain] = cert

	s.saveUnsafe()
	return nil
}

func (s *stateManager) ACMEAccount() crypto.Signer {
	s.mut.Lock()
	defer s.mut.Unlock()
	return s.acmeAccount
}

func (s *stateManager) SaveACMEAccount(accountKey crypto.Signer) error {
	s.mut.Lock()
	defer s.mut.Unlock()

	s.acmeAccount = accountKey

	s.saveUnsafe()
	return nil
}
