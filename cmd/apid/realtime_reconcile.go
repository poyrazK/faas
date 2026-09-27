package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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

// reconcileManagedRealtimeChannelRoutes seeds the shared routing directory
// from each active realtime node's authoritative live-connection snapshot.
// Rows are additive: stale positives are safe, while removing a row during a
// concurrent subscribe could make a publish skip a live recipient.
func (s *server) reconcileManagedRealtimeChannelRoutes(ctx context.Context, owner *leasedRealtimeOwner) error {
	if owner == nil || !owner.channelRoutingEnabled || owner.channelRoutes == nil || owner.nodes == nil {
		return nil
	}
	nodes, err := owner.nodes.ActiveComputeNodes(ctx)
	if err != nil {
		return fmt.Errorf("list active nodes for realtime channel routing: %w", err)
	}
	active := make(map[string]struct{}, len(nodes))
	for _, node := range nodes {
		active[node.ID] = struct{}{}
	}
	owner.retainChannelRoutesReady(active)

	var errs []error
	for _, node := range nodes {
		op, err := owner.nodeOperator(node)
		if err != nil {
			owner.setChannelRoutesReady(node.ID, false)
			errs = append(errs, fmt.Errorf("node %s operator: %w", node.ID, err))
			continue
		}
		connections, err := op.Connections(ctx)
		if err != nil {
			owner.setChannelRoutesReady(node.ID, false)
			errs = append(errs, fmt.Errorf("node %s connections: %w", node.ID, err))
			if ctx.Err() != nil {
				break
			}
			continue
		}
		routes := make([]state.ManagedRealtimeChannelRoute, 0)
		for _, connection := range connections {
			if connection.EndpointID == "" {
				continue
			}
			for _, channel := range connection.Channels {
				if !realtime.ValidateChannel(channel) {
					err = fmt.Errorf("invalid channel %q in node snapshot", channel)
					break
				}
				routes = append(routes, state.ManagedRealtimeChannelRoute{
					EndpointID: connection.EndpointID,
					Channel:    channel,
					NodeID:     node.ID,
				})
			}
			if err != nil {
				break
			}
		}
		if err == nil {
			err = owner.channelRoutes.AddManagedRealtimeChannelRoutes(ctx, routes)
		}
		if err != nil {
			owner.setChannelRoutesReady(node.ID, false)
			errs = append(errs, fmt.Errorf("node %s route snapshot: %w", node.ID, err))
			if ctx.Err() != nil {
				break
			}
			continue
		}
		owner.setChannelRoutesReady(node.ID, true)
	}
	return errors.Join(errs...)
}

// runManagedRealtimeChannelRouteReconciler warms and periodically refreshes
// route hints. Publish stays fleet-wide for any node whose snapshot fails.
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
