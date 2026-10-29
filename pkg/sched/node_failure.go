// adr: 20261029085904 — what a scheduler does about a node that is gone, or
// up but unable to start VMs.
//
// The heartbeat proves liveness (three failed 30 s probes take a node out)
// and vmmd's base-generation report proves one kind of readiness
// (ADR-20261022184531). Four gaps remained:
//
//   - A reachable vmmd that fails every boot for another reason (disk,
//     jailer, KVM, cgroups) stayed ready. nodeBootBreaker opens after a run
//     of node-caused boot failures from several apps, and placement then
//     treats the node as degraded for a while.
//   - A wake whose RPC never reached vmmd failed instead of trying another
//     node. vmmdNotReached recognises that case; no VM can exist, so wake
//     failover re-places it.
//   - The wake fast path returned a running instance on a node the
//     heartbeat had already taken out of rotation, until the dead-node
//     reconciler failed the row about two minutes later.
//   - vmmd's capacity stream reports every second, yet a vmmd that stopped
//     stayed in placement for up to the heartbeat's 90 s. The scheduler that
//     receives a node's stream now marks it unavailable after
//     api.CapacityStreamLostSeconds of silence and clears its own mark when
//     reports resume.
package sched

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// routerDialError is a VMMRouter failure to dial a node's vmmd: no request
// was sent.
type routerDialError struct{ err error }

func (e routerDialError) Error() string { return e.err.Error() }
func (e routerDialError) Unwrap() error { return e.err }

// vmmdNotReached reports whether err shows a boot request never reached
// vmmd: the router could not dial it, or gRPC refused the call before
// opening a stream ("connection error: …", "name resolver error: …" are
// gRPC's own fail-fast messages). Another node may take such a wake.
func vmmdNotReached(err error) bool {
	if err == nil {
		return false
	}
	var dial routerDialError
	if errors.As(err, &dial) {
		return true
	}
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.Unavailable {
		return false
	}
	msg := st.Message()
	return strings.HasPrefix(msg, "connection error:") || strings.HasPrefix(msg, "name resolver error:")
}

// nodeCausedBootFailure reports whether a boot error points at the node
// rather than the app: vmmd unreachable, or an internal failure before the
// app ran. App problems carry their own codes (startup timeout, scan
// findings, capacity) and do not count.
func nodeCausedBootFailure(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if vmmdNotReached(err) {
		return true
	}
	var problem *api.Problem
	if errors.As(err, &problem) {
		return problem.Code == "internal"
	}
	if st, ok := status.FromError(err); ok {
		switch st.Code() {
		case codes.Internal, codes.Unknown, codes.Unavailable, codes.DataLoss:
			return true
		}
	}
	return false
}

// nodeBootBreaker is one scheduler's view of which nodes keep failing
// boots. It is process-local: each scheduler learns from its own wakes, so
// no scheduler's view overwrites another's.
type nodeBootBreaker struct {
	mu    sync.Mutex
	nodes map[string]*nodeBootHealth
}

type nodeBootHealth struct {
	failures  int
	apps      map[string]struct{}
	openUntil time.Time
}

func newNodeBootBreaker() *nodeBootBreaker {
	return &nodeBootBreaker{nodes: map[string]*nodeBootHealth{}}
}

// failure records a node-caused boot failure and reports whether it opened
// the breaker.
func (b *nodeBootBreaker) failure(nodeID, appID string, now time.Time) bool {
	if b == nil || nodeID == "" {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	h := b.nodes[nodeID]
	if h == nil {
		h = &nodeBootHealth{apps: map[string]struct{}{}}
		b.nodes[nodeID] = h
	}
	h.failures++
	h.apps[appID] = struct{}{}
	if h.failures < api.NodeBootBreakerFailures || len(h.apps) < api.NodeBootBreakerDistinctApps || now.Before(h.openUntil) {
		return false
	}
	h.openUntil = now.Add(api.NodeBootBreakerOpenSeconds * time.Second)
	h.failures, h.apps = 0, map[string]struct{}{}
	return true
}

// success closes the node's breaker: it just booted a VM.
func (b *nodeBootBreaker) success(nodeID string) {
	if b == nil || nodeID == "" {
		return
	}
	b.mu.Lock()
	delete(b.nodes, nodeID)
	b.mu.Unlock()
}

func (b *nodeBootBreaker) open(nodeID string, now time.Time) bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	h := b.nodes[nodeID]
	return h != nil && now.Before(h.openUntil)
}

// noteNodeBoot feeds one wake boot's outcome to the breaker.
func (e *Engine) noteNodeBoot(nodeID, appID string, err error) {
	if err == nil {
		e.nodeBoots.success(nodeID)
		return
	}
	if !nodeCausedBootFailure(err) {
		return
	}
	if e.nodeBoots.failure(nodeID, appID, e.clock()) {
		e.log.Warn("sched: node keeps failing boots; placement treats it as degraded",
			"node_id", nodeID, "open_for_s", api.NodeBootBreakerOpenSeconds, "last_err", err)
	}
}

// markUnprovenNodes returns nodes with the ones this scheduler's breaker has
// open marked degraded, so placement takes them only when nothing else fits.
func (e *Engine) markUnprovenNodes(nodes []state.ComputeNode) []state.ComputeNode {
	now := e.clock()
	var out []state.ComputeNode
	for i, n := range nodes {
		if n.Readiness == state.NodeReadinessDegraded || !e.nodeBoots.open(n.ID, now) {
			continue
		}
		if out == nil {
			out = append([]state.ComputeNode(nil), nodes...)
		}
		out[i].Readiness = state.NodeReadinessDegraded
	}
	if out == nil {
		return nodes
	}
	return out
}

// instanceNodeServes reports whether a running instance's node may still
// serve it. A node the heartbeat took out of rotation (unavailable,
// retired, inactive) cannot: the dead-node reconciler fails its rows later,
// and handing one out meanwhile routes requests to a VM that is gone. A node
// whose row cannot be read keeps serving, as before.
func (e *Engine) instanceNodeServes(ctx context.Context, nodeID string) bool {
	if e.nodeRegistry == nil || nodeID == "" {
		return true
	}
	if _, ok := e.nodeRegistry.Node(nodeID); ok {
		return true
	}
	node, err := e.store.ComputeNodeByID(ctx, nodeID)
	if err != nil {
		return !errors.Is(err, state.ErrNotFound)
	}
	return node.EffectiveReadiness() != state.NodeReadinessUnavailable
}

// capacityStreamLostReason is the readiness reason a stream-loss mark
// carries; only a mark with this reason is cleared when reports resume.
const capacityStreamLostReason = "vmmd stopped reporting capacity"

// capacityStreamWatch remembers which nodes this scheduler marked
// unavailable for a silent capacity stream.
type capacityStreamWatch struct {
	mu     sync.Mutex
	marked map[string]bool
}

func newCapacityStreamWatch() *capacityStreamWatch {
	return &capacityStreamWatch{marked: map[string]bool{}}
}

// ObserveCapacityStreams marks unavailable every node whose capacity stream
// to this scheduler has been silent for api.CapacityStreamLostSeconds,
// unless its vmmd is restarting under a notice, and clears its own mark when
// the node reports again. Only nodes that streamed to this scheduler since it
// started are watched. Each silence is marked once: a heartbeat that proves
// the node ready meanwhile stands.
func (e *Engine) ObserveCapacityStreams(ctx context.Context, now time.Time) {
	writer, ok := e.store.(state.ComputeNodeReadinessStore)
	if !ok || e.capacityTable == nil || e.streams == nil {
		return
	}
	lost := time.Duration(api.CapacityStreamLostSeconds) * time.Second
	for nodeID, lastSeen := range e.capacityTable.LastSeen() {
		silent := now.Sub(lastSeen) > lost
		e.streams.mu.Lock()
		marked := e.streams.marked[nodeID]
		e.streams.mu.Unlock()
		switch {
		case silent && !marked:
			if e.restartNotices.Active(ctx, nodeID) {
				continue
			}
			node, err := e.store.ComputeNodeByID(ctx, nodeID)
			if err != nil || node.EffectiveReadiness() != state.NodeReadinessReady && node.EffectiveReadiness() != state.NodeReadinessDegraded {
				// Gone, draining or already unavailable: nothing to add.
				e.setStreamMark(nodeID, true)
				continue
			}
			if _, err := writer.SetComputeNodeReadiness(ctx, nodeID, state.NodeReadinessUnavailable, capacityStreamLostReason); err != nil {
				e.log.Warn("sched: mark silent node unavailable", "node_id", nodeID, "err", err)
				continue
			}
			e.setStreamMark(nodeID, true)
			e.log.Warn("sched: vmmd stopped reporting capacity; placement avoids the node",
				"node_id", nodeID, "node_name", node.Name, "silent_s", int(now.Sub(lastSeen).Seconds()))
		case !silent && marked:
			e.setStreamMark(nodeID, false)
			node, err := e.store.ComputeNodeByID(ctx, nodeID)
			if err != nil || node.Readiness != state.NodeReadinessUnavailable || node.ReadinessReason == nil || *node.ReadinessReason != capacityStreamLostReason {
				continue
			}
			if _, err := writer.SetComputeNodeReadiness(ctx, nodeID, state.NodeReadinessReady, ""); err != nil {
				e.log.Warn("sched: clear silent-node mark", "node_id", nodeID, "err", err)
				e.setStreamMark(nodeID, true)
				continue
			}
			e.log.Info("sched: vmmd reports capacity again", "node_id", nodeID, "node_name", node.Name)
		}
	}
}

func (e *Engine) setStreamMark(nodeID string, marked bool) {
	e.streams.mu.Lock()
	defer e.streams.mu.Unlock()
	if marked {
		e.streams.marked[nodeID] = true
	} else {
		delete(e.streams.marked, nodeID)
	}
}
