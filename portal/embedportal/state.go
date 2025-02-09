package embedportal

import (
	"crypto/x509"
	"fmt"
	"log"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"ask.systems/daemon/portal/gate"
	"ask.systems/daemon/tools"
	"google.golang.org/protobuf/proto"
)

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
}

func newStateManager(saveFilepath string) *stateManager {
	s := &stateManager{
		mut:          &sync.Mutex{},
		saveFilepath: saveFilepath,

		registrations: make(map[string]*Registration),

		rootCAs:      make(map[*x509.Certificate]struct{}),
		mutCertPool:  x509.NewCertPool(),
		readCertPool: &atomic.Value{},

		token: &atomic.Value{},
	}
	if s.saveFilepath == "" {
		log.Print("No save file, state is only saved in memory.")
	}
	return s
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
	if token := s.token.Load(); token != nil {
		state.ApiToken = token.(string)
	}

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

	saveData, err := proto.Marshal(state)
	if err != nil {
		log.Print("Failed to marshal save state to store leases: ", err)
		return
	}
	tmpFilepath := s.saveFilepath + ".tmp"
	if err := writeFileSync(tmpFilepath, saveData, 0660); err != nil {
		log.Print("Failed to write temp save state file to store leases: ", err)
		return
	}
	if err := atomicReplaceFile(tmpFilepath, s.saveFilepath); err != nil {
		log.Print("Failed to overwrite save state file to store leases: ", err)
	}
	log.Print("Saved leases state file")
}

func (s *stateManager) Token() string {
	return s.token.Load().(string)
}

// If empty, generate a new token
func (s *stateManager) SetToken(token string) {
	if token == "" {
		token = tools.RandomString(32)
	}

	s.mut.Lock()
	defer s.mut.Unlock()
	s.token.Store(token)
	s.saveUnsafe()
}

func (s *stateManager) NewRootCA(rawCert []byte) error {
	newRoot, err := x509.ParseCertificate(rawCert)
	if err != nil {
		return err
	}

	s.mut.Lock()
	defer s.mut.Unlock()

	s.rootCAs[newRoot] = struct{}{}
	s.mutCertPool.AddCert(newRoot)
	s.readCertPool.Store(s.mutCertPool.Clone())
	s.saveUnsafe()
	return nil
}

func (s *stateManager) RootCAs() *x509.CertPool {
	return s.readCertPool.Load().(*x509.CertPool)
}

func leaseKey(lease *gate.Lease) string {
	return fmt.Sprintf("%s:%d:%s", lease.Address, lease.Port, lease.Pattern)
}

func (s *stateManager) LookupRegistration(lease *gate.Lease) *Registration {
	s.mut.Lock()
	defer s.mut.Unlock()

	return s.registrations[leaseKey(lease)]
}

func (s *stateManager) NewRegistration(r *Registration) {
	s.mut.Lock()
	defer s.mut.Unlock()

	s.registrations[leaseKey(r.Lease)] = r

	s.saveUnsafe()
}

func (s *stateManager) RenewRegistration(lease *gate.Lease) {
	s.mut.Lock()
	defer s.mut.Unlock()

	r := s.registrations[leaseKey(lease)]
	r.Lease = lease

	s.saveUnsafe()
}

func (s *stateManager) Unregister(oldLease *gate.Lease) {
	s.mut.Lock()
	defer s.mut.Unlock()

	delete(s.registrations, leaseKey(oldLease))

	s.saveUnsafe()
}
