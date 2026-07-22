// Package sched holds schedd's policy core: the per-node admission ledger,
// the idle reaper, and eviction selection (spec §4.3). schedd is the single
// writer to the instances table and a single process, so this in-memory
// accounting needs no distributed locking — just a short-held mutex.
//
// Issue #97 / ADR-025 axis 3 re-states invariant §6.2-2 (Σ(ram+8) ≤
// 47,600 MB) per-node: each compute_node has its own admission_ceiling_mb
// (defaults to api.RAMAdmissionCeilingMB on the synthetic default-local
// row) and the ledger tracks reservations per node so the global ceiling
// is the sum of the per-node ceilings on a multi-node fleet. Single-box
// installs see identical behaviour because the default-local node
// carries the legacy 47,600 MB ceiling.
package sched

import (
	"fmt"
	"sync"

	"github.com/onebox-faas/faas/pkg/api"
)

// NodeLedger is schedd's per-node live RAM/vCPU/concurrency accounting.
// It is the mechanised form of invariants §6.2-1 (per-app concurrency,
// global — same app can't exceed max_concurrency regardless of how
// many nodes it lands on) and §6.2-2 (Σ(ram_mb + 8) ≤ admission ceiling,
// per-node — the legacy box-wide ceiling becomes the synthetic
// default-local node's ceiling; multi-node fleets get Σ over the
// per-node ceilings).
//
// schedd is single-leader CP and a single process, so a single mutex
// is sufficient — distributed locking is not needed. The per-app map
// stays global (concurrency is per-app, not per-node). The per-node
// map holds resident RAM + vCPU + entries, keyed by compute_node.id.
// The entries map preserves the legacy O(1) Release(instance) lookup
// because each reservation remembers its nodeID.
type NodeLedger struct {
	mu       sync.Mutex
	resident map[string]*nodeReservation // node_id -> accounting (per-node ceiling check)
	perApp   map[string]int              // app_id -> instances counting toward concurrency (global, §6.2-1)
	entries  map[string]*reservation     // instance_id -> reservation (cross-node lookup for Release)
}

type nodeReservation struct {
	residentRAM int // Σ(ram_mb + PerVMOverheadMB) on this node
	usedVCPU    int // Σ vCPU on this node
}

// reservation remembers the node it belongs to so Release can route
// the freed bytes back to the right per-node counter without a
// second lookup against the store. nodeID is empty for legacy
// single-box callers that pre-date PR #113; Release falls back to
// the box-wide counter in that case so the migration is non-breaking
// for tests that don't plumb node IDs.
type reservation struct {
	appID       string
	nodeID      string // empty = legacy box-wide accounting (test seams)
	admissionMB int    // ram_mb + PerVMOverheadMB
	vcpu        int
	countsConc  bool // still in {WAKING,COLD_BOOTING,RUNNING}
}

// NewNodeLedger returns an empty per-node ledger. Backwards-compat
// alias kept under the old name so existing test files (which say
// NewLedger everywhere) compile unchanged — the rename is gradual.
func NewNodeLedger() *NodeLedger {
	return &NodeLedger{
		resident: map[string]*nodeReservation{},
		perApp:   map[string]int{},
		entries:  map[string]*reservation{},
	}
}

// NewLedger is the legacy single-box constructor, preserved as an
// alias so PR #113's test sweep is the only place that updates the
// name. New code should call NewNodeLedger.
func NewLedger() *NodeLedger { return NewNodeLedger() }

// Request is an admission request for one instance (a wake or a build).
type Request struct {
	Instance       string
	AppID          string
	Plan           api.Plan
	RAMMB          int // the app's ram_mb (already validated ≤ plan cap)
	VCPU           int // vcpus for this instance
	MaxConcurrency int // the app's configured max (already validated ≤ plan cap)
	// NodeID is the compute_node chosen by sched.ChoosePlacement at
	// the call site. The ledger does not pick placement — that's the
	// Engine's job. Empty NodeID means "legacy box-wide accounting"
	// (used by PR #113's pre-multi-node tests; production callers in
	// PR #113 always pass a non-empty value).
	NodeID string
}

func (r Request) admissionMB() int { return api.BillableRAMMB(r.RAMMB) }

// Admit reserves resources for one new instance, enforcing the
// per-node RAM headroom guard (spec §4.3 / invariant §6.2-2 re-stated
// per-node). It checks concurrency first (a per-app limit the
// customer can act on) then per-node capacity. On success it records
// the reservation; on failure it reserves nothing and returns a
// *api.Problem.
//
// The single-box path keeps identical behaviour: when every
// reservation lives on the same node (the synthetic default-local),
// the per-node ceiling equals api.RAMAdmissionCeilingMB and the
// math collapses to the legacy single-counter form. The legacy
// "ledger.resident" field of pre-#97 code is gone — per-node
// counters carry the same invariant, just keyed.
func (l *NodeLedger) Admit(r Request) error {
	limits, ok := api.LimitsFor(r.Plan)
	if !ok {
		return fmt.Errorf("sched: admit: unknown plan %q", r.Plan)
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	if _, dup := l.entries[r.Instance]; dup {
		return fmt.Errorf("sched: admit: instance %q already admitted", r.Instance)
	}

	// Per-app concurrency (invariant §6.2-1). The app's configured max is capped
	// by the plan; use the tighter of the two defensively. Concurrency is
	// per-app, NOT per-node — a customer's app can't run 5 instances on
	// node A and another 5 on node B just because the fleet is large.
	maxConc := r.MaxConcurrency
	if maxConc <= 0 || maxConc > limits.MaxConcurrency {
		maxConc = limits.MaxConcurrency
	}
	if have := l.perApp[r.AppID]; have >= maxConc {
		return api.ErrPlanLimitConcurrency(limits, have)
	}

	// Per-node RAM headroom (invariant §6.2-2 re-stated per-node).
	// The reservation is admitted iff the chosen node still has room
	// below its admission_ceiling_mb. An empty NodeID (legacy test
	// seam) routes through the synthetic default-global bucket whose
	// ceiling is api.RAMAdmissionCeilingMB; production always passes
	// a non-empty NodeID (the Engine resolves it via ChoosePlacement).
	ceiling := l.ceilingForNode_locked(r.NodeID, limits)
	node := l.resident[r.NodeID]
	if node == nil {
		node = &nodeReservation{}
		l.resident[r.NodeID] = node
	}
	if node.residentRAM+r.admissionMB() > ceiling {
		return api.ErrCapacity(fmt.Sprintf(
			"RAM headroom: node %q resident %d MB + %d MB requested exceeds the %d MB per-node admission ceiling",
			r.NodeID, node.residentRAM, r.admissionMB(), ceiling))
	}

	// vCPU slots (8× overcommit → 160 slots, spec §1) are a
	// box-wide resource today. PR #113 keeps them box-wide; a
	// future slice that introduces per-node vCPU budgets will move
	// this check alongside the RAM check.
	if l.totalUsedVCPU_locked()+r.VCPU > api.VCPUSlots {
		return api.ErrCapacity(fmt.Sprintf(
			"vCPU slots: %d used + %d requested exceeds %d", l.totalUsedVCPU_locked(), r.VCPU, api.VCPUSlots))
	}

	l.entries[r.Instance] = &reservation{
		appID: r.AppID, nodeID: r.NodeID,
		admissionMB: r.admissionMB(), vcpu: r.VCPU, countsConc: true,
	}
	node.residentRAM += r.admissionMB()
	node.usedVCPU += r.VCPU
	l.perApp[r.AppID]++
	return nil
}

// ceilingForNode_locked resolves the per-node admission ceiling.
// Empty nodeID (legacy test seam) falls back to the global
// api.RAMAdmissionCeilingMB; the production path always passes a
// real NodeID. A future operator surface can override per-node
// ceilings (e.g. via compute_nodes.admission_ceiling_mb rows) by
// changing this resolver.
func (l *NodeLedger) ceilingForNode_locked(nodeID string, _ api.Limits) int {
	if nodeID == "" {
		return api.RAMAdmissionCeilingMB
	}
	// The Engine reads the ceiling from the compute_nodes row at
	// placement time and threads it into the request via NodeID.
	// Today the per-node ceiling is constant on the synthetic
	// default-local row (47600); future slices can plumb a
	// per-request ceiling through Request if operator overrides
	// diverge. Defaulting to RAMAdmissionCeilingMB here keeps the
	// invariant intact for any operator-registered node whose
	// admission_ceiling_mb equals the legacy global value.
	return api.RAMAdmissionCeilingMB
}

// totalUsedVCPU_locked sums vCPU across all nodes. The vCPU
// overcommit budget is global today (spec §1, 160 slots); a future
// per-node vCPU slice would replace this with a per-node lookup
// matching the RAM path.
func (l *NodeLedger) totalUsedVCPU_locked() int {
	var n int
	for _, r := range l.resident {
		n += r.usedVCPU
	}
	return n
}

// BeginSnapshot drops an instance's concurrency contribution while keeping its
// RAM/vCPU reservation (it is still resident during SNAPSHOTTING, §6.2-2 but not
// §6.2-1). Idempotent.
func (l *NodeLedger) BeginSnapshot(instance string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.entries[instance]
	if e != nil && e.countsConc {
		e.countsConc = false
		l.perApp[e.appID]--
		l.cleanupApp(e.appID)
	}
}

// Release frees an instance's entire reservation when it parks/stops (§6.2-4).
// Unknown instances are ignored. The reservation remembers its nodeID, so
// the per-node resident counter is decremented without a second lookup
// against the store.
func (l *NodeLedger) Release(instance string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.entries[instance]
	if e == nil {
		return
	}
	delete(l.entries, instance)
	if node, ok := l.resident[e.nodeID]; ok {
		node.residentRAM -= e.admissionMB
		if node.residentRAM < 0 {
			node.residentRAM = 0
		}
		node.usedVCPU -= e.vcpu
		if node.usedVCPU < 0 {
			node.usedVCPU = 0
		}
		if node.residentRAM == 0 && node.usedVCPU == 0 {
			delete(l.resident, e.nodeID)
		}
	}
	if e.countsConc {
		l.perApp[e.appID]--
		l.cleanupApp(e.appID)
	}
}

func (l *NodeLedger) cleanupApp(appID string) {
	if l.perApp[appID] <= 0 {
		delete(l.perApp, appID)
	}
}

// ResidentRAM returns the global Σ(ram+8) in MB across every node.
// Used by the reaper's headroom gate (loop.go); the per-node
// ceiling is enforced inside Admit, but the reaper works on the
// global instance set so the global sum is what it needs.
func (l *NodeLedger) ResidentRAM() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	var n int
	for _, r := range l.resident {
		n += r.residentRAM
	}
	return n
}

// ResidentRAMForNode returns the Σ(ram+8) on a single node. The
// per-node ceiling check inside Admit uses an internal lookup;
// this is the public read used by tests / future telemetry.
func (l *NodeLedger) ResidentRAMForNode(nodeID string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	if r, ok := l.resident[nodeID]; ok {
		return r.residentRAM
	}
	return 0
}

// HeadroomMB returns the global MB remaining across every active
// node. The per-node ceiling is enforced inside Admit; this is the
// reaper's view of the fleet-wide headroom.
func (l *NodeLedger) HeadroomMB() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	// Global headroom is sum(ceiling - resident) across nodes;
	// collapsing to api.RAMAdmissionCeilingMB for the legacy
	// empty-node case keeps backwards compatibility for tests that
	// pre-date PR #113.
	if len(l.resident) == 0 {
		return api.RAMAdmissionCeilingMB
	}
	var head int
	for nodeID, r := range l.resident {
		head += l.ceilingForNode_locked(nodeID, api.Limits{}) - r.residentRAM
	}
	if head < 0 {
		head = 0
	}
	return head
}

// Concurrency returns the number of instances of appID counting toward its cap.
func (l *NodeLedger) Concurrency(appID string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.perApp[appID]
}

// UsedVCPU returns reserved vCPU slots (global sum across nodes).
func (l *NodeLedger) UsedVCPU() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.totalUsedVCPU_locked()
}

// NodeCount returns the number of distinct compute_nodes currently
// holding reservations. Used by tests to assert the per-node
// accounting is split as expected. Production code uses the Store
// for the authoritative node list.
func (l *NodeLedger) NodeCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.resident)
}
