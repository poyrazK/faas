// pkg/sched/live_migrator.go — Tier A5 (ADR-066) cross-node
// live-instance migration watcher.
//
// Mirror of pkg/sched/rebalancer.go, but consumes the same
// db.NotifyComputeNodesChanged channel and dispatches to the
// live-instance counterpart (Engine.MigrateLiveInstances).
// The split is deliberate: the parked-app rebalancer (Tier A4,
// ADR-064) and the live-instance migrator (Tier A5, ADR-066)
// have different failure modes, different metric labels, and
// different per-tick caps, and bundling them into one watcher
// would conflate the two retry loops.
//
// Architectural note: this is the fifth consumer of the
// compute_nodes_changed pg_notify channel on schedd. Same
// payload shape as the A4 rebalancer — the live migrator also
// only cares about active=false transitions; active=true and
// any malformed payload are dropped silently.
//
// Failure modes:
//
//   - bad payload: log Warn, continue.
//   - active=true: drop (router_watcher's job).
//   - handle returns err: log Warn, continue. A transient PG
//     blip must not stop the loop; the next drain event or the
//     cold-start sweep retries.
//   - ctx cancel: return.

package sched

import (
	"context"
	"encoding/json"

	"github.com/onebox-faas/faas/pkg/db"
)

// LiveMigratorHandle is the per-dead-node work function the
// live-instance migration watcher invokes. The cmd/schedd
// wiring passes Engine.MigrateLiveInstances; the cold-start
// sweep at cmd/schedd's startup invokes the same handle with
// deadNodeID="" (every dead-node's live instances are in
// scope).
type LiveMigratorHandle func(ctx context.Context, deadNodeID string) (int, error)

// LiveMigratorLogger is the minimal slog surface this watcher
// needs. Tests pass nil; the watcher logs nothing in that case.
type LiveMigratorLogger interface {
	Warn(msg string, args ...any)
	Info(msg string, args ...any)
}

// LiveMigrator consumes compute_node_changed events where
// active=false and hands the dead node ID to handle. The
// per-instance outcome (migrated / peer_conflict / lease_expired
// / peer_failure) is reported by the metric inside the handle
// (schedd_live_migration_decisions_total) — the watcher itself
// only logs the batch-level result.
type LiveMigrator struct {
	handle LiveMigratorHandle
	log    LiveMigratorLogger
}

// NewLiveMigrator wires the watcher. handle MUST be non-nil;
// the watcher panics on a nil handle so a missed wiring
// surfaces at startup rather than as silent dead-air at the
// first drain event. The caller owns the pg_notify dial
// lifecycle (cmd/schedd's deps.subscribeLiveMigrator).
func NewLiveMigrator(handle LiveMigratorHandle, log LiveMigratorLogger) *LiveMigrator {
	if handle == nil {
		panic("sched: NewLiveMigrator: handle is nil (live migrate will dead-air at first drain event)")
	}
	return &LiveMigrator{handle: handle, log: log}
}

// Run drains an already-opened channel until ctx is cancelled
// or the channel closes. Returns ctx.Err() on cancellation.
// Each "keep going" decision is deliberate: pg_notify is
// best-effort, the instances table is the source of truth,
// and the cold-start sweep at cmd/schedd's startup
// (Engine.MigrateLiveInstances(ctx, "")) reconciles any
// notify that was lost to a schedd restart.
func (m *LiveMigrator) Run(ctx context.Context, notif <-chan db.Notification) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case n, ok := <-notif:
			if !ok {
				return nil
			}
			m.handleEvent(ctx, n)
		}
	}
}

// handleEvent is the per-message work unit. Filter to
// active=false + valid node_id; dispatch to handle. The
// filter shape mirrors pkg/sched/rebalancer.go and
// pkg/sched/router_watcher.go (the literal "compute_node_keys"
// payload from migration 00076 fails the json.Unmarshal into
// a struct probe and drops silently).
func (m *LiveMigrator) handleEvent(ctx context.Context, n db.Notification) {
	var p struct {
		NodeID string `json:"node_id"`
		Active bool   `json:"active"`
	}
	if err := json.Unmarshal([]byte(n.Payload), &p); err != nil ||
		p.NodeID == "" || p.Active {
		return
	}
	attempted, err := m.handle(ctx, p.NodeID)
	if err != nil {
		if m.log != nil {
			m.log.Warn("sched: live migrator: migrate live failed",
				"dead_node_id", p.NodeID, "err", err)
		}
		return
	}
	if m.log != nil && attempted > 0 {
		m.log.Info("sched: live migrator: attempted",
			"dead_node_id", p.NodeID, "attempted", attempted)
	}
}
