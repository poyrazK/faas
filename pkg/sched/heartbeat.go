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
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
)

// DefaultHeartbeatInterval is the per-node liveness cadence. Defined
// in pkg/state/heartbeat_gap.go (CP-1) so apid can import it without
// crossing the schedd ownership boundary (spec §Component ownership);
// schedd re-exports it here for the rest of the package's callers.
const DefaultHeartbeatInterval = state.DefaultHeartbeatInterval

// DefaultHeartbeatStaleness is the age threshold at which a stale
// last_heartbeat_at flips active=false. Defined in
// pkg/state/heartbeat_gap.go; schedd re-exports it for callers that
// already import pkg/sched.
const DefaultHeartbeatStaleness = state.DefaultHeartbeatStaleness

// DefaultHeartbeatConcurrency bounds simultaneous probes so a large fleet
// does not turn one heartbeat tick into either a sequential tail or an
// unbounded connection burst.
const DefaultHeartbeatConcurrency = 32

// DefaultHeartbeatProbeTimeout bounds an individual dial + Ping attempt.
// The whole sweep can continue probing other nodes when one transport hangs.
const DefaultHeartbeatProbeTimeout = 5 * time.Second

// HeartbeatGapSummary is re-exported from pkg/state for the same
// reason: schedd callers use it in the property test
// (heartbeat_gap_test.go) without importing pkg/state directly.
type HeartbeatGapSummary = state.HeartbeatGapSummary

// ClassifyHeartbeatGap is re-exported from pkg/state. The classifier
// is intentionally pure — no clock, no store, no side effects — so
// the test oracle (this package's heartbeat_gap_test.go) and the
// production wire shape (apid's GET /v1/compute-nodes/{name}/heartbeats)
// share one function via pkg/state.
func ClassifyHeartbeatGap(prev, curr time.Time, interval, staleness time.Duration) state.HeartbeatGapSummary {
	return state.ClassifyHeartbeatGap(prev, curr, interval, staleness)
}

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
	// ownerNodeID is the Phase 2 / Gate A shard key this schedd
	// owns. Empty = legacy fleet-wide sweep (one-box posture);
	// non-empty = single-node ping (the schedd only watches its
	// own vmmd). Set via WithOwnerNodeID after NewHeartbeat.
	ownerNodeID string
	// nodeRegistry is the notification-backed active-node snapshot. Nil keeps
	// the store enumeration path for compatibility with older fixtures.
	nodeRegistry *NodeRegistry
	// events is the recovery-timeline fan-out (Workstream B /
	// issue #1184 / ADR-137). Wired by cmd/schedd via
	// WithEvents to share the same pkg/events.Platform the wake
	// path uses (one per daemon, single-registry metric +
	// recovery-counter pattern). Nil opts out (no row written;
	// task #66 fills in the actual Emit calls). Keeping the field
	// now means Task #66's commits are diff-only — no struct
	// change required when the emit sites land.
	events *events.Platform
	// Concurrency bounds simultaneous node probes. Zero uses the default.
	Concurrency int
	// ProbeTimeout bounds each dial + Ping. Zero uses the default.
	ProbeTimeout time.Duration
}

// WithOwnerNodeID scopes the heartbeat to a single node. Phase 2
// / Gate A: each schedd pings only its own vmmd; per-node liveness
// is the responsibility of the schedd that owns the node. The
// staleness gate (MarkComputeNodeInactive on a stale node) still
// applies — a dead vmmd on this schedd's own node gets flipped
// inactive and the chooser skips it on the next wake.
func (h *Heartbeat) WithOwnerNodeID(nodeID string) *Heartbeat {
	if h == nil {
		return h
	}
	h.ownerNodeID = nodeID
	return h
}

// WithNodeRegistry makes heartbeat consume the same notification-backed
// active-node snapshot as placement.
func (h *Heartbeat) WithNodeRegistry(reg *NodeRegistry) *Heartbeat {
	if h == nil {
		return h
	}
	h.nodeRegistry = reg
	return h
}

// WithEvents attaches the recovery-timeline fan-out (Workstream B).
// Mirrors Engine.WithEvents — the heartbeat shares the per-daemon
// pkg/events.Platform so recovery events surface on the same SSE
// topic the wake timeline uses (TopicRecovery). Nil opts out.
func (h *Heartbeat) WithEvents(p *events.Platform) *Heartbeat {
	if h == nil {
		return h
	}
	h.events = p
	return h
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
	return &Heartbeat{
		store:        store,
		dialer:       dialer,
		tls:          tlsCfg,
		log:          log,
		now:          time.Now,
		Concurrency:  DefaultHeartbeatConcurrency,
		ProbeTimeout: DefaultHeartbeatProbeTimeout,
	}
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
	var nodes []state.ComputeNode
	if h.nodeRegistry != nil {
		nodes = h.nodeRegistry.Snapshot()
	} else {
		var err error
		nodes, err = h.store.ActiveComputeNodes(ctx)
		if err != nil {
			// A transient DB error must not crash schedd. Log + return;
			// the next tick will retry. The Watchdog path (1s tick)
			// is unaffected.
			h.log.Warn("heartbeat: list active compute_nodes failed", "err", err)
			return err
		}
	}
	// Phase 2 / Gate A: scope the sweep to this schedd's owner
	// node. Multi-node schedd → single-node ping; single-box
	// (empty owner) → legacy fleet-wide sweep unchanged.
	if h.ownerNodeID != "" {
		filtered := nodes[:0]
		for _, n := range nodes {
			if n.ID == h.ownerNodeID {
				filtered = append(filtered, n)
			}
		}
		nodes = filtered
	}
	if len(nodes) == 0 {
		return nil
	}
	concurrency := h.Concurrency
	if concurrency <= 0 {
		concurrency = DefaultHeartbeatConcurrency
	}
	if concurrency > len(nodes) {
		concurrency = len(nodes)
	}
	jobs := make(chan state.ComputeNode)
	var wg sync.WaitGroup
	wg.Add(concurrency)
	for i := 0; i < concurrency; i++ {
		go func() {
			defer wg.Done()
			for n := range jobs {
				h.probeNode(ctx, n, now, staleness)
			}
		}()
	}
	for _, n := range nodes {
		select {
		case jobs <- n:
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return ctx.Err()
		}
	}
	close(jobs)
	wg.Wait()
	return ctx.Err()
}

// probeNode performs one bounded probe and applies its result. It is called by
// the bounded worker pool in Tick; each node is processed exactly once.
func (h *Heartbeat) probeNode(ctx context.Context, n state.ComputeNode, tickNow time.Time, staleness time.Duration) {
	// Workstream B (issue #1184 / ADR-137): the heartbeat owns
	// the lifecycle enum on compute_nodes. The legacy bool
	// `active` column is preserved as a STORED GENERATED column
	// (`lifecycle IN ('active','recovering')`) so placement +
	// pg_notify consumers continue to work unchanged. Helper
	// markLifecycle keeps the CAS shape consistent across the
	// three failure / recovery call sites below.
	markLifecycle := func(expected, next state.NodeLifecycle) {
		if err := h.store.NodeSetLifecycle(ctx, n.ID, expected, next); err != nil {
			// ErrConflict means another writer (drain handler,
			// recovery arbiter) raced us; that's fine — they own
			// the row now. ErrNotFound means admin DELETE / retention
			// removed the row between the ActiveComputeNodes scan
			// and this CAS — also fine.
			if errors.Is(err, state.ErrConflict) || errors.Is(err, state.ErrNotFound) {
				return
			}
			h.log.Warn("heartbeat: lifecycle CAS failed",
				"node_id", n.ID, "expected", expected,
				"next", next, "err", err)
		}
	}

	// Staleness gate (issue #98): even if Ping below succeeds,
	// a node whose last_heartbeat_at is older than the
	// threshold is stale and gets flipped unavailable. The ping
	// then continues on the next tick, post-deactivation. We
	// try both `active` and `recovering` as the expected state
	// because a node that just came back from a previous outage
	// is in `recovering` (still admitting) and shouldn't be
	// demoted by the staleness gate on the way out.
	if !n.LastHeartbeatAt.IsZero() && tickNow.Sub(n.LastHeartbeatAt) > staleness {
		h.log.Info("heartbeat: node stale, marking unavailable",
			"node_id", n.ID, "node_name", n.Name,
			"last_seen", n.LastHeartbeatAt.Format(time.RFC3339),
			"prior_lifecycle", string(n.Lifecycle),
			"staleness", staleness.String())
		markLifecycle(n.Lifecycle, state.NodeLifecycleUnavailable)
		h.emitNodeFailed(ctx, n, n.LastHeartbeatAt)
		if h.nodeRegistry != nil {
			h.nodeRegistry.Remove(n.ID)
		}
		return
	}
	if _, err := h.heartbeatPing(ctx, n); err != nil {
		// A dead node gets flipped unavailable so placement
		// skips it on the next Wake. We don't fail the
		// sweep — one bad node must not block the others.
		// Same expected-state fan-out as the staleness gate.
		h.log.Warn("heartbeat: ping failed; marking unavailable",
			"node_id", n.ID, "node_name", n.Name,
			"prior_lifecycle", string(n.Lifecycle), "err", err)
		markLifecycle(n.Lifecycle, state.NodeLifecycleUnavailable)
		h.emitNodeFailed(ctx, n, n.LastHeartbeatAt)
		if h.nodeRegistry != nil {
			h.nodeRegistry.Remove(n.ID)
		}
		return
	}
	// Ping succeeded: stamp last_heartbeat_at, and if the node
	// was previously unavailable, transition it to `recovering`
	// (still admitting, but the recovery arbiter now owns the
	// sweep). A subsequent tick after the recovery sweep will
	// flip it back to `active`. We don't transition from
	// `recovering` to `active` here — that's the arbiter's job
	// (it has the full recovery context: migration / recreate
	// counts, last_recovery_outcome).
	if n.Lifecycle == state.NodeLifecycleUnavailable {
		h.log.Info("heartbeat: node recovered to recovering",
			"node_id", n.ID, "node_name", n.Name)
		markLifecycle(state.NodeLifecycleUnavailable, state.NodeLifecycleRecovering)
		h.emitNodeRecovered(ctx, n)
	}
	if err := h.store.HeartbeatComputeNode(ctx, n.ID); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			// Row vanished between ActiveComputeNodes and
			// HeartbeatComputeNode (admin DELETE, retention,
			// etc.) — log + move on.
			h.log.Info("heartbeat: node disappeared mid-sweep",
				"node_id", n.ID)
			return
		}
		h.log.Warn("heartbeat: stamp failed",
			"node_id", n.ID, "err", err)
	}
	// CP-1: append one row to the heartbeat history. The
	// received_at / last_heartbeat_at pair uses now() — the
	// same wall-clock the stamp above used — so the wire
	// shape's gap classification reflects the operator's clock,
	// not the database's. A duplicate (node_id, received_at)
	// from a hot tick is OBSERVED (logged as a warning) rather
	// than silently deduped; ErrConflict from the store is the
	// canonical signal. Other errors are logged and the sweep
	// continues — observability must never abort the stamp
	// loop.
	receivedAt := tickNow
	// source='heartbeat_tick' is the only value the routine
	// stamp path writes today. The migration 00065 CHECK also
	// permits 'deactivation' and 'reactivation' for the future
	// watchdog integration (the last contact attempt before a
	// deactivation + the recovery stamp); no code writes them
	// yet. Widening the CHECK when those writes land is the
	// expected evolution; do NOT add them here speculatively.
	if err := h.store.AppendComputeNodeHeartbeat(ctx, n.ID, receivedAt, receivedAt, "heartbeat_tick"); err != nil {
		if errors.Is(err, state.ErrConflict) {
			h.log.Warn("heartbeat: history append duplicate",
				"node_id", n.ID, "received_at", receivedAt.Format(time.RFC3339Nano))
		} else {
			h.log.Warn("heartbeat: history append failed",
				"node_id", n.ID, "err", err)
		}
	}
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
	probeTimeout := h.ProbeTimeout
	if probeTimeout <= 0 {
		probeTimeout = DefaultHeartbeatProbeTimeout
	}
	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	cli, err := h.dialer.Dial(probeCtx, n.TargetURL, h.tls)
	if err != nil {
		return nil, err
	}
	defer func() { _ = cli.Close() }()
	return cli.Ping(probeCtx)
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

// emitNodeFailed stamps the recovery timeline when the heartbeat
// flips a node's lifecycle to unavailable. Both the staleness
// gate and the ping-failure path route through here so the
// dashboard sees a single NodeFailed event regardless of which
// trigger caused the demotion. h.events nil is tolerated (tests
// + legacy wiring that pre-dates the events Platform).
func (h *Heartbeat) emitNodeFailed(ctx context.Context, n state.ComputeNode, lastSeen time.Time) {
	if h.events == nil {
		return
	}
	h.events.EmitRecovery(ctx, events.NodeFailedEvent{
		EmitAt:          time.Now().UTC(),
		NodeID:          n.ID,
		NodeName:        n.Name,
		LastHeartbeatAt: lastSeen,
	})
}

// emitNodeRecovered stamps the recovery timeline when the
// heartbeat flips a previously-unavailable node back to
// recovering. The actual transition to `active` is the
// recovery arbiter's job (it has the migration/recreate
// outcome); this event fires on the first successful
// post-failure ping so the dashboard can correlate with the
// NodeFailed that preceded it.
func (h *Heartbeat) emitNodeRecovered(ctx context.Context, n state.ComputeNode) {
	if h.events == nil {
		return
	}
	h.events.EmitRecovery(ctx, events.NodeRecoveredEvent{
		EmitAt:               time.Now().UTC(),
		NodeID:               n.ID,
		NodeName:             n.Name,
		RecoveryInitiatedAt:  time.Now().UTC(),
	})
}
