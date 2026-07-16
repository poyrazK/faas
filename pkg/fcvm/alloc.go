package fcvm

import (
	"fmt"
	"net/netip"
	"sync"
)

// Invariant §6.2-5: two instances (including two restored from the SAME snapshot)
// never share an IP, netns, jail uid, or RNG stream. This allocator is the single
// authority for that. Every per-instance resource is derived from one unique
// slot, so two live instances cannot collide by construction — the property test
// in alloc_test.go proves it under concurrency.

const (
	// Jail uid/gid range (spec §4.4, §11). uid == gid per instance.
	JailUIDBase = 20000
	JailUIDMax  = 29999
	// MaxSlots is the number of simultaneously-live instances the box supports.
	// The uid range is the binding constraint (10000); tenant RAM caps real
	// concurrency far below this (47600/128 ≈ 372).
	MaxSlots = JailUIDMax - JailUIDBase + 1
)

// hostIPBase is the /16 the veth host-side addresses live in (spec §7,
// 10.100.x.y/16). Slot 0 maps to hostIPBase + hostIPOffset so the bridge address
// (10.100.0.1) and network address are never handed to an instance.
var (
	hostIPBase   = netip.MustParseAddr("10.100.0.0")
	hostIPOffset = uint32(2)
)

// Lease is the set of unique resources bound to one running instance. It is
// returned by Allocator.Acquire and must be handed back via Allocator.Release
// (by instance id) on teardown or the slot leaks.
type Lease struct {
	Instance string     // caller's instance id (e.g. a UUID); names the netns
	Slot     int        // unique while live; the root of every other field
	UID      int        // jailer --uid
	GID      int        // jailer --gid (== UID)
	HostIP   netip.Addr // routable veth host-side address, 10.100.x.y
	Netns    string     // network namespace name, fc-<instance>
	VethHost string     // host-side veth (≤15 chars, derived from slot)
	VethPeer string     // netns-side veth (≤15 chars, derived from slot)
}

// Allocator hands out unique Leases and recycles slots on release. Safe for
// concurrent use — vmmd may wake many instances at once.
type Allocator struct {
	mu         sync.Mutex
	free       []int          // stack of free slot numbers
	byInstance map[string]int // instance id -> slot, for Release + double-acquire guard
}

// NewAllocator returns an allocator with all MaxSlots free.
func NewAllocator() *Allocator {
	free := make([]int, MaxSlots)
	for i := range free {
		// Hand out low slots first for readable uids/IPs in dev; order is not
		// load-bearing.
		free[i] = MaxSlots - 1 - i
	}
	return &Allocator{free: free, byInstance: make(map[string]int)}
}

// InUse reports how many slots are currently leased.
func (a *Allocator) InUse() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.byInstance)
}

// Acquire leases a unique slot for instance. It errors if the instance already
// holds a lease (a bug — Release first) or the box is at MaxSlots.
func (a *Allocator) Acquire(instance string) (Lease, error) {
	if instance == "" {
		return Lease{}, fmt.Errorf("fcvm: acquire: empty instance id")
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	if _, dup := a.byInstance[instance]; dup {
		return Lease{}, fmt.Errorf("fcvm: acquire: instance %q already holds a lease", instance)
	}
	if len(a.free) == 0 {
		return Lease{}, fmt.Errorf("fcvm: acquire: no free slots (all %d in use)", MaxSlots)
	}

	slot := a.free[len(a.free)-1]
	a.free = a.free[:len(a.free)-1]
	a.byInstance[instance] = slot
	return leaseForSlot(instance, slot), nil
}

// Release returns instance's slot to the free pool. It is idempotent-safe to call
// once per acquired instance; releasing an unknown instance is a no-op error the
// caller may ignore, surfaced for leak detection during tests.
func (a *Allocator) Release(instance string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	slot, ok := a.byInstance[instance]
	if !ok {
		return fmt.Errorf("fcvm: release: instance %q holds no lease", instance)
	}
	delete(a.byInstance, instance)
	a.free = append(a.free, slot)
	return nil
}

// leaseForSlot deterministically derives every resource from the slot. Given a
// unique slot the outputs are unique; that is the whole invariant.
func leaseForSlot(instance string, slot int) Lease {
	return Lease{
		Instance: instance,
		Slot:     slot,
		UID:      JailUIDBase + slot,
		GID:      JailUIDBase + slot,
		HostIP:   hostIPForSlot(slot),
		Netns:    "fc-" + instance,
		VethHost: fmt.Sprintf("vh%d", slot),
		VethPeer: fmt.Sprintf("vp%d", slot),
	}
}

// hostIPForSlot maps a slot into 10.100.0.0/16 starting at .0.2.
func hostIPForSlot(slot int) netip.Addr {
	v := hostIPBase.As4()
	n := uint32(v[0])<<24 | uint32(v[1])<<16 | uint32(v[2])<<8 | uint32(v[3])
	n += hostIPOffset + uint32(slot)
	return netip.AddrFrom4([4]byte{byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)})
}
