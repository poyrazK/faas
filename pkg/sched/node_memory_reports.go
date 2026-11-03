package sched

// Node-wide memory shared between schedulers (ADR 20261101091815).
//
// Each scheduler measures memory only on its own node: vmmd streams its
// capacity and instance stats to the local scheduler. A scheduler placing one
// of its apps on a peer node had no measurement there, so it admitted by the
// node's plan-RAM bookings at 100% of the ceiling (placement, the ledger and
// the durable guard alike), and none of its memory-pressure handling saw the
// node. Here each node's own scheduler publishes the node's measured charge,
// vmmd's used memory and its frozen VMs every NodeMemoryReportSeconds, and
// every scheduler reads the fresh reports back.

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// NodeMemorySource is a node's own measurement: its measured charge and frozen
// instances, and when it was taken. ok is false without a fresh one.
type NodeMemorySource func(nodeID string, now time.Time) (measuredMB, frozen int, asOf time.Time, ok bool)

// EnableNodeMemoryReports makes RunNodeMemoryReports publish this scheduler's
// node from local. It has no effect without an owner node.
func (e *Engine) EnableNodeMemoryReports(local NodeMemorySource) {
	if e != nil {
		e.nodeMemoryLocal = local
	}
}

// RunNodeMemoryReports publishes this scheduler's node and refreshes every
// node's report each NodeMemoryReportSeconds until ctx ends. It returns at
// once when the store cannot share reports, the scheduler owns no node or
// reports are not enabled.
func (e *Engine) RunNodeMemoryReports(ctx context.Context) error {
	store, ok := e.store.(state.NodeMemoryReportStore)
	if !ok || e.ownerNodeID == "" || e.nodeMemoryLocal == nil {
		return nil
	}
	tick := time.NewTicker(time.Duration(api.NodeMemoryReportSeconds) * time.Second)
	defer tick.Stop()
	for {
		e.reportNodeMemory(ctx, store, time.Now())
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}

// reportNodeMemory publishes this scheduler's node, when it has a fresh
// measurement, and caches every node's fresh report.
func (e *Engine) reportNodeMemory(ctx context.Context, store state.NodeMemoryReportStore, now time.Time) {
	if measured, frozen, asOf, ok := e.nodeMemoryLocal(e.ownerNodeID, now); ok {
		report := state.NodeMemoryReport{NodeID: e.ownerNodeID, MeasuredMB: measured, FrozenInstances: frozen, MeasuredAt: asOf}
		if live, ok := e.capacityTable.Lookup(e.ownerNodeID, now); ok {
			used := int(live.UsedMB)
			report.UsedMB = &used
		}
		if err := store.UpsertNodeMemoryReport(ctx, report); err != nil && !errors.Is(err, context.Canceled) {
			e.log.Warn("sched: publish node memory report", "node", e.ownerNodeID, "err", err)
		}
	}
	reports, err := store.NodeMemoryReports(ctx, now.Add(-nodeMemoryPeerMaxAge))
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			e.log.Warn("sched: read node memory reports", "err", err)
		}
		return
	}
	byNode := make(map[string]state.NodeMemoryReport, len(reports))
	for _, r := range reports {
		byNode[r.NodeID] = r
	}
	e.nodeMemoryReports.Store(&byNode)
}

const nodeMemoryPeerMaxAge = time.Duration(api.NodeMemoryPeerMaxAgeSeconds) * time.Second

// nodeMemoryReport is a node's report when it is at most
// NodeMemoryPeerMaxAgeSeconds old.
func (e *Engine) nodeMemoryReport(nodeID string, now time.Time) (state.NodeMemoryReport, bool) {
	reports := e.nodeMemoryReports.Load()
	if reports == nil || nodeID == "" {
		return state.NodeMemoryReport{}, false
	}
	r, ok := (*reports)[nodeID]
	if !ok || now.Sub(r.MeasuredAt) > nodeMemoryPeerMaxAge {
		return state.NodeMemoryReport{}, false
	}
	return r, true
}

// PeerNodeMemory is a peer node's fresh report. This scheduler's own node is
// measured locally and fresher, so it reports false for it.
func (e *Engine) PeerNodeMemory(nodeID string, now time.Time) (state.NodeMemoryReport, bool) {
	if e == nil || nodeID == e.ownerNodeID {
		return state.NodeMemoryReport{}, false
	}
	return e.nodeMemoryReport(nodeID, now)
}

// nodeFrozenCount is a node's frozen VMs as FrozenMaxPerNode counts them:
// the node's report, which counts every owner's, or this ledger's own, the
// larger.
func (e *Engine) nodeFrozenCount(nodeID string) int {
	n := e.ledger.FrozenCountForNode(nodeID)
	if r, ok := e.nodeMemoryReport(nodeID, time.Now()); ok {
		n = max(n, r.FrozenInstances)
	}
	return n
}
