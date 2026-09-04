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
	mu               sync.Mutex
	resident         map[string]*nodeReservation // node_id -> accounting (per-node ceiling check)
	perApp           map[string]int              // app_id -> instances counting toward concurrency (global, §6.2-1)
	perAppDeployment map[string]int              // app_id|"\x00"|deployment_id -> per-deployment concurrency (ADR-072, issue #557 closure)
	entries          map[string]*reservation     // instance_id -> reservation (cross-node lookup for Release)
}

type nodeReservation struct {
	residentRAM int // Σ(ram_mb + PerVMOverheadMB) on this node
	usedVCPU    int // Σ vCPU on this node
	ceilingMB   int // latest per-node RAM admission ceiling
}

// reservation remembers the node it belongs to so Release can route
// the freed bytes back to the right per-node counter without a
// second lookup against the store. nodeID is empty for legacy
// single-box callers that pre-date PR #113; Release falls back to
// the box-wide counter in that case so the migration is non-breaking
// for tests that don't plumb node IDs.
type reservation struct {
	appID        string
	deploymentID string // empty = legacy pre-#557 reservations (test seams); populated post-#557 via Admit's DeploymentID field
	nodeID       string // empty = legacy box-wide accounting (test seams)
	admissionMB  int    // ram_mb + PerVMOverheadMB
	vcpu         int
	countsConc   bool // still in {WAKING,COLD_BOOTING,RUNNING}
}

// NewNodeLedger returns an empty per-node ledger. Backwards-compat
// alias kept under the old name so existing test files (which say
// NewLedger everywhere) compile unchanged — the rename is gradual.
func NewNodeLedger() *NodeLedger {
	return &NodeLedger{
		resident:         map[string]*nodeReservation{},
		perApp:           map[string]int{},
		perAppDeployment: map[string]int{},
		entries:          map[string]*reservation{},
	}
}

// NewLedger is the legacy single-box constructor, preserved as an
// alias so PR #113's test sweep is the only place that updates the
// name. New code should call NewNodeLedger.
func NewLedger() *NodeLedger { return NewNodeLedger() }

// Kind discriminates the reservation shape so the ledger honours the
// right invariants per request class. The default zero value
// (KindWake) preserves byte-compatibility with pre-Tier-A5 callers —
// every existing Admit site that builds a literal Request struct gets
// KindWake and the per-app concurrency path runs unchanged.
type Kind uint8

const (
	// KindWake (default) is a standard wake-side reservation: counts
	// toward per-app concurrency (§6.2-1), per-node RAM (§6.2-2),
	// and per-node vCPU. Used by Wake / AdmitInstance.
	KindWake Kind = iota
	// KindMigration is a Tier A5 cross-node live-instance handoff
	// reservation: counts toward per-node RAM and vCPU (§6.2-2) but
	// NOT per-app concurrency (§6.2-1) — the migration target was
	// already counted in the source node's per-app concurrency, so
	// bumping here would double-count and artificially cap an app
	// that is mid-migration. Tier A5 (ADR-066) wires this into the
	// destination schedd at Phase 3 of the four-phase handoff.
	KindMigration
	// KindJob (issue #1184 Workstream A / ADR-099) is a run-to-
	// completion workload reservation: counts toward per-node RAM
	// (§6.2-2) and per-node vCPU, but NOT per-app concurrency
	// (§6.2-1) — jobs do not belong to an app. Per-account
	// concurrency is enforced separately via the
	// api.JobConcurrentPerAccount cap on the dispatch tick (M5),
	// which gates BEFORE the ledger Admit so the per-node ceiling
	// is only checked once the per-account pool has headroom.
	KindJob
)

// Request is an admission request for one instance (a wake or a build).
type Request struct {
	Instance string
	AppID    string
	// DeploymentID is the target deployment for the wake (ADR-072 /
	// issue #557 closure). The Engine resolves it from the wake
	// target before calling Admit — the gateway passes the latest
	// ready deployment id, the floor trigger passes the per-sweep
	// deployment id. Empty is the legacy single-box posture
	// (test seams + pre-#557 reservations); the per-deployment
	// concurrency counter is only incremented when this is non-empty.
	DeploymentID string
	Plan         api.Plan
	RAMMB        int // the app's ram_mb (already validated ≤ plan cap)
	// SidecarMBs (issue #463 / ADR-070 §Decision 6 / PR-C) is the
	// per-sidecar RAM slice sourced from the deployment's
	// `sidecars jsonb` column at Admit time. Each entry adds to the
	// billable shutter via `api.BillableRAMMBWithSidecars`; the cap
	// enforcement (SidecarCapMax = 2) happens upstream in apid's
	// Sidecar.Validate and the schema CHECK on migration 00118, so
	// the ledger trusts len(SidecarMBs) ≤ 2 and never re-checks it.
	// Nil or empty = legacy no-sidecar shape; BillableRAMMB
	// (single-arg form) collapses to the same math in that case.
	SidecarMBs     []int
	VCPU           int // vcpus for this instance
	MaxConcurrency int // the app's configured max (already validated ≤ plan cap)
	// Kind discriminates the reservation shape (see Kind doc). Zero
	// value (KindWake) is the standard wake path; KindMigration is
	// the Tier A5 destination-side reservation.
	Kind Kind
	// NodeID is the compute_node chosen by sched.ChoosePlacement at
	// the call site. The ledger does not pick placement — that's the
	// Engine's job. Empty NodeID means "legacy box-wide accounting"
	// (used by PR #113's pre-multi-node tests; production callers in
	// PR #113 always pass a non-empty value).
	NodeID string
	// PreferredNodeID is the sticky-warm affinity hint (placement
	// scheduler PR, ADR-025). The Engine populates this from
	// pkg/sched.WarmAffinity.LastWarmNode(AppID) before calling the
	// chooser; an empty value means "no hint, fall through to
	// least-loaded RAM headroom". The chooser honors this only when
	// the preferred node still has headroom — affinity is bias, never
	// a gate (ADR-005: cold boot must always work).
	PreferredNodeID string
	// PreferredNodeIDs is the set of compute nodes whose local cache has a
	// complete copy of the selected snapshot (issue #1054). It is a bias,
	// never a capacity gate: if every ready replica is full, placement falls
	// through to the normal fleet chooser and the shared backend remains the
	// cold-restore fallback.
	PreferredNodeIDs []string
	// PreferredRegion (ADR-098 PR-D + amendment issue #954) is
	// the connection-aware placement bias, scoped to a single
	// deployment. The Engine populates this from
	// pkg/sched.UpstreamAffinity.Score(appID, deploymentScope)
	// before calling the
	// chooser; the chooser honors it via the upstream_fit
	// tie-break in betterCandidate. Empty value means "no
	// score, fall through to legacy tie-break" — the cache
	// cold path (no probe yet) MUST NOT bias the chooser. Like
	// PreferredNodeID, the region is bias, never a gate: a
	// preferred region with no headroom falls through to the
	// least-loaded path (ADR-005 cold-boot invariant).
	PreferredRegion string
	// NodeCeilingMB is the per-node RAM admission ceiling from
	// compute_nodes.admission_ceiling_mb for the chosen node. The
	// chooser already verified the request fits; the ledger uses this
	// instead of the legacy global api.RAMAdmissionCeilingMB so a
	// node with a smaller ceiling enforces that smaller cap. Zero or
	// negative falls back to api.RAMAdmissionCeilingMB (safe for
	// un-registered nodes and pre-multi-node test seams).
	NodeCeilingMB int
	// VCPUBudget is the per-node vCPU admission budget from
	// compute_nodes.vcpu_budget for the chosen node (Tier A2,
	// migration 00123). The chooser already verified the request
	// fits; the ledger uses this instead of the legacy box-wide
	// api.VCPUSlots so a node with a smaller budget enforces that
	// smaller cap. Zero or negative falls back to api.VCPUSlots
	// (safe for un-registered nodes and pre-multi-node test seams).
	VCPUBudget int
}

func (r Request) admissionMB() int {
	return api.BillableRAMMBWithSidecars(r.RAMMB, r.SidecarMBs)
}

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
	// Tier A5 / ADR-066: KindMigration skips LimitsFor. The
	// migration target's plan limits are already enforced on the
	// source node's Wake-time Admit; on the destination we only
	// need the per-node RAM + vCPU ceilings (invariant §6.2-2),
	// which use r.NodeCeilingMB / r.VCPUBudget directly. The
	// per-app concurrency check below is also skipped for
	// KindMigration, so Plan is unused on this path. We still
	// validate Plan for KindWake so a malformed caller doesn't
	// slip past the per-app concurrency check by leaving Plan
	// empty.
	var limits api.Limits
	if r.Kind != KindMigration {
		l, ok := api.LimitsFor(r.Plan)
		if !ok {
			return fmt.Errorf("sched: admit: unknown plan %q", r.Plan)
		}
		limits = l
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
	//
	// Tier A5 / ADR-066: KindMigration reservations SKIP this check.
	// The migration target was already counted in the source node's
	// per-app concurrency (the instance was RUNNING there before
	// Phase 1's Park). Counting it again on the destination would
	// artificially cap an app that's mid-migration — a customer with
	// 1 instance at MaxConcurrency=1 would see a transient ErrPlan
	// during the failover window even though no extra instance was
	// ever admitted. RAM/vCPU per-node ceilings (below) still apply.
	//
	// KindJob (issue #1184 / ADR-099) ALSO skips this check — jobs
	// have no appID. Per-account concurrency is enforced at the
	// dispatch tick (M5) via api.JobConcurrentPerAccount before the
	// ledger Admit is even called. RAM/vCPU per-node ceilings still
	// apply so a job fan-out can't blow the tenant budget.
	maxConc := r.MaxConcurrency
	if r.Kind != KindMigration && r.Kind != KindJob {
		if maxConc <= 0 || maxConc > limits.MaxConcurrency {
			maxConc = limits.MaxConcurrency
		}
		if have := l.perApp[r.AppID]; have >= maxConc {
			return api.ErrPlanLimitConcurrency(limits, have)
		}
	}

	// Per-node RAM headroom (invariant §6.2-2 re-stated per-node).
	// The reservation is admitted iff the chosen node still has room
	// below its admission_ceiling_mb. The Engine reads the row
	// ceiling from compute_nodes and threads it into the Request;
	// r.NodeCeilingMB > 0 means a real row ceiling; ≤ 0 falls back
	// to the global api.RAMAdmissionCeilingMB (the legacy single-box
	// posture, and the safe default for un-registered nodes).
	ceiling := r.NodeCeilingMB
	if ceiling <= 0 {
		ceiling = l.ceilingForNode_locked(r.NodeID, limits)
	}
	node := l.resident[r.NodeID]
	if node == nil {
		node = &nodeReservation{ceilingMB: ceiling}
		l.resident[r.NodeID] = node
	} else if r.NodeCeilingMB > 0 {
		// Keep the aggregate headroom view aligned with the same row
		// ceiling used for this admission. Operators may tune a node's
		// ceiling while the process is running; the next admission then
		// refreshes the stored value for subsequent floor decisions.
		node.ceilingMB = ceiling
	}
	if node.residentRAM+r.admissionMB() > ceiling {
		return api.ErrCapacity(fmt.Sprintf(
			"RAM headroom: node %q resident %d MB + %d MB requested exceeds the %d MB per-node admission ceiling",
			r.NodeID, node.residentRAM, r.admissionMB(), ceiling))
	}

	// Per-node vCPU headroom (Tier A2, migration 00123). Replaces
	// the legacy box-wide api.VCPUSlots gate. The Engine reads the
	// row budget from compute_nodes.vcpu_budget and threads it
	// into the Request; r.VCPUBudget > 0 means a real row budget;
	// ≤ 0 falls back to api.VCPUSlots (the legacy single-box
	// posture, and the safe default for un-registered nodes).
	// This check sits alongside the RAM check so a node's RAM
	// and vCPU ceilings are enforced together — a heterogeneous
	// fleet with one RAM-rich + vCPU-poor box and one vCPU-rich
	// + RAM-poor box now routes traffic correctly.
	vcpuCeiling := r.VCPUBudget
	if vcpuCeiling <= 0 {
		vcpuCeiling = api.VCPUSlots
	}
	if node.usedVCPU+r.VCPU > vcpuCeiling {
		return api.ErrCapacity(fmt.Sprintf(
			"vCPU headroom: node %q busy %d + %d requested exceeds the %d per-node vCPU budget",
			r.NodeID, node.usedVCPU, r.VCPU, vcpuCeiling))
	}

	l.entries[r.Instance] = &reservation{
		appID: r.AppID, deploymentID: r.DeploymentID, nodeID: r.NodeID,
		admissionMB: r.admissionMB(), vcpu: r.VCPU,
		countsConc: r.Kind != KindMigration,
	}
	node.residentRAM += r.admissionMB()
	node.usedVCPU += r.VCPU
	if r.Kind != KindMigration {
		l.perApp[r.AppID]++
		if r.DeploymentID != "" {
			l.perAppDeployment[r.AppID+"\x00"+r.DeploymentID]++
		}
	}
	return nil
}

// ceilingForNode_locked resolves the per-node admission ceiling.
// Empty nodeID (legacy test seam) falls back to the global
// api.RAMAdmissionCeilingMB; the production path always passes a
// real NodeID plus a non-zero NodeCeilingMB on the Request (the
// Engine threads the row value from compute_nodes.admission_ceiling_mb
// at placement time). Per-row ceilings from migrations/00024 let
// operators tune a node's cap below the legacy 47,600 MB global
// without changing api.RAMAdmissionCeilingMB — the chooser already
// verified headroom at placement time, this is the load-bearing
// enforcement that survives a stale hint.
func (l *NodeLedger) ceilingForNode_locked(nodeID string, _ api.Limits) int {
	// Per-node ceiling enforcement lives on Request.NodeCeilingMB
	// (admission.go:107), populated by the chooser from
	// compute_nodes.admission_ceiling_mb at placement time. This
	// resolver is the safe fallback for callers that didn't thread
	// the row ceiling through the Request — i.e. un-registered
	// nodes, pre-multi-node test seams, and external callers that
	// build a Request with NodeCeilingMB=0. The global constant
	// is the floor in every case (CLAUDE.md invariant 2 — fleet
	// RAM admission ceiling 47,600 MB).
	return api.RAMAdmissionCeilingMB
}

// totalUsedVCPU_locked sums vCPU across all nodes. The fleet-wide
// sum is informational (the per-node gate is enforced inside Admit
// via Request.VCPUBudget); reaper/observability code reads it via
// the public UsedVCPU accessor. A multi-node fleet's fleet sum can
// grow past 160 — the per-node budget is the load-bearing check.
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
		if e.deploymentID != "" {
			key := e.appID + "\x00" + e.deploymentID
			l.perAppDeployment[key]--
			if l.perAppDeployment[key] <= 0 {
				delete(l.perAppDeployment, key)
			}
		}
	}
}

// Release frees an instance's entire reservation when it parks/stops (§6.2-4).
// Unknown instances are ignored. The reservation remembers its nodeID, so
// the per-node resident counter is decremented without a second lookup
// against the store.
// ResidentFor returns true if the ledger holds a slot for the
// given instance (Task #62 source-ledger release backstop).
// Read-only — does not mutate state. Used by the recovery
// reconciler (Engine.ReconcileDeadNodeInstances) after a
// conditional UPDATE returns ErrConflict: when the peer has
// already moved the row but the ledger slot wasn't freed by
// the peer's path (the single-point-of-failure race the
// gateway-listener used to leave open), this returns true so
// the caller can call Release as a backstop.
//
// Safe on a nil receiver — returns false (no slot held).
func (l *NodeLedger) ResidentFor(instance string) bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_, ok := l.entries[instance]
	return ok
}

func (l *NodeLedger) Release(instance string) {
	if l == nil {
		return
	}
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
		if e.deploymentID != "" {
			key := e.appID + "\x00" + e.deploymentID
			l.perAppDeployment[key]--
			if l.perAppDeployment[key] <= 0 {
				delete(l.perAppDeployment, key)
			}
		}
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

// UsedVCPUForNode returns the Σ(vcpu) on a single node. The
// per-node budget check inside Admit uses an internal lookup;
// this is the public read used by the placement chooser
// (Engine.choosePlacementLocked) to thread the per-node vCPU
// used state into the request. Tier A2.
func (l *NodeLedger) UsedVCPUForNode(nodeID string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	if r, ok := l.resident[nodeID]; ok {
		return r.usedVCPU
	}
	return 0
}

// HeadroomMB returns the global MB remaining across every active
// node. The per-node ceiling is enforced inside Admit; this is the
// reaper's view of the fleet-wide headroom.
func (l *NodeLedger) HeadroomMB() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	// Global headroom is sum(per-node ceiling - resident) across nodes;
	// collapsing to api.RAMAdmissionCeilingMB for the legacy empty-node
	// case keeps backwards compatibility for tests that pre-date PR #113.
	if len(l.resident) == 0 {
		return api.RAMAdmissionCeilingMB
	}
	var head int
	for nodeID, r := range l.resident {
		ceiling := r.ceilingMB
		if ceiling <= 0 {
			ceiling = l.ceilingForNode_locked(nodeID, api.Limits{})
		}
		head += ceiling - r.residentRAM
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

// ConcurrencyForDeployment returns the per-(app, deployment) live
// instance count that the ledger tracks (ADR-072, issue #557 closure).
// The floor trigger reads this for per-deployment floor arithmetic;
// the reaper reads the same path. Returns 0 when no instance of the
// (app, deployment) pair is currently admitted (or the deployment
// id was empty on the Admit request — the legacy test seam).
//
// Per-deployment counter keys are `appID + "\x00" + deploymentID`.
// The "\x00" separator is invalid in both Postgres UUIDs and our
// internal id namespace, so a key collision is impossible.
func (l *NodeLedger) ConcurrencyForDeployment(appID, deploymentID string) int {
	if appID == "" || deploymentID == "" {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.perAppDeployment[appID+"\x00"+deploymentID]
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
