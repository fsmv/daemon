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
var (
	ttlCheckFreq = leaseTTL / 100
)

var (
	FixedPortTakenErr = errors.New("Requested fixed port is already taken!")
	UnregisteredErr   = errors.New("The requested lease was not previously registered.")
	InvalidLeaseErr   = errors.New("The requested lease does not match the lease we have on record for this port.")
)

type onCancelFunc func(*gate.Lease)

type clientLeasor struct {
	leasors    *sync.Map  // Key: string Value: *portLeasor
	leasorPool *sync.Pool // Contains *portLeasor

	startPort     uint16
	endPort       uint16
	reservedPorts map[uint16]bool
	quit          chan struct{}

	onCancel    []onCancelFunc
	onCancelMut *sync.Mutex
}

func makeClientLeasor(startPort, endPort uint16, reservedPorts map[uint16]bool, quit chan struct{}) *clientLeasor {
	if endPort < startPort {
		startPort, endPort = endPort, startPort
	}
	c := &clientLeasor{
		leasors:     &sync.Map{},
		onCancelMut: &sync.Mutex{},

		startPort:     startPort,
		endPort:       endPort,
		reservedPorts: reservedPorts,
		quit:          quit,
	}
	c.leasorPool = &sync.Pool{
		New: func() any {
			return &portLeasor{
				mut:       &sync.Mutex{},
				startPort: startPort,
				endPort:   endPort,
				// TODO: clean up this weird system with copyOnCancel and and c.OnCancel
				// calling OnCancel on all the portLeasors. I think the onCancel list
				// should not be copied to each portLeasor and instead they should
				// report cancels back to the clientLeasor which should keep the only
				// list.
				//
				// I think leasorPool.New is garunteed to only be called when we call
				// Get and that is the only reason this system is safe
				onCancel:    c.copyOnCancel(),
				leases:      make(map[uint32][]*gate.Lease),
				unusedPorts: makeUnusedPorts(startPort, endPort, reservedPorts),
			}
		},
	}
	return c
}

func leaseString(lease *gate.Lease) string {
	return fmt.Sprintf(
		"{pattern: %v. Port: %v, Timeout: %v}",
		lease.Pattern, lease.Port, lease.Timeout.AsTime().In(time.Local))
}

func (c *clientLeasor) PortLeasorForClient(clientAddr string) *portLeasor {
	// We use a sync.Pool because we have to call LoadOrStore on the leasors map,
	// we need to have a pointer to space on the heap every time we lookup a
	// client so that we can start a new leasor if one doesn't exist.
	//
	// The pool lets us save and re-use the heap space here when we find a client
	// we already have a leasor for.
	nextLeasor := c.leasorPool.Get()
	// This thread exclusively owns nextLeasor right now becasue we called Get and
	// have not stored it in the map yet.
	nextLeasor.(*portLeasor).clientAddr = clientAddr
	leasor, loaded := c.leasors.LoadOrStore(clientAddr, nextLeasor)
	if !loaded {
		// This same pointer might have been returned from a concurrent call before
		// we start this goroutine, but this will still only happen once and it's
		// okay if we start using leasor before this.
		go leasor.(*portLeasor).monitorTTLs(c.quit)
	} else {
		c.leasorPool.Put(nextLeasor)
	}
	return leasor.(*portLeasor)
}

func (c *clientLeasor) makePortLeasor() any {
	return &portLeasor{}
}

func (c *clientLeasor) copyOnCancel() []onCancelFunc {
	c.onCancelMut.Lock()
	defer c.onCancelMut.Unlock()
	ret := make([]onCancelFunc, len(c.onCancel))
	copy(ret, c.onCancel)
	return ret
}

func (c *clientLeasor) OnCancel(cancelFunc func(*gate.Lease)) {
	c.onCancelMut.Lock()
	defer c.onCancelMut.Unlock()
	c.onCancel = append(c.onCancel, cancelFunc)
	// Since we still have the mutex lock and copyOnCancel also uses it, this
	// covers all existing leasors. New leasors can't be created until the mutex
	// is released and they'll get the new list.
	c.leasors.Range(func(key, value interface{}) bool {
		l := value.(*portLeasor)
		l.OnCancel(cancelFunc)
		return true
	})
}

// Manages a list of leases for ports client servers should listen on.
// Also handles expiration of the leases.
//
// The main purpose is for not having port conflicts when you're running several
// server binaries on the same machine.
type portLeasor struct {
	mut      *sync.Mutex              // Everything in this struct needs the lock
	leases   map[uint32][]*gate.Lease // maps the port to the lease
	onCancel []onCancelFunc           // All are called when a lease times out

	// List of automatic ports to be leased out, in a random order.
	unusedPorts []uint16
	startPort   uint16
	endPort     uint16
	clientAddr  string
}

func (l *portLeasor) OnCancel(cancelFunc func(*gate.Lease)) {
	l.mut.Lock()
	defer l.mut.Unlock()
	l.onCancel = append(l.onCancel, cancelFunc)
}

func randomTTL(ttl time.Duration) time.Duration {
	return time.Duration(float64(ttl) * (1 + rand.Float64()*ttlRandomStagger))
}

// Register a port exclusively for limited time. If the FixedPort is 0, you will
// get a random port in the pre-configured range. Otherwise you will get a lease
// for the requested port if it is not already taken.
//
// The client string is simply stored in the state save file so that proxy
// backends can reconnect to the address on restart.
func (l *portLeasor) Register(request *gate.RegisterRequest, fixedTimeout time.Time) (*gate.Lease, error) {
	l.mut.Lock()
	defer l.mut.Unlock()

	newLease := &gate.Lease{
		Pattern: request.Pattern,
		// We don't use the Hostname field in the request because this field will be
		// resolved to the RPC sender address if the request didn't set one.
		Address: l.clientAddr,
	}

	// Either use the fixed port or select a port automatically
	if request.FixedPort != 0 {
		if request.FixedPort >= 1<<16 {
			return nil, fmt.Errorf(
				"Error port out of range. Ports only go up to 65535. Requested Port: %v",
				request.FixedPort)
		}
		newLease.Port = request.FixedPort
	} else {
		// Always returns an unused port if err == nil
		port, err := l.reservePortUnsafe()
		if err != nil {
			return nil, err
		}
		newLease.Port = port
	}
	if fixedTimeout.IsZero() {
		newLease.Timeout = timestamppb.New(time.Now().Add(randomTTL(leaseTTL)))
	} else {
		newLease.Timeout = timestamppb.New(fixedTimeout)
	}

	portLeases := l.leases[newLease.Port]
	for _, oldLease := range portLeases {
		if oldLease.Pattern != newLease.Pattern {
			continue
		}
		// TODO: can we notify the old lease holder that we kicked them?
		log.Printf("Replacing an existing lease (%v) for the same port and pattern",
			leaseString(oldLease))
		l.deleteLeaseUnsafe(oldLease)
	}
	portLeases = append(portLeases, newLease)
	l.leases[newLease.Port] = portLeases
	log.Print("New lease registered: ", leaseString(newLease))
	return proto.Clone(newLease).(*gate.Lease), nil
}

func (l *portLeasor) Renew(lease *gate.Lease) (*gate.Lease, error) {
	l.mut.Lock()
	defer l.mut.Unlock()

	portLeases, ok := l.leases[lease.Port]
	if !ok || portLeases == nil {
		return nil, fmt.Errorf("%w; Requested lease: %v",
			UnregisteredErr, leaseString(lease))
	}
	for _, foundLease := range portLeases {
		if foundLease.Pattern != lease.GetPattern() {
			continue
		}
		foundLease.Timeout = timestamppb.New(time.Now().Add(randomTTL(leaseTTL)))
		log.Print("Lease renewed: ", leaseString(foundLease))
		return proto.Clone(foundLease).(*gate.Lease), nil
	}
	return nil, fmt.Errorf("%w; Requested lease: %v",
		UnregisteredErr, leaseString(lease))
}

func (l *portLeasor) Unregister(lease *gate.Lease) error {
	l.mut.Lock()
	defer l.mut.Unlock()

	portLeases, ok := l.leases[lease.Port]
	if !ok || portLeases == nil {
		return fmt.Errorf("%w; Requested lease: %v", UnregisteredErr, leaseString(lease))
	}
	for _, foundLease := range portLeases {
		if foundLease.Pattern != lease.GetPattern() {
			continue
		}
		log.Print("Lease unregistered: ", leaseString(foundLease))
		l.deleteLeaseUnsafe(foundLease)
		return nil
	}
	return fmt.Errorf("%w; Requested lease: %v", UnregisteredErr, leaseString(lease))
}

func makeUnusedPorts(start, end uint16, reserved map[uint16]bool) []uint16 {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	n := int(end - start + 1)
	ret := make([]uint16, n)
	idx := 0
	for port := start; port <= end; port++ {
		if reserved[port] {
			continue
		}
		ret[idx] = port
		idx++
	}
	ret = ret[:idx]
	r.Shuffle(len(ret), func(i, j int) {
		ret[i], ret[j] = ret[j], ret[i]
	})
	return ret
}

// reservePort retuns a random unused port and marks it as used.
// Returns an error if the server has no more ports to lease.
//
// You must have a (write) lock on mut before calling.
func (l *portLeasor) reservePortUnsafe() (uint32, error) {
	for len(l.unusedPorts) > 0 {
		port := uint32(l.unusedPorts[0])
		l.unusedPorts = l.unusedPorts[1:]
		if _, ok := l.leases[port]; !ok {
			// Only return the port if it wasn't already registered. If it was
			// registered just pop another random port off the list.
			return port, nil
		}
	}
	return 0, fmt.Errorf("No remaining ports to lease. Active leases: %v", len(l.leases))
}

// You must have a (write) lock on mut before calling.
func (l *portLeasor) deleteLeaseUnsafe(lease *gate.Lease) {
	portLeases := l.leases[lease.Port]
	for i, foundLease := range portLeases {
		if foundLease.Pattern != lease.Pattern {
			continue
		}
		copy(portLeases[:i], portLeases[i+1:])
		portLeases = portLeases[:len(portLeases)-1]
		break
	}
	if len(portLeases) == 0 {
		delete(l.leases, lease.Port)
		port := uint16(lease.Port)
		if port >= l.startPort && port <= l.endPort {
			// Add the port back into the pool if it wasn't a fixed port
			l.unusedPorts = append(l.unusedPorts, port)
		}
	} else {
		l.leases[lease.Port] = portLeases
	}

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
			for _, portLeases := range l.leases {
				for _, lease := range portLeases {
					if !now.After(lease.Timeout.AsTime()) {
						continue
					}
					log.Print("Lease expired: ", leaseString(lease))
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
