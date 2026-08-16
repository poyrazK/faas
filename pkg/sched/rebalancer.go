// pkg/sched/rebalancer.go — Tier A4 cross-node rebalance watcher.
//
// Subscribes to db.NotifyComputeNodesChanged and reacts only to
// active=false transitions. For each drain event, invokes the
// caller-supplied handle with the dead node ID. The interesting
// policy (admission + cooldown + per-tick cap + conditional
// UPDATE + metric + rebalanced notify) lives in
// Engine.RebalanceOrphanedApps (engine.go); this watcher
// keeps to the "filter + dispatch" loop pattern shared with
// pkg/sched/router_watcher.go.
//
// Architectural note: this is the fourth consumer of the
// compute_nodes_changed pg_notify channel on schedd (alongside
// pkg/sched/router_watcher.go, pkg/sched/nodekeys.go::Run, and
// cmd/schedd/main.go's nodeVerifier). The router watcher refreshes
// the vmmd dial target on every event (active-flip and
// target_url-rotation); nodekeys (now on its own keys-changed
// channel, post-00276) refreshes the trusted-key registry;
// nodeVerifier advances mTLS epoch on peer changes.
// The rebalancer only cares about active=false transitions; it
// drops active=true payloads and any malformed payload silently.
//
// Failure modes:
//
//   - bad payload: log Warn, continue.
//   - active=true: drop (router_watcher's job; the rebalancer
//     only migrates apps away from a dead node, not towards a
//     newly-up one).
//   - handle returns err: log Warn, continue. A transient PG
//     blip must not stop the loop.
//   - ctx cancel: return.

package sched

import (
	"context"
	"encoding/json"

	"github.com/onebox-faas/faas/pkg/db"
)

// RebalancerHandle is the per-dead-node work function the
// watcher invokes. The cold-start sweep in cmd/schedd calls
// Engine.RebalanceOrphanedApps directly with deadNodeID="";
// the live watcher supplies the populated deadNodeID from the
// pg_notify payload. A nil return is success; non-nil is
// logged-and-continued by the watcher.
type RebalancerHandle func(ctx context.Context, deadNodeID string) error

// RebalancerLogger is the minimal slog surface this watcher
// needs. Mirrors pkg/sched/router_watcher.go::RouterWatcherLogger
// and pkg/sched/nodekeys.go::NodeKeyLogger — tests pass nil
// and the watcher logs nothing.
type RebalancerLogger interface {
	Warn(msg string, args ...any)
	Info(msg string, args ...any)
}

// Rebalancer consumes compute_node_changed events where
// active=false and hands the dead node ID to handle. Failures
// log Warn and never propagate; the loop is expected to
// outlive transient blips and be reconciled on next boot by
// the cold-start sweep in cmd/schedd.
type Rebalancer struct {
	handle RebalancerHandle
	log    RebalancerLogger
}

// NewRebalancer wires the watcher with handle + log. handle
// MUST be non-nil; the watcher panics on a nil handle so a
// missed wiring surfaces at startup rather than as silent
// rebalance dead-air at the first drain event.
//
// The caller owns the pg_notify dial lifecycle (see
// db.SubscribeWithReconnect and cmd/schedd's
// deps.subscribeRebalancer); Run consumes an already-opened
// channel.
func NewRebalancer(handle RebalancerHandle, log RebalancerLogger) *Rebalancer {
	if handle == nil {
		panic("sched: NewRebalancer: handle is nil (rebalance will dead-air at first drain event)")
	}
	return &Rebalancer{handle: handle, log: log}
}

// Run drains an already-opened channel until ctx is cancelled
// or the channel closes. Returns ctx.Err() on cancellation.
// Each "keep going" decision is deliberate: pg_notify is
// best-effort, the apps table is the source of truth, and the
// cold-start sweep at cmd/schedd's startup
// (Engine.RebalanceOrphanedApps(ctx, "")) reconciles any
// notify that was lost to a schedd restart.
func (r *Rebalancer) Run(ctx context.Context, notif <-chan db.Notification) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case n, ok := <-notif:
			if !ok {
				return nil
			}
			r.handleEvent(ctx, n)
		}
	}
}

// handleEvent is the per-message work unit. Filter to
// active=false + valid node_id; dispatch to handle. Each
// step logs on failure but never propagates — the loop must
// outlive a transient bad event.
//
// The filter shape mirrors pkg/sched/router_watcher.go: we
// peek at the JSON payload, drop anything that doesn't parse
// (the literal "compute_node_keys" payload from migration
// 00076 fails the json.Unmarshal into a struct probe), and
// drop active=true (router_watcher's job).
func (r *Rebalancer) handleEvent(ctx context.Context, n db.Notification) {
	var p struct {
		NodeID string `json:"node_id"`
		Active bool   `json:"active"`
	}
	if err := json.Unmarshal([]byte(n.Payload), &p); err != nil ||
		p.NodeID == "" || p.Active {
		// Not a node event, an active=true reactivation, or a
		// payload that doesn't parse as a JSON object literal.
		// None of those are the rebalancer's job.
		return
	}
	if err := r.handle(ctx, p.NodeID); err != nil {
		if r.log != nil {
			r.log.Warn("sched: rebalancer: orphan rebalance failed",
				"dead_node_id", p.NodeID, "err", err)
		}
	}
}
