package sched

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
)

// RecoveryRunner is the durable driver for the recovery arbiter. It owns the
// database enumeration and lifecycle-completion CAS; Arbiter remains a small
// policy object that is easy to test without a store or a ticker.
type RecoveryRunner struct {
	store   state.Store
	arbiter *Arbiter
	events  *events.Platform
	log     *slog.Logger
	// Interval is the recovery sweep cadence. Zero uses the one-second
	// recovery budget from ADR-137.
	Interval time.Duration
}

// NewRecoveryRunner constructs the production recovery loop.
func NewRecoveryRunner(store state.Store, arbiter *Arbiter, eventPlatform *events.Platform, log *slog.Logger) *RecoveryRunner {
	if log == nil {
		log = slog.Default()
	}
	return &RecoveryRunner{
		store:    store,
		arbiter:  arbiter,
		events:   eventPlatform,
		log:      log,
		Interval: time.Second,
	}
}

// Tick runs one complete recovery sweep. Each node is handled independently
// so a transient query or dispatch failure on one node does not starve the
// rest of the fleet. Completion validates the captured generation and remaining
// work atomically with the transition, receipt and audit handoff (ADR-1527).
func (r *RecoveryRunner) Tick(ctx context.Context) error {
	if r == nil || r.store == nil || r.arbiter == nil {
		return nil
	}
	nodes, err := r.store.NodeList(ctx, "")
	if err != nil {
		return fmt.Errorf("sched: recovery: list nodes: %w", err)
	}
	var firstErr error
	for _, node := range nodes {
		if node.Lifecycle != state.NodeLifecycleDraining &&
			node.Lifecycle != state.NodeLifecycleForceDraining &&
			node.Lifecycle != state.NodeLifecycleUnavailable &&
			node.Lifecycle != state.NodeLifecycleRecovering {
			continue
		}

		instances, listErr := r.store.InstanceListByNodeForRecovery(ctx, node.ID)
		if listErr != nil {
			r.rememberError(&firstErr, fmt.Errorf("sched: recovery: list instances for node %s: %w", node.ID, listErr))
			continue
		}
		liveMig, recreated, _, tickErr := r.arbiter.Tick(ctx, []state.ComputeNode{node}, map[string][]state.RecoveryInstance{
			node.ID: instances,
		})
		if tickErr != nil {
			r.rememberError(&firstErr, fmt.Errorf("sched: recovery: dispatch node %s: %w", node.ID, tickErr))
			continue
		}

		// The arbiter may have migrated or recreated rows during the tick.
		// Never use the pre-dispatch slice to declare completion.
		remaining, readErr := r.store.InstanceListByNodeForRecovery(ctx, node.ID)
		if readErr != nil {
			r.rememberError(&firstErr, fmt.Errorf("sched: recovery: recheck node %s: %w", node.ID, readErr))
			continue
		}
		if !recoveryComplete(node.Lifecycle, remaining) {
			r.log.Debug("sched: recovery: node still has live instances",
				"node_id", node.ID, "lifecycle", node.Lifecycle,
				"remaining", len(remaining), "migrated", liveMig, "recreated", recreated)
			continue
		}

		if node.Lifecycle == state.NodeLifecycleUnavailable {
			continue
		}
		if err := r.completeNodeLifecycle(ctx, state.NodeLifecycleCompletion{
			NodeID: node.ID, Generation: node.LifecycleGeneration, Lifecycle: node.Lifecycle,
			DrainedCount: int64(len(instances)), MigratedCount: int64(liveMig), RecreatedCount: int64(recreated),
		}); err != nil && !errors.Is(err, state.ErrConflict) && !errors.Is(err, state.ErrNotFound) {
			r.rememberError(&firstErr, fmt.Errorf("sched: recovery: complete node %s: %w", node.ID, err))
		}
	}
	return firstErr
}

// recoveryComplete treats a recovered node's local, already-running rows as
// healthy. A recovery sweep on the node that owns the row cannot migrate it to
// itself; a peer may still move it, but that is an optimization rather than a
// prerequisite for declaring the node usable again. Draining and unavailable
// nodes still require every recoverable row to leave the physical node.
func recoveryComplete(lifecycle state.NodeLifecycle, instances []state.RecoveryInstance) bool {
	if lifecycle != state.NodeLifecycleRecovering {
		return len(instances) == 0
	}
	for _, instance := range instances {
		switch strings.ToLower(instance.State) {
		case string(state.StateMigrating), "snapshotting":
			return false
		}
	}
	return true
}

// Run performs an immediate reconciliation and then repeats on Interval.
func (r *RecoveryRunner) Run(ctx context.Context) error {
	if r == nil {
		return nil
	}
	if err := r.Tick(ctx); err != nil && !errors.Is(err, context.Canceled) {
		r.log.Warn("sched: recovery: initial tick failed", "err", err)
	}
	interval := r.Interval
	if interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := r.Tick(ctx); err != nil && !errors.Is(err, context.Canceled) {
				r.log.Warn("sched: recovery: tick failed", "err", err)
			}
		}
	}
}

func (r *RecoveryRunner) rememberError(firstErr *error, err error) {
	if *firstErr == nil {
		*firstErr = err
	}
	if r.log != nil {
		r.log.Warn("sched: recovery: node work failed", "err", err)
	}
}
