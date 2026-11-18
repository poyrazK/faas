// adr: 20261118160000 — the control plane keeps app ownership balanced:
// parked, long-idle apps move from the node owning the largest share of its
// admission ceiling to the smallest, paced, and only onto a node that can
// restore their snapshot.
package sched

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/safetext"
	"github.com/onebox-faas/faas/pkg/state"
)

// OwnerBalancer runs the ownership balance. It starts moving apps once the
// gap between the most and least owned node exceeds
// api.OwnerBalanceTriggerSpreadPct points of their ceilings and keeps moving
// until the gap is within api.OwnerBalanceTargetSpreadPct, so ordinary drift
// from new apps does not cause churn.
type OwnerBalancer struct {
	e      *Engine
	active bool
}

// NewOwnerBalancer returns the control plane's ownership balance.
func (e *Engine) NewOwnerBalancer() *OwnerBalancer { return &OwnerBalancer{e: e} }

// Run balances once every api.OwnerBalanceInterval until ctx ends.
func (b *OwnerBalancer) Run(ctx context.Context) {
	tick := time.NewTicker(api.OwnerBalanceInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		if n, err := b.Tick(ctx); err != nil && ctx.Err() == nil {
			b.e.log.Warn("sched: ownership balance", "err", err)
		} else if n > 0 {
			b.e.log.Info("sched: ownership balance moved apps", "count", n)
		}
	}
}

// ownerBalanceNode is one admitting node's ownership as a share of its
// ceiling.
type ownerBalanceNode struct {
	node    state.ComputeNode
	ownedMB int64
}

// sharePct is owned RAM as whole percentage points of the ceiling.
func (n ownerBalanceNode) sharePct() int64 {
	return n.ownedMB * 100 / int64(n.node.AdmissionCeilingMB)
}

// Tick moves up to api.OwnerBalanceMovesPerTick apps and returns how many
// it moved.
func (b *OwnerBalancer) Tick(ctx context.Context) (int, error) {
	e := b.e
	load, ok1 := e.store.(state.AppOwnershipLoadReader)
	candidates, ok2 := e.store.(state.OwnerBalanceCandidateReader)
	reassign, ok3 := e.store.(state.ParkedAppOwnerReassigner)
	if !ok1 || !ok2 || !ok3 {
		return 0, errors.New("sched: ownership balance: store cannot read ownership load or move parked apps")
	}
	all, err := e.store.ActiveComputeNodes(ctx)
	if err != nil {
		return 0, fmt.Errorf("sched: ownership balance: list nodes: %w", err)
	}
	owned, err := load.OwnedAppRAMByNode(ctx)
	if err != nil {
		return 0, fmt.Errorf("sched: ownership balance: read ownership load: %w", err)
	}
	nodes := ownerBalanceNodes(all, owned)
	from, to, ok := ownerBalanceExtremes(nodes)
	if !ok {
		b.active = false
		return 0, nil
	}
	spread := nodes[from].sharePct() - nodes[to].sharePct()
	if !b.active && spread <= api.OwnerBalanceTriggerSpreadPct {
		return 0, nil
	}
	b.active = spread > api.OwnerBalanceTargetSpreadPct
	if !b.active {
		return 0, nil
	}
	source := nodes[from].node
	list, err := candidates.ListOwnerBalanceCandidates(ctx, source.ID, e.now().Add(-api.OwnerBalanceMinIdle), 4*api.OwnerBalanceMovesPerTick)
	if err != nil {
		return 0, fmt.Errorf("sched: ownership balance: list candidates: %w", err)
	}
	legacyOK := legacySnapshotsRestorable(all)
	moved := map[string]bool{}
	count := 0
	for _, c := range list {
		if count >= api.OwnerBalanceMovesPerTick || ctx.Err() != nil {
			break
		}
		if nodes[from].sharePct()-nodes[to].sharePct() <= api.OwnerBalanceTargetSpreadPct {
			b.active = false
			break
		}
		dest, ok := ownerBalanceDestination(nodes, from, c, legacyOK)
		if !ok {
			continue
		}
		if err := reassign.ReassignParkedAppOwner(ctx, c.ID, source.ID, nodes[dest].node.ID); err != nil {
			if errors.Is(err, state.ErrAppResident) || errors.Is(err, state.ErrConflict) {
				continue
			}
			return count, fmt.Errorf("sched: ownership balance: move %s: %w", c.ID, err)
		}
		nodes[from].ownedMB -= int64(c.RAMMB)
		nodes[dest].ownedMB += int64(c.RAMMB)
		moved[nodes[dest].node.ID] = true
		count++
		b.notifyMoved(ctx, c, source, nodes[dest].node)
		_, to, _ = ownerBalanceExtremes(nodes)
	}
	b.prewarm(ctx, moved)
	return count, nil
}

// ownerBalanceNodes keeps the nodes a new app could be given: active,
// admitting, with a ceiling.
func ownerBalanceNodes(all []state.ComputeNode, owned map[string]int64) []ownerBalanceNode {
	out := make([]ownerBalanceNode, 0, len(all))
	for _, n := range all {
		if !n.Active || n.AdmissionCeilingMB <= 0 || (n.Lifecycle != "" && !n.Lifecycle.IsAdmitting()) {
			continue
		}
		out = append(out, ownerBalanceNode{node: n, ownedMB: owned[n.ID]})
	}
	return out
}

// ownerBalanceExtremes returns the indexes of the most and least owned
// nodes; false with fewer than two nodes.
func ownerBalanceExtremes(nodes []ownerBalanceNode) (most, least int, ok bool) {
	if len(nodes) < 2 {
		return 0, 0, false
	}
	for i := range nodes {
		if nodes[i].sharePct() > nodes[most].sharePct() {
			most = i
		}
		if nodes[i].sharePct() < nodes[least].sharePct() {
			least = i
		}
	}
	return most, least, most != least
}

// ownerBalanceDestination is the least owned node, other than the source,
// that serves wakes, can hold one instance of c, and can restore its
// snapshot. A move that would leave the destination owning a larger share
// than the source is refused, so apps never ping-pong.
func ownerBalanceDestination(nodes []ownerBalanceNode, from int, c state.OwnerBalanceCandidate, legacyOK bool) (int, bool) {
	billable := int64(api.BillableRAMMB(c.RAMMB))
	best := -1
	for i, n := range nodes {
		if i == from || int64(n.node.AdmissionCeilingMB) < billable {
			continue
		}
		if n.node.Readiness == state.NodeReadinessDegraded || n.node.Readiness == state.NodeReadinessUnavailable {
			continue
		}
		if c.HasLiveDeployment && !snapshotRestorableOn(c.Snapshot, n.node, legacyOK) {
			continue
		}
		if best < 0 || n.sharePct() < nodes[best].sharePct() {
			best = i
		}
	}
	if best < 0 {
		return 0, false
	}
	src, dst := nodes[from], nodes[best]
	src.ownedMB -= int64(c.RAMMB)
	dst.ownedMB += int64(c.RAMMB)
	return best, dst.sharePct() <= src.sharePct()
}

// notifyMoved tells routers the app has a new owner, as ownership recovery
// does.
func (b *OwnerBalancer) notifyMoved(ctx context.Context, c state.OwnerBalanceCandidate, from, to state.ComputeNode) {
	e := b.e
	e.log.Info("sched: ownership balance: moved app", "app_id", c.ID, "ram_mb", c.RAMMB,
		"last_active_at", c.LastActiveAt, "from_node", from.Name, "to_node", to.Name)
	if e.notif == nil {
		return
	}
	payload := string(safetext.JSONObject(appPlacementChange{Kind: "rebalanced", AppID: c.ID, FromNode: from.ID, ToNode: to.ID}))
	if err := e.notif.Notify(ctx, db.NotifyAppChanged, payload); err != nil {
		e.log.Warn("sched: ownership balance: notify routing", "app_id", c.ID, "err", err)
	}
}

// prewarm queues each moved app's snapshot onto its new owner now, rather
// than at the owner's next cache sweep, so it is local before the app's
// next wake.
func (b *OwnerBalancer) prewarm(ctx context.Context, nodeIDs map[string]bool) {
	store, ok := b.e.store.(state.NodeOwnedSnapshotStore)
	if !ok {
		return
	}
	for id := range nodeIDs {
		if _, err := store.RequeueOwnedSnapshotReplicas(ctx, id, api.OwnedSnapshotRefetchAfter); err != nil {
			b.e.log.Warn("sched: ownership balance: queue moved snapshots", "node_id", id, "err", err)
		}
	}
}
