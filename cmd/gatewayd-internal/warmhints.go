// warmhints.go — gatewayd-side consumer for schedd's
// StreamWarmHints gRPC stream (ADR-025 axis 4).
//
// PR #429 shipped the pull half (pkg/sched/warmaffinity.go on
// schedd, pkg/gateway/pgbackend.go::WithWarmHint on the gateway).
// This file is the push half: a long-running consumer that dials
// schedd's StreamWarmHints stream and updates the picker's hint
// cache on every event.
//
// Modeled on cmd/gatewayd/nodecache.go::WatchEvictions — same
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
	"errors"
	"io"
	"log/slog"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

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
// log is the gatewayd daemon logger; nil → slog.Default().
type warmHintConsumer struct {
	sched scheddgrpc.ScheddClient
	cache *gateway.WarmHintCache
	log   *slog.Logger
}

// newWarmHintConsumer wires the consumer. sched must be
// non-nil (production dial fails loudly if it's nil); cache may
// be nil only in tests that drive Run directly with a stub
// stream and don't exercise the cache-write path.
func newWarmHintConsumer(sched scheddgrpc.ScheddClient, cache *gateway.WarmHintCache, log *slog.Logger) *warmHintConsumer {
	if log == nil {
		log = slog.Default()
	}
	return &warmHintConsumer{sched: sched, cache: cache, log: log}
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
//   - io.EOF: clean shutdown, return nil (rare in practice — the
//     schedd stream only ends on ctx cancel).
//   - codes.Canceled: caller (gatewayd) cancelled, return nil.
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
		err = g.drain(ctx, stream)
		switch {
		case err == nil, errors.Is(err, io.EOF), status.Code(err) == codes.Canceled:
			// Clean shutdown — caller cancelled. Exit; the
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
