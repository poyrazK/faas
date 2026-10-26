// adr: 20261026170000 — a new app's owner is the admitting node that owns the
// least app RAM for its size.
package sched

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// chooseOwnerByLoad is ClaimUnplaced's owner choice (ADR-20261026170000). It
// reports false when the store cannot report ownership load or no node
// qualifies; ClaimUnplaced then keeps the instance chooser it used before.
func (e *Engine) chooseOwnerByLoad(ctx context.Context, app state.App) (Placement, bool) {
	reader, ok := e.store.(state.AppOwnershipLoadReader)
	if !ok {
		return Placement{}, false
	}
	nodes, err := e.store.ActiveComputeNodes(ctx)
	if err != nil {
		e.log.Warn("sched: claim unplaced: list active compute nodes", "err", err)
		return Placement{}, false
	}
	owned, err := reader.OwnedAppRAMByNode(ctx)
	if err != nil {
		e.log.Warn("sched: claim unplaced: read ownership load", "err", err)
		return Placement{}, false
	}
	node, ok := ChooseOwnerNode(nodes, owned, app.RAMMB)
	if !ok {
		return Placement{}, false
	}
	return placementForNode(node, 0), true
}

// ChooseOwnerNode picks the owner for a new app of ramMB: among the admitting
// nodes whose ceiling can hold one instance, the one whose owned app RAM is
// the smallest share of its admission ceiling, then the lower name. A node
// that proved it cannot serve a wake (readiness degraded) is chosen only when
// no other node qualifies. ownedMB is Σ apps.ram_mb per owner node.
//
// Every schedd computes the same answer from the same rows, so the one that
// wins the claim race stamps the balanced owner rather than whichever node
// happened to answer first.
func ChooseOwnerNode(nodes []state.ComputeNode, ownedMB map[string]int64, ramMB int) (state.ComputeNode, bool) {
	if node, ok := leastOwnedNode(nodes, ownedMB, ramMB, false); ok {
		return node, true
	}
	return leastOwnedNode(nodes, ownedMB, ramMB, true)
}

func leastOwnedNode(nodes []state.ComputeNode, ownedMB map[string]int64, ramMB int, allowDegraded bool) (state.ComputeNode, bool) {
	billable := int64(api.BillableRAMMB(ramMB))
	var best state.ComputeNode
	found := false
	for _, n := range nodes {
		if n.Lifecycle != "" && !n.Lifecycle.IsAdmitting() || !n.Active || int64(n.AdmissionCeilingMB) < billable || billable <= 0 {
			continue
		}
		if n.Readiness == state.NodeReadinessDegraded && !allowDegraded {
			continue
		}
		if !found || ownsLess(n, best, ownedMB) {
			best, found = n, true
		}
	}
	return best, found
}

// ownsLess compares owned RAM as a share of each node's ceiling without
// floating point: a/ca < b/cb  ⇔  a·cb < b·ca.
func ownsLess(n, best state.ComputeNode, ownedMB map[string]int64) bool {
	nShare := ownedMB[n.ID] * int64(best.AdmissionCeilingMB)
	bestShare := ownedMB[best.ID] * int64(n.AdmissionCeilingMB)
	if nShare != bestShare {
		return nShare < bestShare
	}
	return n.Name < best.Name
}
