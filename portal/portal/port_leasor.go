package main

import (
	"errors"
	"fmt"
	"log"
	"math/rand"
	"os"
	"sync"
	"time"

	"ask.systems/daemon/portal"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// How often to look through the Leases and unregister those past TTL
const ttlCheckFreq = 15 * time.Minute

var FixedPortTakenErr = errors.New("Requested fixed port is already taken!")

// Manages a list of leases for ports client servers should listen on.
// Also handles expiration of the leases.
//
// The main purpose is for not having port conflicts when you're running several
// server binaries on the same machine. Technically we could have a separate
// port list for each machine connecting to portal but there should be enough
// ports to just have one list.
type PortLeasor struct {
	mut          *sync.Mutex      // Everything in this struct needs the lock
	leases       map[uint32]lease // maps the port to the lease
	saveFilepath string

	// List of automatic ports to be leased out, in a random order.
	// Always has values between 0 and n, see unusedPortOffset.
	unusedPorts []int // int so we can use rand.Perm()
	// Add this to the values in unusedPorts to get the stored port number
	unusedPortOffset uint16
	ttl              time.Duration
	startPort        uint16
	endPort          uint16
}

type lease struct {
	*portal.Lease
	registration *Registration
	cancel       func() // call this function to cancel the lease`
}

func StartPortLeasor(startPort, endPort uint16, ttl time.Duration, saveFilepath string, quit chan struct{}) *PortLeasor {
	if endPort < startPort {
		startPort, endPort = endPort, startPort
	}
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	l := &PortLeasor{
		mut:              &sync.Mutex{},
		startPort:        startPort,
		endPort:          endPort,
		ttl:              ttl,
		leases:           make(map[uint32]lease),
		saveFilepath:     saveFilepath,
		unusedPortOffset: startPort,
		unusedPorts:      r.Perm(int(endPort - startPort)),
	}
	go l.monitorTTLs(quit)
	return l
}

func (l *PortLeasor) saveStateFileUnsafe() {
	// Build the save state proto from the current in memory state
	state := &State{}
	for _, lease := range l.leases {
		state.Registrations = append(state.Registrations, lease.registration)
	}

	saveData, err := proto.Marshal(state)
	if err != nil {
		log.Print("Failed to marshal save state to store leases: ", err)
		return
	}
	if err := os.WriteFile(l.saveFilepath, saveData, 0660); err != nil {
		log.Print("Failed to write save state file to store leases: ", err)
		return
	}
	log.Print("Saved leases state file")
}

// Register a port exclusively for limited time. If the FixedPort is 0, you will
// get a random port in the pre-configured range. Otherwise you will get a lease
// for the requested port if it is not already taken.
//
// Accepts a cancelation callback function to close connections in proxy
// backends and the Registration proto which is saved to a file to persist
// leases.
//
// The client string is simply stored in the state save file so that proxy
// backends can reconnect to the address on restart.
func (l *PortLeasor) Register(client string, request *portal.RegisterRequest, canceler func()) (*portal.Lease, error) {
	l.mut.Lock()
	defer l.mut.Unlock()

	newLease := &portal.Lease{
		Pattern: request.Pattern,
	}

	// Either use the fixed port or select a port automatically
	if request.FixedPort != 0 {
		if request.FixedPort >= 1<<16 {
			canceler()
			return nil, fmt.Errorf("Error port out of range. Ports only go up to 65535. Requested Port: %v", request.FixedPort)
		}
		if oldLease, ok := l.leases[request.FixedPort]; ok {
			canceler()
			return oldLease.Lease, fmt.Errorf("%w Requested Port: %v", FixedPortTakenErr, request.FixedPort)
		}
		newLease.Port = request.FixedPort
	} else {
		port, err := l.reservePortUnsafe()
		if err != nil {
			canceler()
			return nil, err
		}
		newLease.Port = port
		// Modify the request for saving the state so that when we reload the
		// request it will get a lease for the same port as we are returning now
		request = proto.Clone(request).(*portal.RegisterRequest)
		request.FixedPort = port
	}
	newLease.Timeout = timestamppb.New(time.Now().Add(l.ttl))

	l.leases[newLease.Port] = lease{newLease, &Registration{
		ClientAddr: client,
		Lease:      newLease,
		Request:    request,
	}, canceler}
	log.Print("New lease registered: ", newLease)
	l.saveStateFileUnsafe()
	return proto.Clone(newLease).(*portal.Lease), nil
}

func (l *PortLeasor) Renew(lease *portal.Lease) (*portal.Lease, error) {
	l.mut.Lock()
	defer l.mut.Unlock()

	foundLease, ok := l.leases[lease.Port]
	if !ok || foundLease.Lease == nil {
		return nil, errors.New("Not registered") // TODO: unregistered error
	}
	if foundLease.Lease.Pattern != lease.Pattern {
		return nil, errors.New("The lease you requested to renew doesn't match our records for that port.")
	}

	foundLease.Timeout = timestamppb.New(time.Now().Add(l.ttl))
	log.Print("Lease renewed: ", foundLease)
	l.saveStateFileUnsafe()
	return foundLease.Lease, nil
}

func (l *PortLeasor) Unregister(lease *portal.Lease) error {
	l.mut.Lock()
	defer l.mut.Unlock()

	foundLease := l.leases[lease.Port]
	if foundLease.Lease == nil {
		return errors.New("Not registered") // TODO: unregistered error
	}
	if foundLease.Lease.Pattern != lease.GetPattern() {
		return errors.New("The lease you requested to renew doesn't match our records for that port.")
	}

	log.Print("Lease unregistered: ", foundLease)
	l.deleteLeaseUnsafe(foundLease)
	l.saveStateFileUnsafe()
	return nil
}

// reservePort retuns a random unused port and marks it as used.
// Returns an error if the server has no more ports to lease.
//
// You must have a (write) lock on mut before calling.
func (l *PortLeasor) reservePortUnsafe() (uint32, error) {
	for {
		if len(l.unusedPorts) == 0 {
			return 0, fmt.Errorf("No remaining ports to lease. Active leases: %v", len(l.leases))
		}
		port := uint32(uint16(l.unusedPorts[0]) + l.unusedPortOffset)
		l.unusedPorts = l.unusedPorts[1:]
		if _, ok := l.leases[port]; !ok {
			// Only return the port if it wasn't already registered. If it was
			// registered just pop another random port off the list.
			return uint32(port), nil
		}
	}
}

// You must have a (write) lock on mut before calling.
func (l *PortLeasor) deleteLeaseUnsafe(lease lease) {
	lease.cancel()
	port := uint16(lease.Port)
	if port >= l.startPort && port <= l.endPort {
		// Add the port back into the pool if it wasn't a fixed port
		l.unusedPorts = append(l.unusedPorts, int(port-l.unusedPortOffset))
	}
	delete(l.leases, lease.Port)
}

// monitorTTLs monitors the list of leases and removes the expired ones.
// Calls the canceler function given in the Register call for the lease if it
// expires.
//
// Checks the lease once per each ttlCheckFreq duration.
func (l *PortLeasor) monitorTTLs(quit chan struct{}) {
	ticker := time.NewTicker(ttlCheckFreq)
	for {
		select {
		case <-ticker.C: // on next tick
			l.mut.Lock()
			now := time.Now()
			deletedAny := false
			for _, lease := range l.leases {
				if now.After(lease.Timeout.AsTime()) {
					log.Print("Lease expired: ", lease)
					l.deleteLeaseUnsafe(lease)
					deletedAny = true
				}
			}
			if deletedAny {
				l.saveStateFileUnsafe()
			}
			l.mut.Unlock()
		case <-quit: // on quit
			ticker.Stop()
			return
		}
	}
}
