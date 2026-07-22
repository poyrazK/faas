// heartbeat.go is schedd's per-node liveness loop (issue #97 /
// ADR-025 axis 3, PR #114). Loop.Run drives Heartbeat on a fixed
// cadence (DefaultHeartbeatInterval = 30s); each tick does two
// state-mutating operations per active compute_node:
//
//   1. router.Ping(ctx, nodeID)  — wire-level liveness probe.
//      A successful round-trip proves both gRPC socket reachability
//      and that vmmd's goroutine scheduler is responsive enough to
//      schedule the handler. The dial-once-per-target cache (PR #113's
//      VMMRouter) means this reuses the connection lifecycle RPCs
//      already opened, so the heartbeat adds no per-tick dial cost.
//   2. On success: store.HeartbeatComputeNode stamps last_heartbeat_at
//      to now(). On failure: store.MarkComputeNodeInactive flips
//      active=false — placement's ActiveComputeNodes filter then
//      skips the dead node so future wakes don't dial an unreachable
//      target.
//
// One dead node must not stall the rest of the loop: a per-node
// Ping error is logged + the row is flipped, then we move on to the
// next node. ctx cancellation unwinds the loop cleanly; in-flight
// Pings honour the deadline via gRPC's own ctx plumbing.

package sched

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// DefaultHeartbeatInterval is the per-node liveness cadence
// (issue #97 / ADR-025 axis 3, PR #114). 30s matches the freshness
// contract: a future staleness gate (last_heartbeat_at > 2 ×
// interval ⇒ flip inactive) gets a 60s detection window while
// keeping the per-tick load on Postgres minimal (one UPDATE per
// active node, every 30s, with at most a single-digit fleet for
// v1.0). Overridable via FAAS_HEARTBEAT_INTERVAL on cmd/schedd's
// runDeps seam for tests that want a sub-second cadence.
const DefaultHeartbeatInterval = 30 * time.Second

// Heartbeat owns one tick of the per-node liveness sweep. It is
// stateless across ticks — each tick queries the store fresh —
// so a panicking tick does not corrupt subsequent ticks (same
// shape as Watchdog).
type Heartbeat struct {
	store state.Store
	vmm   RoutedVMM
	log   *slog.Logger
	now   func() time.Time // injected for tests
	// Interval is the tick cadence. Zero falls back to
	// DefaultHeartbeatInterval; cmd/schedd's runDeps overrides
	// for tests.
	Interval time.Duration
}

// NewHeartbeat wires the dependencies. store + vmm must be non-nil;
// log may be nil (slog.Default). The returned Heartbeat uses
// DefaultHeartbeatInterval — production callers (cmd/schedd) and
// tests that want a different cadence set .Interval directly
// before calling Run.
func NewHeartbeat(store state.Store, vmm RoutedVMM, log *slog.Logger) *Heartbeat {
	if log == nil {
		log = slog.Default()
	}
	return &Heartbeat{store: store, vmm: vmm, log: log, now: time.Now}
}

// Tick runs one heartbeat sweep: enumerate active compute_nodes,
// Ping each, and stamp or flip accordingly. Exposed so loop.go can
// call it directly from a select case (no goroutine boundary in
// the heartbeat itself; the goroutine that owns the select is
// Loop.Run, same as the watchdog/retention tickers). One Ping
// error must not abort the sweep — we log + flip and move on.
func (h *Heartbeat) Tick(ctx context.Context) error {
	nodes, err := h.store.ActiveComputeNodes(ctx)
	if err != nil {
		// A transient DB error must not crash schedd. Log + return;
		// the next tick will retry. The Watchdog path (1s tick)
		// is unaffected.
		h.log.Warn("heartbeat: list active compute_nodes failed", "err", err)
		return err
	}
	for _, n := range nodes {
		// ctx cancellation check between nodes — a long fleet
		// shouldn't get stuck on a slow Ping if schedd is
		// shutting down.
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := h.vmm.Ping(ctx, n.ID); err != nil {
			// A dead node gets flipped inactive so placement
			// skips it on the next Wake. We don't fail the
			// sweep — one bad node must not block the others.
			h.log.Warn("heartbeat: ping failed; marking inactive",
				"node_id", n.ID, "node_name", n.Name, "err", err)
			if mErr := h.store.MarkComputeNodeInactive(ctx, n.ID); mErr != nil && !errors.Is(mErr, state.ErrNotFound) {
				h.log.Warn("heartbeat: mark-inactive failed",
					"node_id", n.ID, "err", mErr)
			}
			continue
		}
		if err := h.store.HeartbeatComputeNode(ctx, n.ID); err != nil {
			if errors.Is(err, state.ErrNotFound) {
				// Row vanished between ActiveComputeNodes and
				// HeartbeatComputeNode (admin DELETE, retention,
				// etc.) — log + move on.
				h.log.Info("heartbeat: node disappeared mid-sweep",
					"node_id", n.ID)
				continue
			}
			h.log.Warn("heartbeat: stamp failed",
				"node_id", n.ID, "err", err)
		}
	}
	return nil
}

// Run blocks until ctx is cancelled, ticking every h.Interval. It
// is the goroutine entry point used by tests that don't need the
// full Loop wiring; production cmd/schedd drives the heartbeat
// from inside Loop.Run's select (see loop.go's runHeartbeat
// wrapper) so all periodic work shares one ctx.
func (h *Heartbeat) Run(ctx context.Context) error {
	interval := h.Interval
	if interval <= 0 {
		interval = DefaultHeartbeatInterval
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	// First tick fires immediately per time.NewTicker's contract,
	// so a freshly-started schedd stamps the synthetic default-local
	// row's heartbeat right away (no 30s gap on cold start).
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			_ = h.Tick(ctx)
		}
	}
}