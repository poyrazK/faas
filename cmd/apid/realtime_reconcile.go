package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// Managed realtime endpoint rows are customer intent. A periodic repair pass
// replays that intent to nodes that became active after a restart or missed a
// best-effort mutation fan-out. The interval is deliberately bounded so a
// node does not stay stale for longer than a normal control-plane window.
const managedRealtimeEndpointReconcileInterval = 30 * time.Second

const (
	managedRealtimeOwnerReapInterval = time.Minute
	managedRealtimeOwnerReapBatch    = 1000
)

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
	rows, err := lister.ListManagedRealtimeEndpoints(ctx)
	if err != nil {
		return fmt.Errorf("list managed realtime endpoints: %w", err)
	}

	var errs []error
	for _, row := range rows {
		if err := s.syncManagedRealtimeEndpoint(ctx, row); err != nil {
			errs = append(errs, fmt.Errorf("endpoint %s: %w", row.ID, err))
			if ctx.Err() != nil {
				break
			}
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
