// Package hostport provides the node-local allocation rules used by the
// container host-port lease registry. Allocation is deliberately independent
// from the database so the same collision and idempotency rules are exercised
// by the in-memory store and by the scheduler's unit tests.
package hostport

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultStartPort and DefaultEndPort are reserved for container ingress.
	// They stay outside the node's own control-plane ports and leave room for a
	// future operator-configured range without changing the lease contract.
	DefaultStartPort = 30000
	DefaultEndPort   = 39999
)

var (
	ErrInvalidRequest = errors.New("hostport: invalid lease request")
	ErrExhausted      = errors.New("hostport: allocation range exhausted")
)

type Protocol string

const (
	TCP Protocol = "tcp"
	UDP Protocol = "udp"
)

// Request identifies one guest listener that needs a node-local host port.
// Name is optional; unnamed listeners receive the stable port-N name.
type Request struct {
	Name      string
	Protocol  Protocol
	GuestPort int
}

// Lease is the durable mapping returned to callers. HostPort is unique per
// node and protocol, while the same numeric port may be used by TCP and UDP.
type Lease struct {
	NodeID       string
	InstanceID   string
	ListenerName string
	Protocol     Protocol
	GuestPort    int
	HostPort     int
	CreatedAt    time.Time
}

type key struct {
	nodeID, instanceID, listener string
	protocol                     Protocol
}

type addressKey struct {
	nodeID   string
	protocol Protocol
	port     int
}

// Allocator is a concurrency-safe, deterministic lease allocator. It is used
// by MemStore and is also a reference implementation for the Postgres-backed
// allocator. Acquire is atomic: if any requested listener cannot be placed,
// newly-created leases from that call are rolled back.
type Allocator struct {
	mu     sync.Mutex
	start  int
	end    int
	byKey  map[key]Lease
	byAddr map[addressKey]key
}

func NewAllocator(start, end int) *Allocator {
	if start < 1 || end < start || end > 65535 {
		panic("hostport: invalid allocation range")
	}
	return &Allocator{
		start:  start,
		end:    end,
		byKey:  make(map[key]Lease),
		byAddr: make(map[addressKey]key),
	}
}

func NewDefaultAllocator() *Allocator {
	return NewAllocator(DefaultStartPort, DefaultEndPort)
}

// CanonicalRequests validates and sorts requests so allocation is independent
// of manifest order. The returned names are stable for unnamed listeners.
func CanonicalRequests(requests []Request) ([]Request, error) {
	if len(requests) == 0 {
		return nil, nil
	}
	out := make([]Request, len(requests))
	copy(out, requests)
	seenNames := make(map[string]struct{}, len(out))
	seenPorts := make(map[string]struct{}, len(out))
	for i := range out {
		p := &out[i]
		p.Protocol = Protocol(strings.ToLower(string(p.Protocol)))
		if p.Protocol != TCP && p.Protocol != UDP {
			return nil, fmt.Errorf("%w: protocol %q", ErrInvalidRequest, p.Protocol)
		}
		if p.GuestPort < 1 || p.GuestPort > 65535 {
			return nil, fmt.Errorf("%w: guest port %d", ErrInvalidRequest, p.GuestPort)
		}
		p.Name = strings.ToLower(strings.TrimSpace(p.Name))
		if p.Name == "" {
			p.Name = fmt.Sprintf("port-%d", p.GuestPort)
		}
		if !validName(p.Name) {
			return nil, fmt.Errorf("%w: listener name %q must match [a-z0-9][a-z0-9-]{0,30}", ErrInvalidRequest, p.Name)
		}
		if _, ok := seenNames[p.Name]; ok {
			return nil, fmt.Errorf("%w: duplicate listener name %q", ErrInvalidRequest, p.Name)
		}
		seenNames[p.Name] = struct{}{}
		portKey := fmt.Sprintf("%s/%d", p.Protocol, p.GuestPort)
		if _, ok := seenPorts[portKey]; ok {
			return nil, fmt.Errorf("%w: duplicate listener %s", ErrInvalidRequest, portKey)
		}
		seenPorts[portKey] = struct{}{}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Protocol != out[j].Protocol {
			return out[i].Protocol < out[j].Protocol
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func validName(name string) bool {
	if len(name) == 0 || len(name) > 31 {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || (i > 0 && c == '-') {
			continue
		}
		return false
	}
	return true
}

func (a *Allocator) Acquire(nodeID, instanceID string, requests []Request) ([]Lease, error) {
	if strings.TrimSpace(nodeID) == "" || strings.TrimSpace(instanceID) == "" {
		return nil, fmt.Errorf("%w: node and instance are required", ErrInvalidRequest)
	}
	canonical, err := CanonicalRequests(requests)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	result := make([]Lease, 0, len(canonical))
	created := make([]key, 0, len(canonical))
	rollback := func() {
		for _, k := range created {
			lease := a.byKey[k]
			delete(a.byKey, k)
			delete(a.byAddr, addressKey{nodeID: lease.NodeID, protocol: lease.Protocol, port: lease.HostPort})
		}
	}
	for _, req := range canonical {
		k := key{nodeID: nodeID, instanceID: instanceID, listener: req.Name, protocol: req.Protocol}
		if existing, ok := a.byKey[k]; ok {
			if existing.GuestPort != req.GuestPort {
				rollback()
				return nil, fmt.Errorf("%w: listener %q guest port changed", ErrInvalidRequest, req.Name)
			}
			result = append(result, existing)
			continue
		}
		var hostPort int
		for candidate := a.start; candidate <= a.end; candidate++ {
			if _, used := a.byAddr[addressKey{nodeID: nodeID, protocol: req.Protocol, port: candidate}]; !used {
				hostPort = candidate
				break
			}
		}
		if hostPort == 0 {
			rollback()
			return nil, fmt.Errorf("%w: node=%s protocol=%s", ErrExhausted, nodeID, req.Protocol)
		}
		lease := Lease{
			NodeID: nodeID, InstanceID: instanceID, ListenerName: req.Name,
			Protocol: req.Protocol, GuestPort: req.GuestPort, HostPort: hostPort,
			CreatedAt: time.Now().UTC(),
		}
		a.byKey[k] = lease
		a.byAddr[addressKey{nodeID: nodeID, protocol: req.Protocol, port: hostPort}] = k
		created = append(created, k)
		result = append(result, lease)
	}
	return result, nil
}

func (a *Allocator) Release(nodeID, instanceID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for k, lease := range a.byKey {
		if k.nodeID == nodeID && k.instanceID == instanceID {
			delete(a.byKey, k)
			delete(a.byAddr, addressKey{nodeID: lease.NodeID, protocol: lease.Protocol, port: lease.HostPort})
		}
	}
}

func (a *Allocator) List(nodeID, instanceID string) []Lease {
	a.mu.Lock()
	defer a.mu.Unlock()
	result := make([]Lease, 0)
	for k, lease := range a.byKey {
		if (nodeID == "" || k.nodeID == nodeID) && (instanceID == "" || k.instanceID == instanceID) {
			result = append(result, lease)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].NodeID != result[j].NodeID {
			return result[i].NodeID < result[j].NodeID
		}
		if result[i].Protocol != result[j].Protocol {
			return result[i].Protocol < result[j].Protocol
		}
		return result[i].HostPort < result[j].HostPort
	})
	return result
}
