package embedportal

import (
	"crypto/x509"
	"log"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"ask.systems/daemon/portal/gate"
	"ask.systems/daemon/tools"
	"google.golang.org/protobuf/proto"
)

type StateManager struct {
	mut          *sync.Mutex
	saveFilepath string

	registrations map[uint32]*Registration // lease port key

	rootCAs      map[*x509.Certificate]struct{}
	mutCertPool  *x509.CertPool
	readCertPool *atomic.Value

	token *atomic.Value
}

func NewStateManager(saveFilepath string) *StateManager {
	return &StateManager{
		mut:          &sync.Mutex{},
		saveFilepath: saveFilepath,

		registrations: make(map[uint32]*Registration),

		rootCAs:      make(map[*x509.Certificate]struct{}),
		mutCertPool:  x509.NewCertPool(),
		readCertPool: &atomic.Value{},

		token: &atomic.Value{},
	}
}

func (s *StateManager) saveUnsafe() {
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
	if err := os.WriteFile(s.saveFilepath, saveData, 0660); err != nil {
		log.Print("Failed to write save state file to store leases: ", err)
		return
	}
	log.Print("Saved leases state file")
}

func (s *StateManager) Token() string {
	return s.token.Load().(string)
}

// If empty, generate a new token
func (s *StateManager) SetToken(token string) {
	if token == "" {
		token = tools.RandomString(32)
	}

	log.Printf("**** Portal API token: %v ****", token)
	s.mut.Lock()
	defer s.mut.Unlock()
	s.token.Store(token)
	s.saveUnsafe()
}

func (s *StateManager) NewRootCA(rawCert []byte) error {
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

func (s *StateManager) RootCAs() *x509.CertPool {
	return s.readCertPool.Load().(*x509.CertPool)
}

func (s *StateManager) LookupRegistration(lease *gate.Lease) *Registration {
	s.mut.Lock()
	defer s.mut.Unlock()

	return s.registrations[lease.Port]
}

func (s *StateManager) NewRegistration(r *Registration) {
	s.mut.Lock()
	defer s.mut.Unlock()

	s.registrations[r.Lease.Port] = r

	s.saveUnsafe()
}

func (s *StateManager) RenewRegistration(lease *gate.Lease) {
	s.mut.Lock()
	defer s.mut.Unlock()

	r := s.registrations[lease.Port]
	r.Lease = lease

	s.saveUnsafe()
}

func (s *StateManager) Unregister(oldLease *gate.Lease) {
	s.mut.Lock()
	defer s.mut.Unlock()

	delete(s.registrations, oldLease.Port)

	s.saveUnsafe()
}
