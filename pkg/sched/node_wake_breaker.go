// adr: 20261022184531 — a per-node wake breaker (step 3).
package sched

import (
	"context"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// A node can answer its heartbeat and still fail every wake: production-us
// 2026-10-22, fsn-3. Step 1 re-places each refused wake, which costs a vmmd
// round trip per wake. The breaker stops paying it: after
// api.NodeWakeBreakerFailures consecutive platform-attributable failures on a
// node within api.NodeWakeBreakerWindowSeconds, this schedd opens the node's
// durable breaker (compute_nodes.wake_breaker_open_until), and every schedd
// treats the node as degraded until it passes. Then the breaker is half-open:
// the next platform failure there reopens it at once, and the next successful
// wake there closes it.
type nodeWakeBreaker struct {
	mu sync.Mutex
	// failures holds, per node, the times of the consecutive
	// platform-attributable failures this schedd saw, oldest first.
	failures map[string][]time.Time
	// opened records the nodes whose breaker this schedd opened and has not
	// seen closed, so a success there closes it even without a node registry.
	opened map[string]bool
}

func newNodeWakeBreaker() *nodeWakeBreaker {
	return &nodeWakeBreaker{failures: map[string][]time.Time{}, opened: map[string]bool{}}
}

// fail records one failure at now and reports whether the breaker should
// open: the count within the window reached the threshold, or the node is
// half-open, where one failure is enough.
func (b *nodeWakeBreaker) fail(nodeID string, now time.Time, halfOpen bool) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	window := api.NodeWakeBreakerWindowSeconds * time.Second
	recent := b.failures[nodeID][:0]
	for _, at := range b.failures[nodeID] {
		if now.Sub(at) < window {
			recent = append(recent, at)
		}
	}
	recent = append(recent, now)
	if halfOpen || len(recent) >= api.NodeWakeBreakerFailures {
		delete(b.failures, nodeID)
		b.opened[nodeID] = true
		return true
	}
	b.failures[nodeID] = recent
	return false
}

// succeed resets the node's count and reports whether this schedd opened its
// breaker and has not seen it closed.
func (b *nodeWakeBreaker) succeed(nodeID string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.failures, nodeID)
	opened := b.opened[nodeID]
	delete(b.opened, nodeID)
	return opened
}

// breakerNode returns what this schedd knows of the node's breaker without a
// database read when the node registry holds it.
func (e *Engine) breakerNode(ctx context.Context, nodeID string) (state.ComputeNode, bool) {
	if e.nodeRegistry != nil {
		for _, n := range e.nodeRegistry.Snapshot() {
			if n.ID == nodeID {
				return n, true
			}
		}
	}
	n, err := e.store.ComputeNodeByID(ctx, nodeID)
	return n, err == nil
}

// noteNodeWakeFailure counts a platform-attributable failure on nodeID and
// opens the node's breaker when the threshold is reached, or reopens a
// half-open one. An open breaker is left alone: placement already avoids the
// node, and a wake landed there only because no ready node fitted.
func (e *Engine) noteNodeWakeFailure(ctx context.Context, nodeID, reason string) {
	writer, ok := e.store.(state.ComputeNodeWakeBreakerStore)
	if e.breaker == nil || !ok || nodeID == "" {
		return
	}
	now := e.clock()
	node, _ := e.breakerNode(ctx, nodeID)
	if node.WakeBreakerOpen(now) {
		return
	}
	halfOpen := node.WakeBreakerOpenUntil != nil
	if !e.breaker.fail(nodeID, now, halfOpen) {
		return
	}
	until := now.Add(api.NodeWakeBreakerCooldownSeconds * time.Second)
	//nolint:contextcheck // the breaker must land even when the wake's caller gave up.
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), DestroyTimeout)
	defer cancel()
	if err := writer.OpenComputeNodeWakeBreaker(writeCtx, nodeID, until, reason); err != nil {
		e.log.Warn("wake: open node wake breaker failed", "node", nodeID, "reason", reason, "err", err)
		return
	}
	transition := "opened"
	if halfOpen {
		transition = "reopened"
	}
	e.ops.RecordNodeWakeBreaker(transition)
	e.log.Warn("wake: node wake breaker "+transition, "node", nodeID, "node_name", node.Name,
		"reason", reason, "open_until", until)
}

// noteNodeWakeSuccess resets the node's count and closes its breaker when it
// is set (a half-open probe, or a wake placed there because no ready node
// fitted). The registry or this schedd's own record decides, so the
// success path never reads the database.
func (e *Engine) noteNodeWakeSuccess(ctx context.Context, nodeID string) {
	writer, ok := e.store.(state.ComputeNodeWakeBreakerStore)
	if e.breaker == nil || !ok || nodeID == "" {
		return
	}
	openedHere := e.breaker.succeed(nodeID)
	set := openedHere
	if e.nodeRegistry != nil {
		for _, n := range e.nodeRegistry.Snapshot() {
			if n.ID == nodeID && n.WakeBreakerOpenUntil != nil {
				set = true
			}
		}
	}
	if !set {
		return
	}
	//nolint:contextcheck // closing the breaker must land even when the caller gave up.
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), DestroyTimeout)
	defer cancel()
	closed, err := writer.CloseComputeNodeWakeBreaker(writeCtx, nodeID)
	if err != nil {
		e.log.Warn("wake: close node wake breaker failed", "node", nodeID, "err", err)
		return
	}
	if closed {
		e.ops.RecordNodeWakeBreaker("closed")
		e.log.Info("wake: node wake breaker closed", "node", nodeID)
	}
}
