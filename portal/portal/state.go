package main

import (
	"log"
	"os"
	"sync"

	"ask.systems/daemon/portal"
	"google.golang.org/protobuf/proto"
)

type StateManager struct {
	mut           *sync.Mutex
	registrations map[uint32]*Registration // lease port key
	saveFilepath  string
}

func NewStateManager(saveFilepath string) *StateManager {
	return &StateManager{
		mut:           &sync.Mutex{},
		registrations: make(map[uint32]*Registration),
		saveFilepath:  saveFilepath,
	}
}

// TODO: return error?
func (s *StateManager) saveUnsafe() {
	// Build the save state proto from the current in memory state
	state := &State{}
	for _, r := range s.registrations {
		state.Registrations = append(state.Registrations, r)
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

func (s *StateManager) LookupRegistration(lease *portal.Lease) *Registration {
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

func (s *StateManager) RenewRegistration(lease *portal.Lease) {
	s.mut.Lock()
	defer s.mut.Unlock()

	r := s.registrations[lease.Port]
	r.Lease = lease

	s.saveUnsafe()
}

func (s *StateManager) Unregister(oldLease *portal.Lease) {
	s.mut.Lock()
	defer s.mut.Unlock()

	delete(s.registrations, oldLease.Port)

	s.saveUnsafe()
}
