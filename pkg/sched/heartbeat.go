// Package sched — heartbeat goroutine (issue #97 #98 / ADR-025 axis 3
// + ADR-028).
//
// schedd is the authority on "is this compute_node still alive?".
// schedd pings each registered vmmd on a tick (default 30s), and:
//   - on success, calls HeartbeatComputeNode to stamp
//     last_heartbeat_at = now()
//   - on failure, flips active=false once the timestamp ages past
//     the staleness window (default 90s = 3× the 30s tick)
//
// Wire primitive: router.Ping (PR #114) — proven the socket is
// reachable AND vmmd's goroutine scheduler is responsive enough to
// schedule a handler. A successful round-trip is the only signal
// schedd needs to keep last_heartbeat_at fresh.
//
// Direction was chosen to invert the vmmd-pushes design. schedd is
// the admission authority and shouldn't trust inbound traffic from a
// box it may have already drained; outbound probing means schedd
// detects failure on its own clock, not on the box's.
//
// The goroutine owns its own ticker (not the §6.1 1s watchdog
// ticker) because the cadence is fundamentally different — 30s for
// heartbeat vs 1s for state-stuck detection — and conflating them
// would force schedd's hot loop to do a per-row DB read 30× more
// often than needed.

package sched

import (
	"context"
	"crypto/tls"
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

// DefaultHeartbeatStaleness is the age threshold at which a stale
// last_heartbeat_at flips active=false. 90s = 3× the 30s tick; the
// ratio gives one retry a chance before deactivation kicks in
// (issue #98 / ADR-028 acceptance: "Watchdog marks a node
// active=false after 90s of missed pings").
const DefaultHeartbeatStaleness = 90 * time.Second

// Heartbeat owns one tick of the per-node liveness sweep. It is
// stateless across ticks — each tick queries the store fresh —
// so a panicking tick does not corrupt subsequent ticks (same
// shape as Watchdog).
type Heartbeat struct {
	store state.Store
	// dialer is the per-tick fresh-dial path (issue #120). The
	// heartbeat dials a fresh *VMMClient per node per tick instead
	// of reusing the VMMRouter's cached conn, matching the
	// pkg/overlay package-doc intent: "every heartbeat pays the
	// dial cost and sees the truth". A cached conn could let a
	// stale transport look healthy right when the heartbeat
	// should be reporting failure. The dialer receives the node's
	// target_url and the mTLS config; production wires it to a
	// closure that calls overlay.Dial + sched.DialVMMContext.
	dialer HeartbeatDialer
	tls    *tls.Config
	log    *slog.Logger
	now    func() time.Time // injected for tests
	// Interval is the tick cadence. Zero falls back to
	// DefaultHeartbeatInterval; cmd/schedd's runDeps overrides
	// for tests.
	Interval time.Duration
	// Staleness is the age threshold for deactivation. Zero
	// falls back to DefaultHeartbeatStaleness.
	Staleness time.Duration
}

// HeartbeatDialer is the per-tick fresh-dial contract. The heartbeat
// invokes Dial once per (tick, node) — the returned client MUST be
// short-lived; Close it before the next iteration so a per-tick
// resource leak doesn't compound across the daemon's lifetime.
//
// Why a separate interface from VMM/RoutedVMM: the heartbeat's only
// need is "open a fresh conn to this target_url and ping it". The
// VMMRouter interface (and the VMM interface above it) carry the
// full lifecycle surface — CreateColdBoot, CreateFromSnapshot,
// PauseAndSnapshot, Destroy — none of which the heartbeat calls.
// Splitting the surface keeps the heartbeat's test seam tight and
// makes the per-tick dial cost observable in a unit test.
type HeartbeatDialer interface {
	Dial(ctx context.Context, targetURL string, tlsCfg *tls.Config) (VMM, error)
}

// HeartbeatDialerFunc adapts an ordinary function to the
// HeartbeatDialer interface. It exists so cmd/schedd can pass the
// existing deps.dialVMM closure directly (whose signature already
// matches) without inventing a new named type or wrapper type per
// caller; the alternative (a per-caller adapter struct) would just
// echo this same body.
type HeartbeatDialerFunc func(ctx context.Context, targetURL string, tlsCfg *tls.Config) (VMM, error)

// Dial implements HeartbeatDialer.
func (f HeartbeatDialerFunc) Dial(ctx context.Context, targetURL string, tlsCfg *tls.Config) (VMM, error) {
	return f(ctx, targetURL, tlsCfg)
}

// NewHeartbeat wires the dependencies. store + dialer must be
// non-nil; log may be nil (slog.Default). tlsCfg may be nil for
// unix-only deployments (single-box default); tcp/dns targets
// require a populated mTLS config (issue #95). The returned
// Heartbeat uses the defaults — production callers (cmd/schedd)
// and tests that want a different cadence set .Interval /
// .Staleness directly before calling Run.
func NewHeartbeat(store state.Store, dialer HeartbeatDialer, tlsCfg *tls.Config, log *slog.Logger) *Heartbeat {
	if log == nil {
		log = slog.Default()
	}
	return &Heartbeat{store: store, dialer: dialer, tls: tlsCfg, log: log, now: time.Now}
}

// Tick runs one heartbeat sweep: enumerate active compute_nodes,
// ping each via router.Ping, and stamp or flip accordingly. Exposed
// so loop.go can call it directly from a select case (no goroutine
// boundary in the heartbeat itself; the goroutine that owns the
// select is Loop.Run, same as the watchdog/retention tickers). One
// Ping error must not abort the sweep — we log + flip and move on.
//
// Tick honours the staleness gate (issue #98 / ADR-028): a row
// whose last_heartbeat_at has aged past h.Staleness is flipped
// inactive even if Ping just succeeded (defence-in-depth — Ping
// racing with a half-shut vmmd might return OK once after the box
// was already dead). Re-activation happens on the next successful
// ping post-recovery, same as PR #114's pre-#98 behaviour.
func (h *Heartbeat) Tick(ctx context.Context) error {
	staleness := h.Staleness
	if staleness <= 0 {
		staleness = DefaultHeartbeatStaleness
	}
	now := h.now()
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
		// Staleness gate (issue #98): even if Ping below succeeds,
		// a node whose last_heartbeat_at is older than the
		// threshold is stale and gets flipped inactive. The ping
		// then continues on the next tick, post-deactivation.
		if !n.LastHeartbeatAt.IsZero() && now.Sub(n.LastHeartbeatAt) > staleness {
			h.log.Info("heartbeat: node stale, deactivating",
				"node_id", n.ID, "node_name", n.Name,
				"last_seen", n.LastHeartbeatAt.Format(time.RFC3339),
				"staleness", staleness.String())
			if mErr := h.store.MarkComputeNodeInactive(ctx, n.ID); mErr != nil && !errors.Is(mErr, state.ErrNotFound) {
				h.log.Warn("heartbeat: mark-inactive failed",
					"node_id", n.ID, "err", mErr)
			}
			continue
		}
		if _, err := h.heartbeatPing(ctx, n); err != nil {
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

// heartbeatPing dials a fresh *VMMClient per call (issue #120 —
// every heartbeat pays the dial cost) and closes it before
// returning. The dialer is the production HeartbeatDialer wired by
// cmd/schedd (overlay.Dial → sched.DialVMMContext); tests inject a
// counting stub so the per-tick dial cost is observable in
// heartbeat_test.go. We do NOT pass ctx.Done into the dial — the
// per-tick dial is bounded by ctx from the call site (Tick's ctx
// is loop.go's loopCtx, which is cancelled on shutdown).
func (h *Heartbeat) heartbeatPing(ctx context.Context, n state.ComputeNode) (*PingOutcome, error) {
	if h.dialer == nil {
		return nil, errors.New("heartbeat: dialer not configured")
	}
	cli, err := h.dialer.Dial(ctx, n.TargetURL, h.tls)
	if err != nil {
		return nil, err
	}
	defer func() { _ = cli.Close() }()
	return cli.Ping(ctx)
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
