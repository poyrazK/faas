package sched

import (
	"context"

	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
)

// CPU-compatible snapshot restore (ADR-20261024225612).
//
// A snapshot keeps the CPUID its guest booted with, and guests pick code
// paths from it, so a snapshot may only restore on a CPU that has every
// feature it recorded. The capturing node's identity rides on the snapshot
// row: a templated capture restores on any node class the template
// supports, a template-less one only on the identical host signature, and a
// legacy row (no identity) only on template-less nodes while those all
// report one CPU model.

// ColdReasonCPUIncompatible: no active node can restore the snapshot's CPU
// state, or every node that can is full.
const ColdReasonCPUIncompatible = "cpu_incompatible"

// nodeCPUIdentity returns the CPU template and host signature nodeID
// registered. An unknown node reports the empty pair, which marks the
// capture as a legacy row.
func (e *Engine) nodeCPUIdentity(ctx context.Context, nodeID string) (template, model string) {
	if nodeID == "" {
		return "", ""
	}
	if e.nodeRegistry != nil {
		for _, n := range e.nodeRegistry.Snapshot() {
			if n.ID == nodeID {
				return n.CPUTemplate, n.CPUModel
			}
		}
	}
	n, err := e.store.ComputeNodeByID(ctx, nodeID)
	if err != nil {
		return "", ""
	}
	return n.CPUTemplate, n.CPUModel
}

// activeNodesForCPU is the node list placement would choose from. A failed
// read returns nil: callers then leave the decision to placement.
func (e *Engine) activeNodesForCPU(ctx context.Context) []state.ComputeNode {
	if e.nodeRegistry != nil {
		return e.nodeRegistry.Snapshot()
	}
	nodes, err := e.store.ActiveComputeNodes(ctx)
	if err != nil {
		return nil
	}
	return nodes
}

// legacySnapshotsRestorable reports whether rows captured before nodes
// recorded their CPU may still restore: only while every template-less node
// that reports a signature reports the same one. Nodes that report none (a
// vmmd from before the ADR) do not count against it.
func legacySnapshotsRestorable(nodes []state.ComputeNode) bool {
	model := ""
	for _, n := range nodes {
		if n.CPUTemplate != "" || n.CPUModel == "" {
			continue
		}
		if model == "" {
			model = n.CPUModel
		} else if n.CPUModel != model {
			return false
		}
	}
	return true
}

// snapshotRestorableOn reports whether snap may restore on node. legacyOK is
// legacySnapshotsRestorable over the active fleet.
func snapshotRestorableOn(snap state.Snapshot, node state.ComputeNode, legacyOK bool) bool {
	if snap.CPUTemplate == "" && snap.CPUModel == "" {
		return legacyOK && node.CPUTemplate == ""
	}
	return fcvm.SnapshotRestoreCompatible(snap.CPUTemplate, snap.CPUModel, node.CPUModel)
}

// restorableNodes splits nodes into those snap may restore on and a count of
// the rest.
func restorableNodes(snap state.Snapshot, nodes []state.ComputeNode) (ok []state.ComputeNode, excluded int) {
	legacyOK := legacySnapshotsRestorable(nodes)
	ok = make([]state.ComputeNode, 0, len(nodes))
	for _, n := range nodes {
		if snapshotRestorableOn(snap, n, legacyOK) {
			ok = append(ok, n)
		} else {
			excluded++
		}
	}
	return ok, excluded
}

// snapshotRestorableSomewhere is the wake's pre-placement check: false only
// when the fleet is known and no active node can restore snap. An empty or
// unreadable fleet leaves the decision to placement, as before the ADR.
func (e *Engine) snapshotRestorableSomewhere(ctx context.Context, snap state.Snapshot) bool {
	nodes := e.activeNodesForCPU(ctx)
	if len(nodes) == 0 {
		return true
	}
	ok, _ := restorableNodes(snap, nodes)
	return len(ok) > 0
}

// snapshotExcludesNodes reports whether some active node cannot restore snap,
// so a restore that found no room may still cold-boot on one of them.
func (e *Engine) snapshotExcludesNodes(ctx context.Context, snap state.Snapshot) bool {
	_, excluded := restorableNodes(snap, e.activeNodesForCPU(ctx))
	return excluded > 0
}

// restorableFor narrows a restore placement to the nodes r.restoreSnapshot
// may restore on. The legacy rule is judged over the whole list, before the
// narrowing.
func (s placementSnapshot) restorableFor(r Request) placementSnapshot {
	if r.restoreSnapshot == nil {
		return s
	}
	snap := *r.restoreSnapshot
	legacyOK := legacySnapshotsRestorable(s.nodes)
	keep := func(nodes []state.ComputeNode) []state.ComputeNode {
		if nodes == nil {
			return nil
		}
		out := make([]state.ComputeNode, 0, len(nodes))
		for _, n := range nodes {
			if snapshotRestorableOn(snap, n, legacyOK) {
				out = append(out, n)
			}
		}
		return out
	}
	s.nodes = keep(s.nodes)
	s.preferred = keep(s.preferred)
	return s
}

// CPUIdentityWarning describes a fleet whose nodes cannot share snapshots:
// different templates, or template-less nodes on different CPUs. Empty when
// every active node can restore every other node's captures.
func CPUIdentityWarning(nodes []state.ComputeNode) (distinct int, warning string) {
	templates := map[string]bool{}
	bare := map[string]bool{}
	identities := map[string]bool{}
	for _, n := range nodes {
		if n.CPUTemplate == "" && n.CPUModel == "" {
			continue
		}
		templates[n.CPUTemplate] = true
		identities[n.CPUTemplate+"|"+n.CPUModel] = true
		if n.CPUTemplate == "" {
			bare[n.CPUModel] = true
		}
	}
	distinct = len(identities)
	switch {
	case len(templates) > 1:
		warning = "active compute nodes boot guests with different CPU templates; snapshots restore only on nodes their capture is compatible with"
	case len(bare) > 1:
		warning = "active compute nodes without a CPU template run different CPUs; their snapshots restore only on identical hosts (set cpu_template, ADR-20261024225612)"
	}
	return distinct, warning
}
