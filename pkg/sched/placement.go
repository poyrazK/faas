// placement.go — schedd's compute-node placement chooser (issue #97 / ADR-025
// axis 3).
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
//     (lexicographic name). No distributed state, no leader election, no
//     eventual consistency — single schedd process owns placement.
//   - The single-box path (one 'default-local' row with the legacy
//     47,600 MB ceiling) degenerates to "always default-local" without a
//     special case: ChoosePlacement with one active node returns that node.
//   - Testable without Postgres or KVM: the test table is a literal slice
//     of ComputeNode + a map of used_mb, exactly what ComputeNodeUsedMB
//     returns from PG/MemStore.

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
	// CeilingMB is the per-node RAM admission ceiling
	// (compute_node.admission_ceiling_mb). The chooser already verified
	// the request fits; downstream code reads this to log context.
	CeilingMB int
	// UsedMB is the live Σ(ram_mb + PerVMOverheadMB) on the chosen node
	// AT THE TIME OF THE CHOICE, BEFORE this request is added. It is
	// informational — the engine's per-node ledger keeps the canonical
	// post-admit count. Tests use it to assert the tie-break.
	UsedMB int64
}

// ChoosePlacement returns the node with the most free RAM headroom that
// still fits the request, or a *api.Problem if no node can. Tie-break:
// lexicographic name (deterministic, test-friendly). Pure function: no
// Engine/Ledger coupling, no DB access.
//
// Inputs:
//
//   - nodes: every active compute_node from ActiveComputeNodes. Inactive
//     rows are filtered here (placement skips drained nodes; an operator
//     flips Active=false to drain without deleting the row).
//   - usedMB: live Σ(ram_mb + PerVMOverheadMB) per node ID, from
//     ComputeNodeUsedMB. The map may be sparse (a node with no
//     instances is just absent); missing keys are treated as 0.
//
// The request's billable RAM is api.BillableRAMMB(r.RAMMB) — the +8 MB
// overhead (spec §4.7) is part of the per-node headroom check, mirroring
// the per-instance accounting the ledger enforces.
func ChoosePlacement(nodes []state.ComputeNode, usedMB map[string]int64, r Request) (Placement, error) {
	if r.RAMMB <= 0 {
		return Placement{}, api.ErrCapacity(fmt.Sprintf("placement: request RAM must be positive (got %d)", r.RAMMB))
	}
	billable := int64(api.BillableRAMMB(r.RAMMB))

	var (
		chosen     Placement
		chosenFree int64 = -1 // -1 sentinel so a zero-headroom node still wins when nothing else fits
	)
	for _, n := range nodes {
		if !n.Active {
			continue
		}
		if n.AdmissionCeilingMB <= 0 {
			continue
		}
		used := usedMB[n.ID]
		if used+billable > int64(n.AdmissionCeilingMB) {
			continue // this node can't fit the request
		}
		free := int64(n.AdmissionCeilingMB) - used
		// Tie-break: lexicographic name on the node (NOT the id) so
		// "node-A" beats "node-B" deterministically and tests don't have
		// to pin UUIDs. Two candidates with equal free headroom:
		//   - first by free headroom descending (more slack wins),
		//   - then by name ascending (deterministic).
		if free > chosenFree {
			chosen = Placement{
				NodeID:    n.ID,
				Name:      n.Name,
				TargetURL: n.TargetURL,
				CeilingMB: n.AdmissionCeilingMB,
				UsedMB:    used,
			}
			chosenFree = free
			continue
		}
		if free == chosenFree && chosen.NodeID != "" && n.Name < chosen.Name {
			chosen = Placement{
				NodeID:    n.ID,
				Name:      n.Name,
				TargetURL: n.TargetURL,
				CeilingMB: n.AdmissionCeilingMB,
				UsedMB:    used,
			}
		}
	}
	if chosen.NodeID == "" {
		return Placement{}, api.ErrCapacity(fmt.Sprintf(
			"placement: no active compute_node fits %d MB billable across %d candidates (per-node ceilings: see compute_nodes.admission_ceiling_mb)",
			billable, len(nodes)))
	}
	return chosen, nil
}
