// warmhints.go — gatewayd-internal-side consumer for schedd's
// StreamWarmHints gRPC stream (ADR-025 axis 4).
//
// PR #429 shipped the pull half (pkg/sched/warmaffinity.go on
// schedd, pkg/gateway/pgbackend.go::WithWarmHint on the gateway).
// This file is the push half: a long-running consumer that dials
// schedd's StreamWarmHints stream and updates the picker's hint
// cache on every event.
//
// Modeled on cmd/gatewayd-internal/nodecache.go::WatchEvictions — same
// outer reconnect loop, same backoff cadence, same heartbeat
// pattern (a slog.Debug line every 30s, no Prometheus surface;
// the Phase 3 review chose slog.Debug only for dropped events).
//
// Disconnect policy (Phase 3 review): freeze. The cache holds
// its last-known hints forever; a transient blip costs zero
// picker behaviour change. A 30-minute outage means stale hints,
// but the picker is bias-only and falls through to per-node
// healthyCount scoring on saturation (ADR-009). The WarmAffinityTTL
// on schedd (api.WarmAffinityTTL, default 30 min) bounds staleness
// on both sides simultaneously, so a gatewayd that reconnects
// after a long outage converges within one TTL.
//
// Fail-fast contract: the consumer never blocks the public
// listener. If the schedd dial fails, the consumer logs and
// reconnects with backoff; the picker's WarmHintFunc returns
// "no hint" until the cache converges (ADR-005 cold-boot-safe
// path).

package main

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/scheddgrpc"
)

// warmHintConsumer drains the schedd StreamWarmHints stream and
// updates the picker's hint cache. Constructed once at gatewayd
// startup; Run is the long-lived goroutine that owns the
// reconnect loop.
//
// Phase 2 / Gate A: sched is a scheddgrpc.ScheddClient (any node's
// client works because the warm hint cache is the consumer's
// payload — the stream emits (app_id, node_id) tuples keyed by the
// schedd's local scope; on a single-box install the only schedd
// emits every hint; on a multi-box install, multiple schedds each
// emit a slice). Today the production wiring dials ONE schedd for
// the stream; full multi-stream fan-in is a v1.1 follow-up (the
// picker degrades to per-node healthyCount scoring on saturation,
// so missing hints are bias-only — ADR-005 cold-boot-safe path
// preserved).
//
// cache is the *gateway.warmHintCache whose HintFunc is wired
// into PGBackend via WithWarmHint. The consumer holds a pointer
// and writes via cache.update; the picker reads via cache.hint
// on every Pick.
//
// onTouch is a callback the consumer invokes after every
// successful cache.Update or heartbeat — and once at construction (see
// the /readyz staleness-signal wiring in run.go). The
// callback drives a gateway.NewStalenessSignal instance so
// the daemon's /readyz reflects "the warm-hint stream is
// alive and delivering" rather than the looser "the
// consumer was constructed". An absent callback (no /readyz
// staleness wired, e.g. the e2e harness) is a no-op so the
// same consumer shape works in tests that don't construct
// the probe.
//
// log is the gatewayd daemon logger; nil → slog.Default().
type warmHintConsumer struct {
	sched   scheddgrpc.ScheddClient
	cache   *gateway.WarmHintCache
	log     *slog.Logger
	touchMu sync.RWMutex
	onTouch func()
}

// newWarmHintConsumer wires the consumer. sched must be
// non-nil (production dial fails loudly if it's nil); cache may
// be nil only in tests that drive Run directly with a stub
// stream and don't exercise the cache-write path.
//
// onTouch is invoked once at construction so the
// /readyz staleness signal's first helper-goroutine tick
// observes `touched > 0` and stays in "fresh" state —
// without this, the goroutine reads `touched == 0` on every
// tick and flips the signal false with reason "no touch yet"
// (see gateway.NewStalenessSignal — the signal is pre-armed
// true at construction, but the helper goroutine overrides
// that on the first tick when lastTouch is still zero).
// nil onTouch is allowed; the consumer then doesn't touch.
func newWarmHintConsumer(sched scheddgrpc.ScheddClient, cache *gateway.WarmHintCache, log *slog.Logger, onTouch func()) *warmHintConsumer {
	if log == nil {
		log = slog.Default()
	}
	c := &warmHintConsumer{sched: sched, cache: cache, log: log, onTouch: onTouch}
	// Touch once at construction. This is the fix for the
	// PR-B1 regression caught by code review of PR #880:
	// before the touch was called from newWarmHintConsumer,
	// the staleness helper's goroutine saw touched == 0
	// forever and flipped the signal false with reason
	// "no touch yet" on every tick — /readyz was 503
	// forever (the LB never routed traffic). Now Run()
	// also touches on every successful delivery (and on
	// reconnect after the helper goroutine has already
	// stalled), but this initial touch makes the
	// "consumer constructed but no events yet delivered"
	// window safe.
	//
	// In production run.go's runDeps() constructs the consumer
	// BEFORE /readyz staleness is wired (the staleness signal
	// is built in runWithDeps once the probe is in scope), so
	// runDeps passes nil here. The real touch callback is
	// supplied via SetOnTouch right after the staleness
	// signal is constructed, and that call invokes the touch
	// once immediately to cover the gap between consumer
	// construction (runDeps) and signal wiring (runWithDeps).
	if onTouch != nil {
		onTouch()
	}
	return c
}

// SetOnTouch installs (or replaces) the onTouch callback after
// construction. The caller invokes it once immediately to seed
// the staleness signal's lastTouch timestamp, so the helper
// goroutine observes `touched > 0` from its first tick onwards
// and the signal stays in "fresh" state across the natural gap
// between consumer construction (runDeps) and probe wiring
// (runWithDeps).
//
// Replaces any prior callback; passing nil is allowed and
// effectively disables the staleness touch (e.g. tests that
// construct the consumer without a probe).
func (g *warmHintConsumer) SetOnTouch(touch func()) {
	g.touchMu.Lock()
	g.onTouch = touch
	g.touchMu.Unlock()
	if touch != nil {
		// Seed the staleness signal's lastTouch so the helper
		// goroutine doesn't observe "no touch yet" on its first
		// tick (see NewStalenessSignal — the goroutine flips the
		// signal false with reason "no touch yet" until the first
		// touch lands, irrespective of the pre-arm at construction).
		touch()
	}
}

func (g *warmHintConsumer) touch() {
	g.touchMu.RLock()
	touch := g.onTouch
	g.touchMu.RUnlock()
	if touch != nil {
		touch()
	}
}

// Run is the long-lived consumer loop. It dials schedd's
// StreamWarmHints stream, drains it into cache.update, and
// reconnects on transient errors with a fixed
// 1s/2s/5s/10s/30s capped backoff (matches the cadence used by
// subscribeWithReconnect on the schedd side — see
// cmd/schedd/main.go:558-613).
//
// Wire error mapping:
//
//   - io.EOF: reconnect while the daemon context remains active.
//   - codes.Canceled: reconnect unless the daemon context was canceled.
//   - codes.Unavailable: transient — reconnect with backoff.
//   - anything else: log + reconnect (treat as transient until
//     a real ops case calls for stronger semantics).
//
// Returns nil on ctx cancel; non-nil only on a programmer error
// (nil sched, nil cache — both caught at construction time).
func (g *warmHintConsumer) Run(ctx context.Context) {
	if g.sched == nil {
		g.log.Error("gatewayd: warm hint consumer has no schedd client; stream disabled")
		<-ctx.Done()
		return
	}
	if g.cache == nil {
		g.log.Error("gatewayd: warm hint consumer has no cache; stream disabled")
		<-ctx.Done()
		return
	}
	backoff := 1 * time.Second
	const maxBackoff = 30 * time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		stream, err := g.sched.StreamWarmHints(ctx)
		if err != nil {
			g.log.Warn("gatewayd: warm hint stream dial failed; cache frozen",
				"err", err, "retry_in", backoff.String())
			if !sleepCtx(ctx, backoff) {
				return
			}
			backoff = nextBackoff(backoff, maxBackoff)
			continue
		}
		// Reset backoff after a successful dial — the next
		// reconnect starts from 1s again.
		backoff = 1 * time.Second
		g.log.Info("gatewayd: warm hint stream connected")
		// Touch the /readyz staleness signal as soon as the
		// stream establishes. Without this, the staleness
		// window (2 minutes in production) starts ticking
		// from construction rather than from the actual
		// first successful dial, and a slow schedd cold-boot
		// that takes >2 min to listen would cause /readyz to
		// flip false with reason "stale" before the stream
		// ever delivers an event.
		g.touch()
		err = g.drain(ctx, stream)
		switch {
		case ctx.Err() != nil:
			// The daemon context was canceled. Exit; the
			// daemon's main ctx drives this return.
			return
		default:
			g.log.Warn("gatewayd: warm hint stream recv error; reconnecting",
				"err", err, "retry_in", backoff.String())
			if !sleepCtx(ctx, backoff) {
				return
			}
			backoff = nextBackoff(backoff, maxBackoff)
		}
	}
}

// drain pulls events from stream until ctx cancels or the stream
// errors. Each event updates the cache. Returns nil on ctx
// cancel; io.EOF when the stream closes cleanly outside of a
// cancel; the underlying gRPC error otherwise. A stream error that
// races ctx cancel (e.g. io.EOF delivered in the same goroutine
// cycle as ctx.Done) is folded into nil — caller wants the cancel
// path to win so Run() exits instead of treating a stale EOF as a
// transient blip and reconnecting.
func (g *warmHintConsumer) drain(ctx context.Context, stream scheddgrpc.WarmHintStream) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		ev, err := stream.Recv()
		if err != nil {
			if ctx.Err() != nil {
				// ctx wins — return nil so Run() exits cleanly
				// instead of reconnecting on a stream teardown
				// that was triggered by the cancel.
				//
				// nolint:nilerr // Mirrors the bearer() fallback
				// at oci.go:1197-1203: a stream teardown that
				// races ctx cancel folds into the clean-shutdown
				// path; returning the underlying error would
				// trigger a spurious reconnect.
				return nil
			}
			return err
		}
		if ev.AppID == "" && ev.NodeID == "" && !ev.WrittenAt.IsZero() {
			g.touch()
			continue
		}
		// Drop malformed events silently — schedd is the only
		// producer and we trust its emit path. Empty AppID/NodeID
		// would be a programming bug; logging at warn level is
		// enough.
		if ev.AppID == "" || ev.NodeID == "" {
			g.log.Warn("gatewayd: warm hint event missing app_id/node_id",
				"app_id", ev.AppID, "node_id", ev.NodeID)
			continue
		}
		g.cache.Update(ev.AppID, ev.NodeID)
		// Touch the /readyz staleness signal on every successful
		// delivery. The signal's helper goroutine (see
		// gateway.NewStalenessSignal) flips the bit false after
		// 2 minutes without a touch; in steady state the schedd
		// stream emits sub-second, so a touch per delivery keeps
		// the staleness window well-respected and surfaces
		// "stream silently deadlocked" within 2 minutes as a
		// /readyz=503 (the LB then takes this daemon out of
		// rotation until a delivery resumes).
		//
		// touch() is itself nil-safe via the consumer's onTouch
		// guard, so the same shape works when /readyz staleness
		// isn't wired (e2e harness, unit tests).
		g.touch()
	}
}

// sleepCtx is time.After + ctx.Done. Returns false if ctx fired
// during the sleep.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// nextBackoff doubles d up to max. Mirrors the schedd-side
// subscribeWithReconnect pattern (cmd/schedd/main.go:558-613)
// so the two daemons use a consistent cadence.
func nextBackoff(d, max time.Duration) time.Duration {
	next := d * 2
	if next > max {
		return max
	}
	return next
}
