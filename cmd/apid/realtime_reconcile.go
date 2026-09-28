package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/realtime"
	"github.com/onebox-faas/faas/pkg/state"
)

// Managed realtime endpoint rows are customer intent. A periodic repair pass
// replays that intent to nodes that became active after a restart or missed a
// best-effort mutation fan-out. The interval is deliberately bounded so a
// node does not stay stale for longer than a normal control-plane window.
const managedRealtimeEndpointReconcileInterval = 30 * time.Second

const (
	managedRealtimeOwnerReapInterval             = time.Minute
	managedRealtimeOwnerReapBatch                = 1000
	managedRealtimeChannelRouteReconcileInterval = 30 * time.Second
)

// Each snapshot holds a session-scoped database lock while it queries a node.
// Keep spare connections available to apid request handling.
const managedRealtimeChannelRouteSnapshotConcurrency = 4

// reconcileManagedRealtimeChannelRoutes replaces each active node's shared
// routing rows from an authoritative connection snapshot. Bounded workers
// snapshot independent nodes concurrently; each per-node lock still makes
// replacement atomic with Subscribe, while generation state keeps publishes
// broad until active nodes have fresh snapshots.
func (s *server) reconcileManagedRealtimeChannelRoutes(ctx context.Context, owner *leasedRealtimeOwner) error {
	if owner == nil || !owner.channelRoutingEnabled || owner.channelRoutes == nil || owner.nodes == nil {
		return nil
	}
	started := time.Now()
	outcome := "complete"
	defer func() {
		owner.channelRouteMetrics.reconcilePass(outcome, time.Since(started).Seconds())
	}()
	nodes, err := owner.nodes.ActiveComputeNodes(ctx)
	if err != nil {
		outcome = realtimeRouteErrorOutcome(ctx, err)
		return fmt.Errorf("list active nodes for realtime channel routing: %w", err)
	}
	finalized, err := reconcileManagedRealtimeChannelRouteNodes(ctx, owner, nodes)
	if err != nil {
		outcome = realtimeRouteErrorOutcome(ctx, err)
	} else if ctx.Err() != nil {
		outcome = "canceled"
	} else if !finalized {
		outcome = "incomplete"
	}
	return err
}

func reconcileManagedRealtimeChannelRouteNodes(ctx context.Context, owner *leasedRealtimeOwner, nodes []state.ComputeNode) (bool, error) {
	snapshotErrs := make([]error, len(nodes))
	jobs := make(chan int)
	var workers sync.WaitGroup
	for range min(len(nodes), managedRealtimeChannelRouteSnapshotConcurrency) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				if ctx.Err() != nil {
					continue
				}
				attempted, err := snapshotManagedRealtimeChannelRoutes(ctx, owner, nodes[index])
				snapshotErrs[index] = err
				if attempted || err != nil {
					snapshotOutcome := "success"
					if err != nil {
						snapshotOutcome = realtimeRouteErrorOutcome(ctx, err)
					}
					owner.channelRouteMetrics.nodeSnapshot(snapshotOutcome)
				}
			}
		}()
	}
dispatch:
	for index := range nodes {
		if ctx.Err() != nil {
			break dispatch
		}
		select {
		case <-ctx.Done():
			break dispatch
		case jobs <- index:
		}
	}
	close(jobs)
	workers.Wait()

	var errs []error
	for _, err := range snapshotErrs {
		if err != nil {
			errs = append(errs, err)
		}
	}
	finalized, err := owner.channelRoutes.FinalizeManagedRealtimeChannelRouteRebuild(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("finalize realtime channel route rebuild: %w", err))
	}
	joined := errors.Join(errs...)
	return finalized, joined
}

func reconcileMissingManagedRealtimeChannelRoutes(ctx context.Context, owner *leasedRealtimeOwner, coordinator state.ManagedRealtimeChannelRouteSnapshotCoordinator) error {
	started := time.Now()
	outcome := "complete"
	defer func() {
		owner.channelRouteMetrics.reconcilePass(outcome, time.Since(started).Seconds())
	}()

	missingNodeIDs, err := coordinator.ListManagedRealtimeChannelRouteNodesNeedingSnapshot(ctx)
	if err != nil {
		outcome = realtimeRouteErrorOutcome(ctx, err)
		return fmt.Errorf("list nodes needing realtime channel route snapshots: %w", err)
	}
	if len(missingNodeIDs) == 0 {
		return nil
	}
	nodes, err := owner.nodes.ActiveComputeNodes(ctx)
	if err != nil {
		outcome = realtimeRouteErrorOutcome(ctx, err)
		return fmt.Errorf("list active nodes for realtime channel route bootstrap: %w", err)
	}
	missing := make(map[string]struct{}, len(missingNodeIDs))
	for _, nodeID := range missingNodeIDs {
		missing[nodeID] = struct{}{}
	}
	needed := make([]state.ComputeNode, 0, len(missing))
	for _, node := range nodes {
		if _, ok := missing[node.ID]; ok {
			needed = append(needed, node)
		}
	}
	finalized, err := reconcileManagedRealtimeChannelRouteNodes(ctx, owner, needed)
	if err != nil {
		outcome = realtimeRouteErrorOutcome(ctx, err)
	} else if ctx.Err() != nil {
		outcome = "canceled"
	} else if !finalized {
		outcome = "incomplete"
	}
	return err
}

func snapshotManagedRealtimeChannelRoutes(ctx context.Context, owner *leasedRealtimeOwner, node state.ComputeNode) (bool, error) {
	lock, err := owner.channelRoutes.AcquireManagedRealtimeChannelRouteLock(ctx, node.ID)
	if err != nil {
		return false, fmt.Errorf("node %s route lock: %w", node.ID, err)
	}
	defer lock.Release(ctx)
	if coordinator, ok := owner.channelRoutes.(state.ManagedRealtimeChannelRouteSnapshotCoordinator); ok {
		current, err := coordinator.ManagedRealtimeChannelRouteNodeSnapshotCurrent(ctx, node.ID)
		if err != nil {
			return false, fmt.Errorf("node %s route snapshot readiness: %w", node.ID, err)
		}
		if current {
			return false, nil
		}
	}
	generation, err := owner.channelRoutes.CurrentManagedRealtimeChannelRouteGeneration(ctx)
	if err != nil {
		return false, fmt.Errorf("node %s route generation: %w", node.ID, err)
	}
	op, err := owner.nodeOperator(node)
	if err != nil {
		return false, fmt.Errorf("node %s operator: %w", node.ID, err)
	}
	connections, err := op.Connections(ctx)
	if err != nil {
		return false, fmt.Errorf("node %s connections: %w", node.ID, err)
	}
	routes := make([]state.ManagedRealtimeChannelRoute, 0)
	for _, connection := range connections {
		if connection.EndpointID == "" {
			continue
		}
		for _, channel := range connection.Channels {
			if !realtime.ValidateChannel(channel) {
				return false, fmt.Errorf("node %s has invalid channel %q in connection snapshot", node.ID, channel)
			}
			routes = append(routes, state.ManagedRealtimeChannelRoute{
				EndpointID: connection.EndpointID,
				Channel:    channel,
				NodeID:     node.ID,
			})
		}
	}
	if err := owner.channelRoutes.ReplaceManagedRealtimeChannelRoutes(ctx, node.ID, generation, routes); err != nil {
		return false, fmt.Errorf("node %s route snapshot: %w", node.ID, err)
	}
	return true, nil
}

func realtimeRouteErrorOutcome(ctx context.Context, err error) string {
	if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "canceled"
	}
	return "error"
}

// runManagedRealtimeChannelRouteReconciler warms and periodically refreshes
// route hints. Publish keeps nodes whose snapshots fail in the fallback set.
func (s *server) runManagedRealtimeChannelRouteReconciler(ctx context.Context) {
	owner, ok := s.realtimeOwner.(*leasedRealtimeOwner)
	if !ok || !owner.channelRoutingEnabled || owner.channelRoutes == nil {
		return
	}
	log := s.log
	if log == nil {
		log = slog.Default()
	}
	runPass := func() {
		coordinator, hasCoordinator := owner.channelRoutes.(state.ManagedRealtimeChannelRouteSnapshotCoordinator)
		if hasCoordinator {
			lock, acquired, err := coordinator.TryAcquireManagedRealtimeChannelRouteReconcileLock(ctx)
			if err != nil {
				owner.channelRouteMetrics.rebuildCheck(realtimeRouteErrorOutcome(ctx, err))
				if !errors.Is(err, context.Canceled) {
					log.Warn("managed realtime channel route reconciler lock could not be acquired", "err", err)
				}
				return
			}
			if !acquired {
				owner.channelRouteMetrics.rebuildCheck("idle")
				return
			}
			defer lock.Release(ctx)
		}
		started, err := owner.channelRoutes.BeginManagedRealtimeChannelRouteRebuild(ctx)
		if err != nil {
			owner.channelRouteMetrics.rebuildCheck(realtimeRouteErrorOutcome(ctx, err))
			if !errors.Is(err, context.Canceled) {
				log.Warn("managed realtime channel route rebuild could not start", "err", err)
			}
			return
		}
		if !started {
			owner.channelRouteMetrics.rebuildCheck("idle")
			if hasCoordinator {
				if err := reconcileMissingManagedRealtimeChannelRoutes(ctx, owner, coordinator); err != nil && !errors.Is(err, context.Canceled) {
					log.Warn("managed realtime channel route bootstrap pass failed", "err", err)
				}
			}
			return
		}
		owner.channelRouteMetrics.rebuildCheck("started")
		if err := s.reconcileManagedRealtimeChannelRoutes(ctx, owner); err != nil && !errors.Is(err, context.Canceled) {
			log.Warn("managed realtime channel route reconciliation pass failed", "err", err)
		}
	}
	runPass()
	ticker := time.NewTicker(managedRealtimeChannelRouteReconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runPass()
		}
	}
}

// reconcileManagedRealtimeEndpoints projects every durable endpoint row onto
// the configured realtime registrar. Disabled rows are included so a missed
// update eventually removes their registration as well. A single bad row is
// isolated from the rest of the pass; the next tick retries all rows.
func (s *server) reconcileManagedRealtimeEndpoints(ctx context.Context) error {
	if s.realtimeRegistrar == nil {
		return nil
	}
	lister, ok := s.store.(state.ManagedRealtimeEndpointLister)
	if !ok {
		// The lister is optional to keep older/narrow store implementations
		// source-compatible. Such stores retain mutation-time synchronization.
		return nil
	}
	// Read the node inventory before durable intent. A concurrent create that
	// happens after this snapshot cannot be mistaken for a stale registration.
	var errs []error
	var inventory realtime.EndpointInventory
	if observer, ok := s.realtimeRegistrar.(realtimeEndpointInventory); ok {
		var inventoryErr error
		inventory, inventoryErr = observer.ListEndpointInventory(ctx)
		if inventoryErr != nil {
			errs = append(errs, fmt.Errorf("list node endpoint inventory: %w", inventoryErr))
		} else if inventory.NodesUnavailable > 0 {
			errs = append(errs, fmt.Errorf("endpoint inventory incomplete: %d nodes unavailable", inventory.NodesUnavailable))
		}
		if inventoryErr != nil && inventory.NodesQueried == 0 {
			// Continue replaying desired rows; a later pass can prune stale
			// registrations once at least one node responds.
			inventory = realtime.EndpointInventory{}
		}
	}
	rows, err := lister.ListManagedRealtimeEndpoints(ctx)
	if err != nil {
		return fmt.Errorf("list managed realtime endpoints: %w", err)
	}

	desired := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		desired[row.ID] = struct{}{}
		if err := s.syncManagedRealtimeEndpoint(ctx, row); err != nil {
			errs = append(errs, fmt.Errorf("endpoint %s: %w", row.ID, err))
			if ctx.Err() != nil {
				break
			}
		}
	}
	for _, id := range inventory.IDs {
		if _, ok := desired[id]; ok {
			continue
		}
		if err := s.realtimeRegistrar.RemoveEndpoint(ctx, id); err != nil {
			errs = append(errs, fmt.Errorf("remove deleted endpoint %s: %w", id, err))
		}
	}
	return errors.Join(errs...)
}

// runManagedRealtimeEndpointReconciler keeps endpoint configuration repaired
// for the lifetime of apid. The first pass is immediate so a newly started
// node does not wait for the first ticker before accepting managed clients.
func (s *server) runManagedRealtimeEndpointReconciler(ctx context.Context) {
	if s.realtimeRegistrar == nil {
		return
	}
	if _, ok := s.store.(state.ManagedRealtimeEndpointLister); !ok {
		return
	}
	log := s.log
	if log == nil {
		log = slog.Default()
	}

	runPass := func() {
		if err := s.reconcileManagedRealtimeEndpoints(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Warn("managed realtime endpoint reconciliation pass failed", "err", err)
		}
	}
	runPass()

	ticker := time.NewTicker(managedRealtimeEndpointReconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runPass()
		}
	}
}

// reapManagedRealtimeConnectionOwners removes expired directory rows left by
// crashed API or realtime processes. The optional interface keeps this safe
// for older stores while the production PgStore provides the implementation.
func (s *server) reapManagedRealtimeConnectionOwners(ctx context.Context) (int64, error) {
	reaper, ok := s.store.(state.ManagedRealtimeConnectionOwnerReaper)
	if !ok {
		return 0, nil
	}
	removed, err := reaper.PruneExpiredManagedRealtimeConnectionOwners(ctx, managedRealtimeOwnerReapBatch)
	if err != nil {
		return 0, fmt.Errorf("prune managed realtime connection owners: %w", err)
	}
	return removed, nil
}

// runManagedRealtimeOwnerReaper keeps the lease directory bounded for the
// lifetime of apid. Cleanup is deliberately independent of the endpoint
// reconciler: owner rows are ephemeral routing hints, not endpoint state.
func (s *server) runManagedRealtimeOwnerReaper(ctx context.Context) {
	if _, ok := s.store.(state.ManagedRealtimeConnectionOwnerReaper); !ok {
		return
	}
	log := s.log
	if log == nil {
		log = slog.Default()
	}
	runPass := func() {
		removed, err := s.reapManagedRealtimeConnectionOwners(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			log.Warn("managed realtime owner reaper pass failed", "err", err)
			return
		}
		if removed > 0 {
			log.Info("managed realtime owner reaper pass complete", "removed", removed)
		}
	}
	runPass()

	ticker := time.NewTicker(managedRealtimeOwnerReapInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runPass()
		}
	}
}
