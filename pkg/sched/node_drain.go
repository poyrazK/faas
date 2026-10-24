package sched

// adr: 20261024085817

import (
	"context"

	"github.com/onebox-faas/faas/pkg/state"
)

// parkReasonNodeDraining labels a frozen or warm VM parked, or an idle
// instance parked instead of frozen, because its node no longer admits work.
const parkReasonNodeDraining = "node_draining"

// ParkForDrain parks an instance the recovery arbiter could not hand off
// while its node drains (ADR-20261024085817): the ordinary park on its own
// node, a capture then a destroy for RUNNING and a destroy for WARM, so the
// next request wakes it on an admitting node. A row that is no longer
// parkable (a handoff or another schedd moved it) is left alone.
func (e *Engine) ParkForDrain(ctx context.Context, instanceID string) error {
	return e.ParkWithReason(ctx, instanceID, "node_drain")
}

// ObserveNodeLifecycles records which nodes no longer admit work. The
// recovery runner calls it with every node list it reads, about once a
// second, so the view lags a lifecycle change by about one recovery tick.
func (e *Engine) ObserveNodeLifecycles(nodes []state.ComputeNode) {
	if e == nil {
		return
	}
	leaving := make(map[string]struct{})
	for _, node := range nodes {
		if nodeLeaving(node) {
			leaving[node.ID] = struct{}{}
		}
	}
	e.leavingNodes.Store(&leaving)
}

// nodeLeavingByID reports whether the last observed node list showed
// nodeID no longer admitting work. A node the view has not seen admits.
func (e *Engine) nodeLeavingByID(nodeID string) bool {
	if e == nil || nodeID == "" {
		return false
	}
	leaving := e.leavingNodes.Load()
	if leaving == nil {
		return false
	}
	_, ok := (*leaving)[nodeID]
	return ok
}

// nodeLeaving is placement's admission predicate turned around: a node
// with a lifecycle that does not admit (draining, force_draining,
// maintenance, retired, unavailable) is leaving. A row without a
// lifecycle predates it and admits, as placement treats it.
func nodeLeaving(node state.ComputeNode) bool {
	return node.Lifecycle != "" && !node.Lifecycle.IsAdmitting()
}
