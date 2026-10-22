// adr: 20261022184531 — a compute node's readiness is proven state.
package sched

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// production-us 2026-10-22: fsn-3 answered every heartbeat while its vmmd
// could not adopt its configured base generation and refused every wake.
// Liveness alone kept it in placement. The heartbeat now also records what
// the node has proven: a reachable node is ready only while its vmmd can
// adopt each configured base generation, and placement prefers ready nodes.

// BaseGenerationReadiness is vmmd's report on one configured runtime base.
type BaseGenerationReadiness struct {
	BaseKey    string
	Generation string
	Adoptable  bool
	Reason     string
}

// evaluateNodeReadiness decides a probed node's readiness. ping is the
// successful probe's outcome; nil means the node did not answer. Lifecycle
// wins over everything this schedd proved.
func evaluateNodeReadiness(n state.ComputeNode, ping *PingOutcome) (state.NodeReadiness, string) {
	lifecycleReadiness := n.EffectiveReadiness()
	switch {
	case lifecycleReadiness == state.NodeReadinessDraining:
		return state.NodeReadinessDraining, ""
	case ping == nil:
		return state.NodeReadinessUnavailable, "vmmd does not answer its heartbeat"
	case lifecycleReadiness == state.NodeReadinessUnavailable:
		return state.NodeReadinessUnavailable, ""
	}
	var refused []string
	for _, base := range ping.BaseGenerations {
		if !base.Adoptable {
			refused = append(refused, fmt.Sprintf("cannot adopt base generation %s of %s: %s", base.Generation, base.BaseKey, base.Reason))
		}
	}
	if len(refused) > 0 {
		slices.Sort(refused)
		return state.NodeReadinessDegraded, strings.Join(refused, "; ")
	}
	return state.NodeReadinessReady, ""
}

// recordReadiness exports n's readiness and stores it when it changed. The
// store write fires compute_node_changed, which refreshes every schedd's
// node registry and therefore placement.
func (h *Heartbeat) recordReadiness(ctx context.Context, n state.ComputeNode, readiness state.NodeReadiness, reason string) {
	h.ops.SetComputeNodeReadiness(n.Name, string(readiness))
	writer, ok := h.store.(state.ComputeNodeReadinessStore)
	if !ok {
		return
	}
	changed, err := writer.SetComputeNodeReadiness(ctx, n.ID, readiness, reason)
	if err != nil {
		h.log.Warn("heartbeat: record node readiness failed", "node_id", n.ID, "node_name", n.Name,
			"readiness", readiness, "err", err)
		return
	}
	if !changed {
		return
	}
	logReadiness := h.log.Info
	if readiness == state.NodeReadinessDegraded {
		logReadiness = h.log.Warn
	}
	logReadiness("heartbeat: node readiness changed", "node_id", n.ID, "node_name", n.Name,
		"from", n.EffectiveReadiness(), "to", readiness, "reason", reason)
}

// WithOpsMetrics exports schedd_compute_node_readiness. Nil opts out.
func (h *Heartbeat) WithOpsMetrics(ops *wire.OpsMetrics) *Heartbeat {
	if h == nil {
		return h
	}
	h.ops = ops
	return h
}
