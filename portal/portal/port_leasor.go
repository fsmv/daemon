package main

import (
    "fmt"
    "time"
    "sync"
    "errors"
    "math/rand"

    "ask.systems/daemon/portal"
    "google.golang.org/protobuf/proto"
    "google.golang.org/protobuf/types/known/timestamppb"
)

// How often to look through the Leases and unregister those past TTL
const ttlCheckFreq = 15*time.Minute

// Manages a list of leases for ports client servers should listen on.
// Also handles expiration of the leases.
//
// The main purpose is for not having port conflicts when you're running several
// server binaries on the same machine. Technically we could have a separate
// port list for each machine connecting to portal but there should be enough
// ports to just have one list.
type PortLeasor struct {
    mut    *sync.RWMutex // Everything in this struct needs the lock
    leases map[uint32]lease // maps the port to the lease

    // List of automatic ports to be leased out, in a random order.
    // Always has values between 0 and n, see unusedPortOffset.
    unusedPorts      []int // int so we can use rand.Perm()
    // Add this to the values in unusedPorts to get the stored port number
    unusedPortOffset uint16
    ttl              time.Duration
    startPort        uint16
    endPort          uint16
}

type lease struct {
    *portal.Lease
    cancel func() // call this function to cancel the lease`
}

func StartPortLeasor(startPort, endPort uint16, ttl time.Duration, quit chan struct{}) *PortLeasor {
    if endPort < startPort {
        startPort, endPort = endPort, startPort
    }
    r := rand.New(rand.NewSource(time.Now().UnixNano()))
    l := &PortLeasor{
        mut: &sync.RWMutex{},
        startPort: startPort,
        endPort: endPort,
        ttl: ttl,
        leases: make(map[uint32]lease),
        unusedPortOffset: startPort,
        unusedPorts: r.Perm(int(endPort - startPort)),
    }
    go l.monitorTTLs(quit)
    return l
}

func (l *PortLeasor) Register(request *portal.Lease, canceler func()) (*portal.Lease, error) {
    l.mut.Lock()
    defer l.mut.Unlock()

    // Either use the fixed port or select a port automatically
    if request.Port != 0 {
        if (request.Port >= uint32(l.startPort) && request.Port <= uint32(l.endPort)) {
            return nil, fmt.Errorf("Fixed port %v must not be in the reserved portal client range: [%v, %v]", request.Port, l.startPort, l.endPort)
        }
        if (request.Port >= 1 << 16) {
            return nil, fmt.Errorf("Fixed port (%v) out of possiible port range: must be less than 2^16", request.Port)
        }
    } else {
        port, err := l.reservePortUnsafe()
        if err != nil {
            return nil, err
        }
        request.Port = port
    }

    request.Timeout = timestamppb.New(time.Now().Add(l.ttl))
    l.leases[request.Port] = lease{request, canceler}
    return request, nil
}

func (l *PortLeasor) Renew(lease *portal.Lease) (*portal.Lease, error) {
    l.mut.Lock()
    defer l.mut.Unlock()

    foundLease := l.leases[lease.Port]
    if foundLease.Lease == nil {
        return nil, errors.New("Not registered")// TODO: unregistered error
    }
    if !proto.Equal(foundLease.Lease, lease) {
        return nil, errors.New("The lease you requested to renew doesn't match our records for that port.")
    }

    foundLease.Timeout = timestamppb.New(time.Now().Add(l.ttl))
    return foundLease.Lease, nil
}

func (l *PortLeasor) Unregister(lease *portal.Lease) error {
    l.mut.Lock()
    defer l.mut.Unlock()

    foundLease := l.leases[lease.Port]
    if foundLease.Lease == nil {
        return errors.New("Not registered")// TODO: unregistered error
    }
    if !proto.Equal(foundLease.Lease, lease) {
        return errors.New("The lease you requested to renew doesn't match our records for that port.")
    }

    l.deleteLeaseUnsafe(foundLease)
    return nil
}

// reservePort retuns a random unused port and marks it as used.
// Returns an error if the server has no more ports to lease.
//
// You must have a (write) lock on mut before calling.
func (l *PortLeasor) reservePortUnsafe() (uint32, error) {
    if len(l.unusedPorts) == 0 {
        return 0, fmt.Errorf("No remaining ports to lease. Active leases: %v", len(l.leases))
    }
    port := uint16(l.unusedPorts[0]) + l.unusedPortOffset
    l.unusedPorts = l.unusedPorts[1:]
    return uint32(port), nil
}

// You must have a (write) lock on mut before calling.
func (l *PortLeasor) deleteLeaseUnsafe(lease lease) {
    lease.cancel()
    port := uint16(lease.Port)
    if port >= l.startPort && port <= l.endPort {
        // Add the port back into the pool if it wasn't a fixed port
        l.unusedPorts = append(l.unusedPorts, int(port - l.unusedPortOffset))
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
            for _, lease := range l.leases {
                if now.After(lease.Timeout.AsTime()) {
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
