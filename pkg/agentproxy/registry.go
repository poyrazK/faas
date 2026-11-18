package agentproxy

import (
	"errors"
	"fmt"
	"net/netip"
	"sync"
	"time"
)

// Registration is one agent VM's identity, as its node's vmmd registered it
// (ADR-20261117185346 §4).
type Registration struct {
	Addr      netip.Addr
	Node      netip.Addr
	AccountID string
	SandboxID string
	Expires   time.Time
}

// Slices maps agent tunnel addresses to the node slices they belong to:
// slice N of NodePrefix in Tunnel is node N's, and its first host address
// is the node's own (manifest.AgentNodeSlice).
type Slices struct {
	Tunnel     netip.Prefix
	NodePrefix int
}

// nodeOf is the node address of the slice holding addr.
func (s Slices) nodeOf(addr netip.Addr) (netip.Addr, bool) {
	if !addr.Is4() || !s.Tunnel.Contains(addr) || s.NodePrefix <= s.Tunnel.Bits() || s.NodePrefix > 30 {
		return netip.Addr{}, false
	}
	slice, err := addr.Prefix(s.NodePrefix)
	if err != nil || slice.Addr() == s.Tunnel.Masked().Addr() {
		return netip.Addr{}, false // slice 0 is the gateway's
	}
	return slice.Addr().Next(), true
}

// Registry holds the agent VMs the proxy serves. A connection from an
// address without a live registration is refused.
type Registry struct {
	slices Slices
	ttl    time.Duration
	now    func() time.Time

	mu   sync.Mutex
	regs map[netip.Addr]Registration
}

// NewRegistry is an empty registry for the tunnel's slices; a registration
// lives ttl past its last renewal.
func NewRegistry(slices Slices, ttl time.Duration, now func() time.Time) *Registry {
	if now == nil {
		now = time.Now
	}
	return &Registry{slices: slices, ttl: ttl, now: now, regs: map[netip.Addr]Registration{}}
}

// ErrNotYourSlice refuses a peer that registers an address outside its own
// node's slice, or that is not a node.
var ErrNotYourSlice = errors.New("the address is not in the registering node's slice")

// Register adds or renews addr's registration on behalf of peer, the
// node's tunnel address. A node registers only its own slice's VMs, never
// its own address.
func (r *Registry) Register(peer, addr netip.Addr, accountID, sandboxID string) (Registration, error) {
	node, ok := r.slices.nodeOf(addr)
	if !ok || node != peer || addr == node {
		return Registration{}, ErrNotYourSlice
	}
	if accountID == "" || len(accountID) > 64 || sandboxID == "" || len(sandboxID) > 64 {
		return Registration{}, fmt.Errorf("a registration needs an account and a sandbox of at most 64 bytes")
	}
	reg := Registration{Addr: addr, Node: node, AccountID: accountID, SandboxID: sandboxID, Expires: r.now().Add(r.ttl)}
	r.mu.Lock()
	r.regs[addr] = reg
	r.mu.Unlock()
	return reg, nil
}

// Unregister drops addr's registration on behalf of peer.
func (r *Registry) Unregister(peer, addr netip.Addr) error {
	if node, ok := r.slices.nodeOf(addr); !ok || node != peer {
		return ErrNotYourSlice
	}
	r.mu.Lock()
	delete(r.regs, addr)
	r.mu.Unlock()
	return nil
}

// Lookup is addr's live registration.
func (r *Registry) Lookup(addr netip.Addr) (Registration, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	reg, ok := r.regs[addr]
	if !ok {
		return Registration{}, false
	}
	if !r.now().Before(reg.Expires) {
		delete(r.regs, addr)
		return Registration{}, false
	}
	return reg, true
}

// Len is the number of registrations, live or not yet swept.
func (r *Registry) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.regs)
}

// Sweep drops expired registrations.
func (r *Registry) Sweep() {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	for addr, reg := range r.regs {
		if !now.Before(reg.Expires) {
			delete(r.regs, addr)
		}
	}
}
