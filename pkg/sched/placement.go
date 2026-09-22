// placement.go — schedd's compute-node placement chooser (issue #97 / ADR-025
// axis 3, scale-out worktree).
//
// schedd is the single leader of placement (ADR-025: single-leader CP, no
// consensus). ChoosePlacement is the pure-function core: given the live fleet
// (every active compute_node row from pkg/state) plus a snapshot of how much
// RAM each node is currently holding (from ComputeNodeUsedMB), pick the node
// that should host the next wake. The Engine wraps this with a thin layer
// that fetches the live data; the chooser itself is unit-testable in
// isolation (placement_test.go).
//
// Why a pure function:
//   - The decision is O(N) over the active set with a deterministic tie-break
//     (lexicographic name, secondary on Region/Zone from migration 00069).
//     No distributed state, no leader election, no eventual consistency —
//     single schedd process owns placement.
//   - The single-box path (one 'default-local' row with the legacy
//     47,600 MB ceiling) degenerates to "always default-local" without a
//     special case: ChoosePlacement with one active node returns that node.
//   - Testable without Postgres or KVM: the test table is a literal slice
//     of ComputeNode + a map of used_mb, exactly what ComputeNodeUsedMB
//     returns from PG/MemStore.
//
// Affinity (ADR-025, sticky-warm):
//   - r.PreferredNodeID is a hint, not a gate. If the preferred node still
//     has headroom, the chooser returns it (warm snapshot + page cache
//     benefit per ADR-009). If not, falls through to least-loaded.
//   - Hint source is pkg/sched.WarmAffinity (in-memory TTL cache); the
//     engine reads LastWarmNode(AppID) before calling ChoosePlacement.
//   - Cold-boot path (ADR-005) is preserved: an empty hint falls through
//     to the same least-loaded path a fresh install would take.

package sched

import (
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Placement is the chosen compute node for one admit. Carries the dial
// target so the wake loop doesn't need a second lookup against the
// compute_nodes table.
type Placement struct {
	NodeID    string
	Name      string
	TargetURL string // wire.ParseTarget-compatible (unix://|tcp://|dns://)
	// Region is copied from compute_nodes at placement time so the wake
	// identity can describe the locality that actually hosted the VM.
	Region string
	// CeilingMB is the per-node RAM admission ceiling
	// (compute_node.admission_ceiling_mb). The chooser already verified
	// the request fits; downstream code reads this to log context.
	CeilingMB int
	// VCPUBudget is the per-node vCPU admission budget (Tier A2,
	// compute_node.vcpu_budget). The chooser already verified the
	// request fits; downstream code (admission) reads this to log
	// context and to thread the budget into the per-node ledger check.
	VCPUBudget int
	// CPUBudgetMillicores is the physical host CPU capacity used by the
	// sustained-quota admission guard. Unlike VCPUBudget, this is not guest
	// topology: it is compute_nodes.vpcpus * 1000.
	CPUBudgetMillicores int
	// UsedMB is the live Σ(ram_mb + PerVMOverheadMB) on the chosen node
	// AT THE TIME OF THE CHOICE, BEFORE this request is added. It is
	// informational — the engine's per-node ledger keeps the canonical
	// post-admit count. Tests use it to assert the tie-break.
	UsedMB int64
}

// ChoosePlacement returns a node that fits all configured resource guards, or
// a *api.Problem if none can. Legacy requests without CPUMillicores preserve
// the old order. CPU-aware production requests use:
// (physical_cpu_headroom DESC, ram_headroom DESC, guest_vcpu_headroom DESC,
// region ASC, zone ASC, name ASC).
// Pure function: no Engine/Ledger coupling, no DB access.
//
// Inputs:
//
//   - nodes: every active compute_node from ActiveComputeNodes. Inactive
//     rows are filtered here (placement skips drained nodes; an operator
//     flips Active=false to drain without deleting the row).
//   - usedMB: live Σ(ram_mb + PerVMOverheadMB) per node ID, from
//     ComputeNodeUsedMB. The map may be sparse (a node with no
//     instances is just absent); missing keys are treated as 0.
//   - usedVCPU (Tier A2): ledger-observed Σ(vcpu) per node ID, passed
//     by the engine from NodeLedger.usedVCPUForNode. A nil or
//     sparse map treats missing keys as 0. Pre-Tier-A2 callers pass
//     an empty map; the chooser then never refuses on vCPU headroom
//     (the request's vCPU still has to fit, but a node with a
//     non-zero budget is always accepted).
//
// The request's billable RAM is api.BillableRAMMBWithSidecars(r.RAMMB,
// r.SidecarMBs) — the +8 MB overhead (spec §4.7) is part of the
// per-node headroom check, mirroring the per-instance accounting the
// ledger enforces. Sidecar MBs add to the billable shutter (issue #463
// / ADR-070 §Decision 6); a no-sidecar request collapses to the
// legacy single-arg form (r.SidecarMBs nil/empty).
//
// Sticky-warm affinity (r.PreferredNodeID): legacy requests return a fitting
// preferred node directly. CPU-aware requests retain affinity only when that
// node is tied for the most physical CPU headroom, preventing locality from
// packing one host while an idle peer exists. Affinity remains a bias, never
// a capacity gate (ADR-005).
func ChoosePlacement(nodes []state.ComputeNode, usedMB map[string]int64, usedVCPU map[string]int64, r Request) (Placement, error) {
	return choosePlacementWithCPU(nodes, usedMB, usedVCPU, nil, r)
}

// choosePlacementWithCPU is the production chooser. The exported wrapper
// keeps the pre-millicore pure-function seam stable for older callers and
// tests; Engine supplies the ledger's per-node sustained CPU reservations.
func choosePlacementWithCPU(nodes []state.ComputeNode, usedMB map[string]int64, usedVCPU, usedCPUMillicores map[string]int64, r Request) (Placement, error) {
	if r.RAMMB <= 0 {
		return Placement{}, api.ErrCapacity(fmt.Sprintf("placement: request RAM must be positive (got %d)", r.RAMMB))
	}
	billable := int64(api.BillableRAMMBWithSidecars(r.RAMMB, r.SidecarMBs))

	// First pass: filter to candidates that fit, capture the warm
	// hint if it fits. We keep this single-pass because N is small
	// (single-digit fleet for v1.0, see cmd/apid/compute_nodes.go
	// comments) and a separate filter pass would duplicate work.
	//
	// Tier A2: vCPU is a per-node budget parallel to RAM. The
	// chooser must refuse to land a request into a node whose
	// vcpu_budget - usedVCPU can't fit r.VCPU. The usedVCPU per
	// node is read from the ledger via the engine — placement.go
	// itself is a pure function over the nodes slice + a
	// per-node usedMB map, so the vCPU headroom is threaded
	// through a parallel per-node usedVCPU map passed by the
	// engine. (A zero-budget node with r.VCPU > 0 is excluded.)
	var (
		candidates []state.ComputeNode
		warmFit    *state.ComputeNode
		snapshotF  []state.ComputeNode
	)
	for i := range nodes {
		n := nodes[i]
		// Workstream B (issue #1184 / ADR-137): placement
		// rejects nodes whose lifecycle is not in
		// {active, recovering}. The legacy bool `active` is a
		// STORED GENERATED column derived from `lifecycle` in
		// Postgres (migration 00579) and a parallel-update in
		// the in-memory store (pkg/state/memstore.go
		// NodeSetLifecycle). The two checks below are belt-
		// and-suspenders: the explicit lifecycle assertion is
		// the source of truth for rows that explicitly set
		// lifecycle; legacy callers that haven't migrated yet
		// (Lifecycle=="") fall through to the bool check
		// rather than getting silently rejected.
		if n.Lifecycle != "" && !n.Lifecycle.IsAdmitting() {
			continue
		}
		if !n.Active {
			continue
		}
		if n.AdmissionCeilingMB <= 0 {
			continue
		}
		// Per-node vCPU budget gate (Tier A2). A node with
		// vcpu_budget=0 is treated as "no budget" and skipped
		// — a freshly-migrated row that didn't get backfilled
		// (shouldn't happen — DEFAULT 160 — but a defensive
		// zero-check) or a node that an operator drained.
		if n.VCPUBudget <= 0 {
			continue
		}
		// usedVCPU is keyed by node id; absent means 0. The
		// engine passes the ledger's per-node view (reservations
		// made so far this process). A request of r.VCPU=0
		// (the build-VM path today? no — build VMs are not
		// routed through ChoosePlacement) is always accepted
		// on vCPU headroom, paralleling how r.RAMMB<=0 would
		// have been rejected by the precondition above.
		if r.VCPU > 0 {
			used := usedVCPU[n.ID]
			if used+int64(r.VCPU) > int64(n.VCPUBudget) {
				continue // this node can't fit the vCPU
			}
		}
		// Physical CPU admission uses the cgroup quota, not guest-visible
		// topology. A node advertising four host vCPUs has 4000
		// millicores of sustained app capacity. Missing/zero VPCPUs is
		// rejected when the request opts into this guard; legacy requests
		// with CPUMillicores=0 retain the previous behavior.
		if r.CPUMillicores > 0 {
			budget := cpuBudgetMillicores(n)
			if budget <= 0 || usedCPUMillicores[n.ID]+int64(r.CPUMillicores) > budget {
				continue
			}
		}
		used := usedMB[n.ID]
		if used+billable > int64(n.AdmissionCeilingMB) {
			continue // this node can't fit the request
		}
		candidates = append(candidates, n)
		if r.PreferredNodeID != "" && n.ID == r.PreferredNodeID {
			// Capture by value (small struct) so the warmFit
			// pointer doesn't alias a loop variable that the
			// sort below mutates.
			nCopy := n
			warmFit = &nCopy
		}
		if containsNodeID(r.PreferredNodeIDs, n.ID) {
			snapshotF = append(snapshotF, n)
		}
	}

	// CPU headroom outranks sticky locality. Preserve the warm fast path
	// only while the preferred node is tied for the most physical CPU
	// headroom; otherwise affinity would pack a single host until it was
	// saturated while an idle peer existed.
	if warmFit != nil && (r.CPUMillicores <= 0 ||
		cpuHeadroomMillicores(*warmFit, usedCPUMillicores[warmFit.ID]) >= maxCPUHeadroomMillicores(candidates, usedCPUMillicores)) {
		return Placement{
			NodeID:              warmFit.ID,
			Name:                warmFit.Name,
			TargetURL:           warmFit.TargetURL,
			Region:              stringValue(warmFit.Region),
			CeilingMB:           warmFit.AdmissionCeilingMB,
			VCPUBudget:          warmFit.VCPUBudget,
			CPUBudgetMillicores: int(cpuBudgetMillicores(*warmFit)),
			UsedMB:              usedMB[warmFit.ID],
		}, nil
	}

	// A ready local snapshot is a stronger locality signal than the
	// least-loaded tie-break, but remains a hint: capacity was checked above
	// and a saturated replica is simply omitted from snapshotF. Choose the
	// best ready replica deterministically when more than one is available.
	if len(snapshotF) > 0 {
		best := snapshotF[0]
		for _, n := range snapshotF[1:] {
			if betterCandidate(n, usedMB[n.ID], usedVCPU[n.ID], usedCPUMillicores[n.ID], best, usedMB[best.ID], usedVCPU[best.ID], usedCPUMillicores[best.ID], r) {
				best = n
			}
		}
		if r.CPUMillicores <= 0 ||
			cpuHeadroomMillicores(best, usedCPUMillicores[best.ID]) >= maxCPUHeadroomMillicores(candidates, usedCPUMillicores) {
			return placementForNode(best, usedMB[best.ID]), nil
		}
	}

	if len(candidates) == 0 {
		return Placement{}, api.ErrCapacity(fmt.Sprintf(
			"placement: no active compute_node fits %d MB billable (per-node ceilings: see compute_nodes.admission_ceiling_mb) / %d guest vCPU (per-node budgets: see compute_nodes.vcpu_budget) / %d millicores (physical host CPU budget) across %d candidates",
			billable, r.VCPU, r.CPUMillicores, len(nodes)))
	}

	// Single best candidate — short-circuit the sort.
	if len(candidates) == 1 {
		n := candidates[0]
		return placementForNode(n, usedMB[n.ID]), nil
	}

	// Pick by physical CPU headroom first for CPU-aware requests, then RAM,
	// guest-vCPU headroom, and the deterministic locality/name tie-breakers.
	//
	// Region/Zone are *string; treat nil and "" identically so a
	// pre-00069 row (nil pointers) sorts the same as an operator-
	// inserted row with empty strings. The seeded default-local row
	// is backfilled to ('local','local') in migration 00069 so the
	// single-box deploy has a deterministic ordering.
	//
	// Tier A2: vcpu_headroom DESC is the secondary tie-break. A
	// fleet with one RAM-rich + vCPU-poor box and one vCPU-rich +
	// RAM-poor box now biases toward the vCPU-richer node when
	// RAM headroom is tied (the typical "burst" case where every
	// node is well under its RAM ceiling).
	best := candidates[0]
	for _, n := range candidates[1:] {
		if betterCandidate(n, usedMB[n.ID], usedVCPU[n.ID], usedCPUMillicores[n.ID], best, usedMB[best.ID], usedVCPU[best.ID], usedCPUMillicores[best.ID], r) {
			best = n
		}
	}
	return placementForNode(best, usedMB[best.ID]), nil
}

func placementForNode(n state.ComputeNode, usedMB int64) Placement {
	return Placement{
		NodeID:              n.ID,
		Name:                n.Name,
		TargetURL:           n.TargetURL,
		Region:              stringValue(n.Region),
		CeilingMB:           n.AdmissionCeilingMB,
		VCPUBudget:          n.VCPUBudget,
		CPUBudgetMillicores: int(cpuBudgetMillicores(n)),
		UsedMB:              usedMB,
	}
}

// cpuBudgetMillicores is the sustained CPU a node may admit, in millicores.
//
// An app's CPUMillicores is a cgroup v2 cpu.max *quota* — a ceiling on burst,
// not a reservation. Summing those ceilings against raw physical cores
// therefore reserves peak capacity for every idle instance, which is the
// opposite of what a scale-to-zero platform needs and the opposite of what
// spec §1 says: CPUOvercommit is 8, and it is the same factor the
// guest-vCPU gate already applies through VCPUBudget
// (api.DeriveNodeSizing sets VCPUSlots = hostCPUs * CPUOvercommit).
//
// Without the factor the two gates in ChoosePlacement disagreed about the
// same resource and the stricter one silently won: a 4-core node advertising
// vcpu_budget=32 would admit exactly 4 apps at the default 1000 millicores,
// making the overcommit-aware budget dead code. On the production fleet that
// left every node unable to satisfy its own pinned min_instances floors.
//
// Oversubscribing CPU degrades latency under simultaneous load; the kernel
// throttles each instance to its own cpu.max. That is a different and far
// more recoverable failure than oversubscribing RAM, which OOM-kills. The
// RAM ceiling stays un-overcommitted for exactly that reason.
func cpuBudgetMillicores(n state.ComputeNode) int64 {
	if n.VPCPUs <= 0 {
		return 0
	}
	return int64(n.VPCPUs) * 1000 * int64(api.CPUOvercommit)
}

func cpuHeadroomMillicores(n state.ComputeNode, used int64) int64 {
	return cpuBudgetMillicores(n) - used
}

func maxCPUHeadroomMillicores(nodes []state.ComputeNode, used map[string]int64) int64 {
	var max int64
	for i, n := range nodes {
		headroom := cpuHeadroomMillicores(n, used[n.ID])
		if i == 0 || headroom > max {
			max = headroom
		}
	}
	return max
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func containsNodeID(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

// betterCandidate returns true if `n` should replace `best` per the
// tie-break ordering in ChoosePlacement. Pure helper so placement_test.go
// can exercise the comparator without spinning up Engine + Ledger.
//
// The ordering is the load-bearing contract — placement_test.go pins it.
// Changing this function changes where hot apps land; never edit without
// reading the test cases.
//
// For a CPU-aware request, physical millicore headroom is primary. RAM and
// guest-vCPU preserve their prior order below it. A legacy request with
// CPUMillicores=0 skips the new comparison bit-for-bit.
func betterCandidate(n state.ComputeNode, nUsed, nVCPUUsed, nCPUUsed int64, best state.ComputeNode, bestUsed, bestVCPUUsed, bestCPUUsed int64, r Request) bool {
	// Sustained host CPU headroom is the first placement signal once a
	// request carries its real cgroup quota. This spreads work before RAM
	// and guest-topology tie-breakers can pack one node.
	if r.CPUMillicores > 0 {
		nCPUHead := cpuHeadroomMillicores(n, nCPUUsed)
		bestCPUHead := cpuHeadroomMillicores(best, bestCPUUsed)
		if nCPUHead != bestCPUHead {
			return nCPUHead > bestCPUHead
		}
	}
	nHead := int64(n.AdmissionCeilingMB) - nUsed
	bestHead := int64(best.AdmissionCeilingMB) - bestUsed
	if nHead != bestHead {
		return nHead > bestHead
	}
	// Tier A2 secondary: vCPU headroom. Pure function — no
	// ledger access here; the per-node vCPU accounting is
	// threaded in via the caller (Engine.choosePlacementLocked
	// reads ledger.usedVCPUForNode and stamps Request.usedVCPU).
	nVHead := int64(n.VCPUBudget) - nVCPUUsed
	bestVHead := int64(best.VCPUBudget) - bestVCPUUsed
	if nVHead != bestVHead {
		return nVHead > bestVHead
	}
	// ADR-098 PR-D: connection-aware upstream fit. When the
	// caller passed a PreferredRegion (the upstream-affinity
	// cache hit on the wake path), prefer the candidate whose
	// compute_node.region matches. An empty PreferredRegion
	// (cache cold / FAAS_UPSTREAM_AFFINITY=0) skips this branch
	// and falls through to the legacy region-string ASC
	// tie-break — the bias is opt-in by design. Region
	// comparison uses the same nil → "" collapse as the
	// legacy comparator below; the upstream_fit INSERT is
	// load-bearing (placement_test.go pins the ordering).
	nRegion := derefRegion(n.Region)
	bestRegion := derefRegion(best.Region)
	if r.PreferredRegion != "" {
		nMatch := nRegion == r.PreferredRegion
		bestMatch := bestRegion == r.PreferredRegion
		if nMatch != bestMatch {
			return nMatch
		}
	}
	// Region/Zone are nullable strings; collapse nil → "" so the
	// comparator sees a single shape. Tied on headroom → prefer
	// lower region, then lower zone, then lower name. The seeded
	// default-local row is backfilled to ('local','local') in
	// migration 00069, so single-box deploys see a deterministic
	// ordering with no operator-added rows competing.
	if nRegion != bestRegion {
		return nRegion < bestRegion
	}
	nZone := derefRegion(n.Zone)
	bestZone := derefRegion(best.Zone)
	if nZone != bestZone {
		return nZone < bestZone
	}
	return n.Name < best.Name
}

func derefRegion(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
