package fcvm

import (
	"fmt"
	"net/netip"
	"sync"

	"github.com/onebox-faas/faas/pkg/api"
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
//
// Mega-PR-B (issue #911 / ADR-110 Tier-1 BLOCKING Commit 1) lifts the
// bridge CIDR from a Go const into per-host config (pkg/api.DefaultHostBridgeCIDR
// + cmd/vmmd/config.go::ComputeNodeConfig.HostBridgeCIDR). Single-host
// dev keeps the legacy 10.100.0.0 base; multi-host deployments call
// Allocator.SetHostIPBase before Acquire so per-instance host IPs land
// in the operator's chosen /16.
var (
	// hostIPBase is the per-host bridge network address the veth
	// host-side allocation starts from (spec §7, 10.100.x.y/16).
	// Slot 0 maps to hostIPBase + hostIPOffset so the bridge address
	// (hostIPBase + 1) and the network address (hostIPBase + 0) are
	// reserved for the bridge itself.
	//
	// Mega-PR-B Commit 1 (issue #911 / ADR-110 Tier-1 BLOCKING) lifts
	// the bridge CIDR from a Go const into per-host config
	// (pkg/api.DefaultHostBridgeCIDR + cmd/vmmd/config.go::ComputeNode
	// Config.HostBridgeCIDR). Single-host dev keeps the legacy
	// 10.100.0.0 base; multi-host deployments call Allocator.SetHostIP
	// Base before Acquire so per-instance host IPs land in the
	// operator's chosen /16.
	//
	// hostIPBaseMu guards hostIPBase + hostIPOffset. Acquire/Release
	// take the read lock for the duration of one hostIPForSlot call;
	// SetHostIPBase takes the write lock for the duration of the swap.
	// Not strictly required by the single-call-site discipline (vmmd
	// main calls SetHostIPBase exactly once at boot, before any
	// Acquire), but the package-level mutable makes the race
	// detector the only tripwire without this — the lock makes it
	// silent-by-construction even if a future test or external caller
	// races the two operations. Mega-PR-B review M1.
	hostIPBaseMu sync.RWMutex
	hostIPBase   = netip.MustParseAddr("10.100.0.0")
	hostIPOffset = uint32(2)
)

// SetHostIPBase overrides the per-instance veth host-side address base
// (Mega-PR-B Commit 1). Pass the network address of the per-host /16
// (e.g. 10.101.0.0 for a 10.101.0.0/16 deployment). The bridge IP
// (hostIPBase + 1) and the network address (hostIPBase + 0) are
// reserved — slot 0 maps to hostIPBase + hostIPOffset so the allocator
// never hands them out. The setter is additive; legacy callers that
// don't invoke it keep the v1 single-host 10.100.0.0 base.
//
// Cheap to call repeatedly — the RWMutex write-lock path is fast and
// the swap is a single word (netip.Addr is a 16-byte value but Go's
// interface boxing doesn't apply here, so it's an atomic-style
// assignment under the lock).
//
// Safe for concurrent calls with Acquire/Release. vmmd main calls
// SetHostIPBase exactly once at boot, before any Acquire; the lock
// documents the contract but does not depend on it.
func SetHostIPBase(addr netip.Addr) {
	hostIPBaseMu.Lock()
	defer hostIPBaseMu.Unlock()
	hostIPBase = addr
}

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
	// Plan is the apps row's owning plan tier (issue #301, ADR-044).
	// Stamped at alloc time so every downstream consumer (Boot,
	// Restore, Destroy, Kill) reads the same plan without a separate
	// map lookup. Empty for pre-issue-301 callers (legacy 2-level
	// hierarchy); see ParentCgroupFor for the empty fallback.
	Plan api.Plan
	// IsBuilder selects the dedicated faas-cp-build.slice cgroup for an
	// ephemeral builder VM. Builder memory must not be charged to vmmd's
	// supervisor cgroup or to tenant RAM.
	IsBuilder bool
	// BuildTimeoutSec is the guest build wall-clock budget carried from
	// builderd. vmmd uses it to size builder teardown headroom; zero keeps
	// the platform default for legacy callers and ordinary app VMs.
	BuildTimeoutSec int
	// MemoryMaxMiB is the requested VM memory fence carried into the jailer
	// command. The jailer creates the per-VM cgroup as root, so memory.max is
	// set there before it drops privileges; vmmd's post-boot CPU fence remains
	// a separate write.
	MemoryMaxMiB int
	// CPUMillicores is the app-selected sustained CPU quota. Zero keeps the
	// plan-derived legacy quota for internal callers and builder paths.
	CPUMillicores int
}

// Allocator hands out unique Leases and recycles slots on release. Safe for
// concurrent use — vmmd may wake many instances at once.
type Allocator struct {
	mu         sync.Mutex
	free       []int          // stack of free slot numbers
	byInstance map[string]int // instance id -> slot, for Release + double-acquire guard
	reserved   map[string]int // fresh networks only; excluded from VM admission counts
}

// reserveNetwork takes a slot without claiming a running VM. The cache must
// either adopt it exactly once or return it after tearing down its network.
func (a *Allocator) reserveNetwork(id string) (Lease, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if id == "" || len(a.free) == 0 {
		return Lease{}, fmt.Errorf("fcvm: reserve network: empty id or no free slots")
	}
	if _, ok := a.byInstance[id]; ok {
		return Lease{}, fmt.Errorf("fcvm: reserve network: id already leased")
	}
	if _, ok := a.reserved[id]; ok {
		return Lease{}, fmt.Errorf("fcvm: reserve network: id already reserved")
	}
	if a.reserved == nil {
		a.reserved = make(map[string]int)
	}
	slot := a.free[len(a.free)-1]
	a.free = a.free[:len(a.free)-1]
	a.reserved[id] = slot
	return leaseForSlot(id, slot), nil
}

func (a *Allocator) releaseNetwork(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if slot, ok := a.reserved[id]; ok {
		delete(a.reserved, id)
		a.free = append(a.free, slot)
	}
}

func (a *Allocator) adoptNetwork(reservation, instance string) (Lease, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if instance == "" {
		return Lease{}, fmt.Errorf("fcvm: adopt network: empty instance")
	}
	if _, ok := a.byInstance[instance]; ok {
		return Lease{}, fmt.Errorf("fcvm: adopt network: instance already leased")
	}
	if _, ok := a.reserved[instance]; ok {
		return Lease{}, fmt.Errorf("fcvm: adopt network: instance id reserved")
	}
	slot, ok := a.reserved[reservation]
	if !ok {
		return Lease{}, fmt.Errorf("fcvm: adopt network: reservation missing")
	}
	delete(a.reserved, reservation)
	a.byInstance[instance] = slot
	return leaseForSlot(instance, slot), nil
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
	if _, dup := a.reserved[instance]; dup {
		return Lease{}, fmt.Errorf("fcvm: acquire: instance %q is reserved", instance)
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
// Takes the read lock so a concurrent SetHostIPBase serializes
// against the load; reads see either the old or new value, never a
// torn one. Mega-PR-B review M1.
func hostIPForSlot(slot int) netip.Addr {
	hostIPBaseMu.RLock()
	base := hostIPBase
	hostIPBaseMu.RUnlock()
	v := base.As4()
	n := uint32(v[0])<<24 | uint32(v[1])<<16 | uint32(v[2])<<8 | uint32(v[3])
	n += hostIPOffset + uint32(slot)
	return netip.AddrFrom4([4]byte{byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)})
}

// ---------------------------------------------------------------------------
// ADR-119 redesign: per-VM static-egress IP pool
// ---------------------------------------------------------------------------
//
// The legacy design (PR-997 round-1) attached SNAT to the per-netns
// chain, which was dead code (nftables NAT is first-match + terminal).
// The redesign moves SNAT to the host renderer, which emits one
//
//     ip saddr <per-vm-host-ip> oifname <PublicIface> snat to <CustomerIP>
//
// rule per live VM of a static-egress-pinned app. The per-VM host IP
// is what the kernel uses for the `ip saddr` match (the bridged
// packet's source on its way out PublicIface) — distinct from the
// customer's IP (the SNAT target).
//
// Why a separate pool (10.200.0.0/16, not 10.100.0.0/16):
//   1. The legacy per-VM /16 (10.100.x.y) is the bridge /16 — every
//      bridged tenant VM's host-side IP falls in this range. A
//      per-VM host IP for a static-egress rule that ALSO sits in
//      10.100.x.y would alias-collide with the bridge plumbing
//      (the conntrack matches PerVMHostIP, not the bridge).
//   2. The renderer panic-gates PerVMHostIP ∉ MasqueradeCIDR; using
//      a separate /16 makes the gate trivially safe (MasqueradeCIDR
//      is 10.100.0.0/16, the static-egress pool is 10.200.0.0/16).
//   3. Capacity: 10.200.x.y/16 is 65,534 slots — far in excess of
//      the v1 Scale-plan concurrency ceiling (100 apps × ~1 live
//      VM per app = 100 slots out of 65,534).
//
// The reservation is keyed by (accountID, appID). Idempotent on
// re-acquire for the same (account, app, customerIP) tuple. Release
// happens on customer-clear (DELETE /v1/apps/{slug}/static-egress-ip)
// NOT on per-VM teardown — a customer's IP stays available across
// Scale-driven wakes so the wake path never races a release.

const (
	// staticEgressPoolBase is the network address of the
	// per-VM static-egress pool (10.200.0.0/16). Distinct from
	// hostIPBase (10.100.0.0/16) so the per-VM host IPs used
	// in `ip saddr` cannot alias-collide with the bridge /16.
	staticEgressPoolBase = "10.200.0.0"
	// staticEgressPoolMax is the capacity of the static-egress
	// pool (the /16 minus network + broadcast). 65,534 slots.
	staticEgressPoolMax = 65534
)

// StaticEgressReservation is the (account, app, customer-IP,
// per-VM host-IP) tuple vmmd keeps in memory + persists to the
// operator bundle. The host renderer consumes the perVMHostIP +
// customerIP pair; the accountID + appID are metadata for the
// rendered rule's trailing comment.
type StaticEgressReservation struct {
	AccountID       string
	AppID           string
	CustomerIP      netip.Addr
	PerVMHostIP     netip.Addr
	reservationSlot int // internal: the slot index in the pool
}

// staticEgressPool is the package-level reservation store. The map
// is keyed by appID (one reservation per app, even if the same
// account has multiple pinned apps). The free-slice is a stack of
// free slot indices, popped on acquire and pushed on release — same
// discipline as the legacy Acquire/Release pair, so the underlying
// allocation order is deterministic in dev.
var staticEgressPool = struct {
	mu sync.Mutex
	// byAppID: appID → reservation. Idempotent acquire on
	// (accountID, appID, customerIP) returns the existing
	// reservation; a different customerIP for the same
	// (accountID, appID) rotates (release-old + acquire-new).
	byAppID map[string]StaticEgressReservation
	// bySlot: slot index → appID (inverse lookup so release
	// is O(1)).
	bySlot map[int]string
	// free: stack of free slot indices. Popped on acquire,
	// pushed on release. Initialised lazily by the first
	// acquire call.
	free []int
	// nextFresh: monotonically increasing counter for fresh
	// slot allocations when free is empty. Capped at
	// staticEgressPoolMax.
	nextFresh int
}{
	byAppID:   make(map[string]StaticEgressReservation),
	bySlot:    make(map[int]string),
	nextFresh: 0,
}

// AcquireStaticEgressIP reserves a (per-VM host IP, customer IP)
// tuple for an (accountID, appID) pair. The per-VM host IP is
// allocated from the separate 10.200.0.0/16 pool (so it cannot
// collision with the per-VM bridge /16 at 10.100.x.y).
//
// Idempotency:
//   - Same (accountID, appID, customerIP) → returns the existing
//     reservation untouched (Wakes re-issue the same request).
//   - Same (accountID, appID) with a DIFFERENT customerIP →
//     releases the old per-VM host IP slot, allocates a fresh one
//     for the new customerIP. The old per-VM host IP returns to
//     the free pool and is reused by a future acquire.
//   - Different (accountID, appID) → allocates a fresh slot
//     (no sharing across apps, even within the same account).
//
// Errors:
//   - api.ErrValidation for a customerIP that fails the canonical
//     ValidateStaticEgressIP deny-set (the canonical validator
//     rejects RFC1918, link-local, multicast, loopback, 0.0.0.0/8,
//     CGN — see pkg/api/static_egress_ip_validate.go).
//   - ErrStaticEgressPoolExhausted when the /16 is full (v1
//     capacity is 65,534; the Scale-plan ceiling is 100 apps
//     per node so this is unreachable in practice, but the
//     guard is load-bearing for the future).
//
// The returned StaticEgressReservation is the canonical form
// the vmmd Manager pushes into the host renderer. Persistence
// to the operator TOML happens via the manager's call to
// SetStaticEgressIPAliases (which already handles the per-app
// bridge alias lifecycle).
func AcquireStaticEgressIP(accountID, appID string, customerIP netip.Addr) (StaticEgressReservation, error) {
	if accountID == "" {
		return StaticEgressReservation{}, fmt.Errorf("fcvm: AcquireStaticEgressIP: empty account_id")
	}
	if appID == "" {
		return StaticEgressReservation{}, fmt.Errorf("fcvm: AcquireStaticEgressIP: empty app_id")
	}
	if !customerIP.IsValid() {
		return StaticEgressReservation{}, fmt.Errorf("fcvm: AcquireStaticEgressIP: invalid customer_ip")
	}
	// Run the canonical deny-set validator. Pulls in
	// pkg/api.ValidateStaticEgressIP as the single source of
	// truth — the hard-coded validCustomerStaticEgressIP /
	// validStaticEgressIPAddr / validCustomerStaticEgressIPMetal
	// copies were all deleted in the redesign.
	if err := api.ValidateStaticEgressIP(customerIP); err != nil {
		return StaticEgressReservation{}, fmt.Errorf("fcvm: AcquireStaticEgressIP: %w", err)
	}

	staticEgressPool.mu.Lock()
	defer staticEgressPool.mu.Unlock()

	// Idempotent on (accountID, appID, customerIP).
	if existing, ok := staticEgressPool.byAppID[appID]; ok {
		if existing.AccountID != accountID {
			// appID collision across accounts — refuse rather
			// than silently overwrite. The apid handler
			// protects against this via the
			// apps_static_egress_ip partial unique index, but
			// the allocator must defend in depth.
			return StaticEgressReservation{}, fmt.Errorf("fcvm: AcquireStaticEgressIP: app %s already reserved under account %s", appID, existing.AccountID)
		}
		if existing.CustomerIP == customerIP {
			return existing, nil
		}
		// Rotation: release the old slot, fall through to
		// fresh allocation.
		delete(staticEgressPool.bySlot, existing.reservationSlot)
		staticEgressPool.free = append(staticEgressPool.free, existing.reservationSlot)
		delete(staticEgressPool.byAppID, appID)
	}

	// Allocate a fresh slot.
	slot, err := allocStaticEgressSlot()
	if err != nil {
		return StaticEgressReservation{}, err
	}
	perVMHostIP := staticEgressHostIPForSlot(slot)
	res := StaticEgressReservation{
		AccountID:       accountID,
		AppID:           appID,
		CustomerIP:      customerIP,
		PerVMHostIP:     perVMHostIP,
		reservationSlot: slot,
	}
	staticEgressPool.byAppID[appID] = res
	staticEgressPool.bySlot[slot] = appID
	return res, nil
}

// ReleaseStaticEgressIP returns the per-VM host IP slot for
// (accountID, appID) to the free pool. Idempotent on a
// non-existent appID (returns nil — used in the customer-clear
// path where the apid handler may re-issue the delete).
//
// Release is the customer-clear path (DELETE /v1/apps/{slug}/
// static-egress-ip), NOT the per-VM teardown path. A live VM
// tearing down does NOT release the reservation — the customer's
// IP stays available so the next wake (Scale-driven) reissues
// the same per-VM host IP. The pool only drains via the
// apid-side clear.
func ReleaseStaticEgressIP(accountID, appID string) error {
	staticEgressPool.mu.Lock()
	defer staticEgressPool.mu.Unlock()
	existing, ok := staticEgressPool.byAppID[appID]
	if !ok {
		return nil
	}
	if existing.AccountID != accountID {
		return fmt.Errorf("fcvm: ReleaseStaticEgressIP: app %s reserved under account %s, not %s", appID, existing.AccountID, accountID)
	}
	delete(staticEgressPool.byAppID, appID)
	delete(staticEgressPool.bySlot, existing.reservationSlot)
	staticEgressPool.free = append(staticEgressPool.free, existing.reservationSlot)
	return nil
}

// StaticEgressReservationFor returns the active reservation for
// appID, or false if no reservation is active. Used by the vmmd
// Manager at Wake time to populate the per-VM host IP without
// re-acquiring (the reservation is idempotent on re-acquire, but
// the lookup is cheaper and signals intent — "this is the
// existing one", not "I want a new one").
func StaticEgressReservationFor(appID string) (StaticEgressReservation, bool) {
	staticEgressPool.mu.Lock()
	defer staticEgressPool.mu.Unlock()
	r, ok := staticEgressPool.byAppID[appID]
	return r, ok
}

// allocStaticEgressSlot pops a free slot or allocates a fresh one.
// Lock is held by the caller.
func allocStaticEgressSlot() (int, error) {
	if n := len(staticEgressPool.free); n > 0 {
		slot := staticEgressPool.free[n-1]
		staticEgressPool.free = staticEgressPool.free[:n-1]
		return slot, nil
	}
	if staticEgressPool.nextFresh >= staticEgressPoolMax {
		return 0, fmt.Errorf("fcvm: AcquireStaticEgressIP: pool exhausted (%d slots)", staticEgressPoolMax)
	}
	slot := staticEgressPool.nextFresh
	staticEgressPool.nextFresh++
	return slot, nil
}

// staticEgressHostIPForSlot maps a slot index to the per-VM host
// IP (10.200.x.y). Slot 0 → 10.200.0.2, slot 1 → 10.200.0.3,
// etc. The /16 base is distinct from hostIPBase (10.100.0.0/16).
//
// The +2 offset mirrors the per-VM pool (hostIPOffset = 2) so
// slot 0 = .0.2 (the .0 is network, .1 is reserved for the
// bridge-equivalent — the static-egress pool has no bridge
// because it's host-side-only, but the offset is preserved for
// symmetry with the per-VM pool).
func staticEgressHostIPForSlot(slot int) netip.Addr {
	base := netip.MustParseAddr(staticEgressPoolBase)
	v := base.As4()
	n := uint32(v[0])<<24 | uint32(v[1])<<16 | uint32(v[2])<<8 | uint32(v[3])
	n += hostIPOffset + uint32(slot)
	return netip.AddrFrom4([4]byte{byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)})
}
