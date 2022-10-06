package embedportal

import (
	"errors"
	"fmt"
	"log"
	"math/rand"
	"sync"
	"time"

	"ask.systems/daemon/portal/gate"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// How often to look through the Leases and unregister those past TTL
const ttlCheckFreq = 15 * time.Minute

var (
	FixedPortTakenErr = errors.New("Requested fixed port is already taken!")
	UnregisteredErr   = errors.New("The requested lease was not previously registered.")
	InvalidLeaseErr   = errors.New("The requested lease does not match the lease we have on record for this port.")
)

// Manages a list of leases for ports client servers should listen on.
// Also handles expiration of the leases.
//
// The main purpose is for not having port conflicts when you're running several
// server binaries on the same machine. Technically we could have a separate
// port list for each machine connecting to portal but there should be enough
// ports to just have one list.
type portLeasor struct {
	mut      *sync.Mutex            // Everything in this struct needs the lock
	leases   map[uint32]*gate.Lease // maps the port to the lease
	onCancel []func(*gate.Lease)    // All are called when a lease times out

	// List of automatic ports to be leased out, in a random order.
	// Always has values between 0 and n, see unusedPortOffset.
	unusedPorts []int // int so we can use rand.Perm()
	// Add this to the values in unusedPorts to get the stored port number
	unusedPortOffset uint16
	ttl              time.Duration
	startPort        uint16
	endPort          uint16
}

func startPortLeasor(startPort, endPort uint16, ttl time.Duration, quit chan struct{}) *portLeasor {
	if endPort < startPort {
		startPort, endPort = endPort, startPort
	}
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	l := &portLeasor{
		mut:              &sync.Mutex{},
		startPort:        startPort,
		endPort:          endPort,
		ttl:              ttl,
		leases:           make(map[uint32]*gate.Lease),
		unusedPortOffset: startPort,
		unusedPorts:      r.Perm(int(endPort - startPort)),
	}
	go l.monitorTTLs(quit)
	return l
}

func (l *portLeasor) OnCancel(cancelFunc func(*gate.Lease)) {
	l.mut.Lock()
	defer l.mut.Unlock()
	l.onCancel = append(l.onCancel, cancelFunc)
}

// Register a port exclusively for limited time. If the FixedPort is 0, you will
// get a random port in the pre-configured range. Otherwise you will get a lease
// for the requested port if it is not already taken.
//
// The client string is simply stored in the state save file so that proxy
// backends can reconnect to the address on restart.
func (l *portLeasor) Register(request *gate.RegisterRequest) (*gate.Lease, error) {
	l.mut.Lock()
	defer l.mut.Unlock()

	newLease := &gate.Lease{
		Pattern: request.Pattern,
	}

	// Either use the fixed port or select a port automatically
	if request.FixedPort != 0 {
		if request.FixedPort >= 1<<16 {
			return nil, fmt.Errorf("Error port out of range. Ports only go up to 65535. Requested Port: %v", request.FixedPort)
		}
		if oldLease, ok := l.leases[request.FixedPort]; ok {
			// TODO: can we notify the old lease holder that we kicked them?
			log.Printf("Replacing an existing lease for the same port: %#v", oldLease.Pattern)
			l.deleteLeaseUnsafe(oldLease)
		}
		newLease.Port = request.FixedPort
	} else {
		port, err := l.reservePortUnsafe()
		if err != nil {
			return nil, err
		}
		newLease.Port = port
	}
	newLease.Timeout = timestamppb.New(time.Now().Add(l.ttl))

	l.leases[newLease.Port] = newLease
	log.Print("New lease registered: ", newLease)
	return proto.Clone(newLease).(*gate.Lease), nil
}

func (l *portLeasor) Renew(lease *gate.Lease) (*gate.Lease, error) {
	l.mut.Lock()
	defer l.mut.Unlock()

	foundLease, ok := l.leases[lease.Port]
	if !ok || foundLease == nil {
		return nil, UnregisteredErr
	}
	if foundLease.Pattern != lease.GetPattern() {
		return nil, InvalidLeaseErr
	}

	foundLease.Timeout = timestamppb.New(time.Now().Add(l.ttl))
	log.Print("Lease renewed: ", foundLease)
	return proto.Clone(foundLease).(*gate.Lease), nil
}

func (l *portLeasor) Unregister(lease *gate.Lease) error {
	l.mut.Lock()
	defer l.mut.Unlock()

	foundLease := l.leases[lease.Port]
	if foundLease == nil {
		return UnregisteredErr
	}
	if foundLease.Pattern != lease.GetPattern() {
		return InvalidLeaseErr
	}

	log.Print("Lease unregistered: ", foundLease)
	l.deleteLeaseUnsafe(foundLease)
	return nil
}

// reservePort retuns a random unused port and marks it as used.
// Returns an error if the server has no more ports to lease.
//
// You must have a (write) lock on mut before calling.
func (l *portLeasor) reservePortUnsafe() (uint32, error) {
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
func (l *portLeasor) deleteLeaseUnsafe(lease *gate.Lease) {
	port := uint16(lease.Port)
	if port >= l.startPort && port <= l.endPort {
		// Add the port back into the pool if it wasn't a fixed port
		l.unusedPorts = append(l.unusedPorts, int(port-l.unusedPortOffset))
	}
	delete(l.leases, lease.Port)

	for _, onCancel := range l.onCancel {
		onCancel(lease)
	}
}

// monitorTTLs monitors the list of leases and removes the expired ones.
// Calls the onTTL functions given in the OnTTL() call for the lease if it
// expires.
//
// Checks the lease once per each ttlCheckFreq duration.
func (l *portLeasor) monitorTTLs(quit chan struct{}) {
	ticker := time.NewTicker(ttlCheckFreq)
	for {
		select {
		case <-ticker.C: // on next tick
			l.mut.Lock()
			now := time.Now()
			for _, lease := range l.leases {
				if now.After(lease.Timeout.AsTime()) {
					log.Print("Lease expired: ", lease)
					l.deleteLeaseUnsafe(lease)
				}
			}
			l.mut.Unlock()
		case <-quit: // on quit
			ticker.Stop()
			return
		}
	}
}
