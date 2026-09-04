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
	// Concurrency bounds simultaneous node probes. Zero uses the default.
	Concurrency int
	// ProbeTimeout bounds each dial + Ping. Zero uses the default.
	ProbeTimeout time.Duration

	// recoverMu guards recovering.
	recoverMu sync.Mutex
	// recovering holds the nodes THIS schedd flipped inactive after a
	// failed ping, so the sweep can keep probing them and put them back
	// when they answer.
	//
	// Why an in-process set rather than a query: `compute_nodes.active`
	// is overloaded. It means both "an operator drained this node" and
	// "the watchdog decided it was dead", and nothing on the row
	// distinguishes them (which is exactly why
	// UpsertComputeNodeFromVmmd deliberately preserves active=false —
	// a drained node must not un-drain itself by restarting). Selecting
	// every inactive row and re-activating whatever answers a ping
	// would silently undo operator drains.
	//
	// Tracking only the rows we ourselves marked keeps the recovery
	// strictly to nodes the watchdog took out, and needs no schema
	// change — so ADR-137's lifecycle enum (PR #1218) can replace this
	// wholesale without a migration to unwind.
	recovering map[string]state.ComputeNode
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
	// Staleness gate (issue #98): even if Ping below succeeds,
	// a node whose last_heartbeat_at is older than the
	// threshold is stale and gets flipped inactive. The ping
	// then continues on the next tick, post-deactivation.
	if !n.LastHeartbeatAt.IsZero() && tickNow.Sub(n.LastHeartbeatAt) > staleness {
		h.log.Info("heartbeat: node stale, deactivating",
			"node_id", n.ID, "node_name", n.Name,
			"last_seen", n.LastHeartbeatAt.Format(time.RFC3339),
			"staleness", staleness.String())
		if mErr := h.store.MarkComputeNodeInactive(ctx, n.ID); mErr != nil && !errors.Is(mErr, state.ErrNotFound) {
			h.log.Warn("heartbeat: mark-inactive failed",
				"node_id", n.ID, "err", mErr)
		}
		if h.nodeRegistry != nil {
			h.nodeRegistry.Remove(n.ID)
		}
		return
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
		// Remember it so the sweep keeps probing. Without this the
		// node is gone for good: the sweep enumerates ACTIVE nodes,
		// so a row it just flipped is never looked at again and can
		// only come back via an operator UPDATE or a vmmd restart
		// (PR #1293's re-registration path). A transient ping
		// failure — a vmmd restarting during a rollout — therefore
		// removed a healthy node from the fleet permanently.
		h.markRecovering(n)
		if h.nodeRegistry != nil {
			h.nodeRegistry.Remove(n.ID)
		}
		return
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
			// Runs after Tick so a node flipped inactive by this
			// very sweep is not immediately re-probed with the
			// same failing transport; it gets its first retry on
			// the next tick.
			h.TickRecover(ctx)
		}
	}
}

// markRecovering records a node the watchdog just flipped inactive so
// TickRecover keeps probing it.
func (h *Heartbeat) markRecovering(n state.ComputeNode) {
	h.recoverMu.Lock()
	defer h.recoverMu.Unlock()
	if h.recovering == nil {
		h.recovering = map[string]state.ComputeNode{}
	}
	h.recovering[n.ID] = n
}

// forgetRecovering stops tracking a node (it came back, or its row is
// gone).
func (h *Heartbeat) forgetRecovering(nodeID string) {
	h.recoverMu.Lock()
	defer h.recoverMu.Unlock()
	delete(h.recovering, nodeID)
}

// recoveringSnapshot returns the nodes currently awaiting recovery.
func (h *Heartbeat) recoveringSnapshot() []state.ComputeNode {
	h.recoverMu.Lock()
	defer h.recoverMu.Unlock()
	out := make([]state.ComputeNode, 0, len(h.recovering))
	for _, n := range h.recovering {
		out = append(out, n)
	}
	return out
}

// TickRecover re-probes the nodes this schedd flipped inactive and puts
// back the ones that answer.
//
// This closes a hole that cost real capacity. The main sweep enumerates
// ACTIVE nodes, so the moment it marks a node inactive that node stops
// being probed — the "re-activation happens on the next successful ping"
// the Tick doc claimed could never happen, because there was no next
// ping. Observed in production on 2026-09-04: a vmmd restarting during a
// rollout produced one failed ping, fsn-3 was flipped inactive, and it
// stayed out of rotation with TCP 50051 reachable the whole time. Every
// instance piled onto the surviving node until an operator ran
// `UPDATE compute_nodes SET active = true`.
//
// Scope, deliberately narrow:
//
//   - Only nodes THIS process marked inactive are probed. An
//     operator-drained node is never touched, because `active` alone
//     cannot distinguish the two (see the recovering field comment).
//   - A schedd restart forgets the set. Those nodes stay inactive, the
//     same as today — no regression, and vmmd re-registration
//     (PR #1293) still covers the restart case. The durable fix is
//     ADR-137's lifecycle enum (PR #1218); this is the stop-gap that
//     keeps a rollout from halving the fleet in the meantime.
//   - A row that has vanished is dropped rather than retried forever.
//
// Errors never abort the sweep: one unreachable node must not stop the
// others from recovering.
func (h *Heartbeat) TickRecover(ctx context.Context) {
	for _, n := range h.recoveringSnapshot() {
		if err := ctx.Err(); err != nil {
			return
		}
		// Re-read the row: an operator may have re-activated it, or
		// deleted it, while it sat in the set.
		fresh, err := h.store.ComputeNodeByID(ctx, n.ID)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				h.forgetRecovering(n.ID)
			}
			continue
		}
		if fresh.Active {
			// Someone else brought it back; stop tracking.
			h.forgetRecovering(n.ID)
			continue
		}
		if _, err := h.heartbeatPing(ctx, fresh); err != nil {
			// Still down. Keep it in the set and try next tick.
			continue
		}
		if err := h.store.SetComputeNodeActive(ctx, fresh.ID, true); err != nil {
			h.log.Warn("heartbeat: recovery re-activate failed",
				"node_id", fresh.ID, "node_name", fresh.Name, "err", err)
			continue
		}
		// Stamp the heartbeat immediately. Without it the row is
		// re-activated carrying its old last_heartbeat_at, and the
		// staleness gate in the very next Tick flips it straight back
		// to inactive — a recovery loop that never converges.
		if err := h.store.HeartbeatComputeNode(ctx, fresh.ID); err != nil && !errors.Is(err, state.ErrNotFound) {
			h.log.Warn("heartbeat: recovery stamp failed",
				"node_id", fresh.ID, "err", err)
		}
		h.forgetRecovering(fresh.ID)
		h.log.Info("heartbeat: node answered again; returned to rotation",
			"node_id", fresh.ID, "node_name", fresh.Name)
	}
}
