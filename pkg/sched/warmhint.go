// warmhint.go — schedd's sticky-warm affinity broadcaster
// (placement scheduler PR axis 4, ADR-025).
//
// The PR #429 half of sticky-warm is the pull cache
// (pkg/sched/warmaffinity.go) that the Engine reads at admit time
// to bias a wake toward the node that last warmed the same app.
// This file is the push half: every RecordWake that actually
// changed the cache entry fans out a WarmHintEvent to every
// subscribed gRPC stream consumer (today: every gatewayd-internal).
//
// The broadcaster is intentionally narrow. It does not own a
// snapshot of WarmAffinity — only "what changed since the last
// emit". The engine is the policy owner (the only caller of
// RecordWake, and the only caller of this broadcaster's emit
// method), so the broadcaster lives next to the engine wiring
// rather than next to the cache it observes.
//
// Cold-boot path (ADR-005) is preserved by construction: a
// gatewayd-internal that reconnects sees no events for the warm entries it
// missed during the disconnect. An empty hint on the gateway side
// degrades to least-loaded, identical to a fresh install. ADR-009
// (snapshot reuse) is preserved because the hint is bias-only on
// the consumer side; saturation falls through to per-node
// healthyCount scoring in pkg/gateway/pgbackend.go::pick.
//
// Backpressure: emit is non-blocking. A subscriber whose channel
// is full drops the event and bumps a counter exposed via
// slog.Debug. The producer never blocks on a slow gatewayd-internal; the
// per-subscriber buffer absorbs short-term slack and the picker
// bias-on-stale property keeps the worst-case visible as "one
// extra wake lands on the wrong node", which is the same
// degradation a lost event on the wire already costs.

package sched

import (
	"sync"
	"sync/atomic"
	"time"
)

// defaultWarmHintBufCap is the per-subscriber channel buffer cap
// used by Engine.StreamWarmHints. Mirrors Engine.StreamAppLogs's
// 32-event per-instance channel cap (logs.go:93). A single source
// of truth — the broadcaster's subscribe() fall-back and the
// engine's call site both reference this constant so a future
// change has one knob to turn.
//
// Tuned for the gateway consumer: it reads at wire speed and is
// bounded by the per-subscriber reader goroutine in
// scheddgrpc.Server.StreamWarmHints, so 32 events of slack absorb
// realistic admission bursts without ever forcing a drop on a
// healthy gatewayd-internal. A sustained slow consumer drops events
// regardless; the cap only smooths short-term jitter.
const defaultWarmHintBufCap = 32

// WarmHintEvent is the per-app affinity stamp the broadcaster
// delivers. AppID + NodeID are the load-bearing fields; WrittenAt
// is advisory (clock-skew diagnostics on the consumer side).
//
// The struct mirrors scheddpb.StreamWarmHintsResponse at the wire
// boundary (pkg/scheddgrpc/client_warmhints.go converts on Recv).
// Keeping the canonical type here, in the engine's package, lets
// the broadcaster's emit site reference it without importing the
// gRPC package; the gRPC package re-exports via a type alias next
// to the existing LogFrameSink alias (server.go:45).
// Empty AppID and NodeID with a nonzero WrittenAt indicate a stream heartbeat.
type WarmHintEvent struct {
	AppID     string
	NodeID    string
	WrittenAt time.Time
}

// WarmHintSink is the per-event callback the StreamWarmHints
// handler invokes for each WarmHintEvent decoded from the
// broadcaster. It returns a non-nil error to abort the stream (the
// gRPC trailer carries it back to the gateway caller); a nil
// return tells the handler to keep delivering.
//
// The production caller (pkg/scheddgrpc.Server.StreamWarmHints)
// renders the event to a scheddpb.StreamWarmHintsResponse proto
// and forwards it on the caller's gRPC stream. That work is
// bounded by the proto size; long-running work inside the
// callback would stall backpressure on the matching gateway
// stream, so callers must keep the callback cheap.
//
// Type-aliased by pkg/scheddgrpc (server.go:45 region) so the
// scheddgrpc interface signature can name sched.WarmHintSink
// without an import cycle.
type WarmHintSink func(ev WarmHintEvent) error

// warmHintBroadcaster fans out WarmHintEvents to N subscribers.
//
// Concurrency model:
//   - emit takes the mutex once, iterates the subscriber map, and
//     does a non-blocking send per subscriber (select with
//     default). One producer (Engine.RecordWake); N consumers
//     (one per open gatewayd-internal gRPC stream).
//   - subscribe appends to the map under the mutex; unsubscribe
//     removes + closes the channel.
//   - dropped is an atomic counter the consumer of broadcaster
//     state (slog.Debug) reads; we keep it atomic so the
//     observability path doesn't need the mutex.
//
// The buffer cap passed to subscribe defaults to 32 (matches
// Engine.StreamAppLogs's per-instance channel cap in logs.go:93).
// At bursty admission rates this gives the gatewayd-internal time to drain
// without losing affinity state; on a sustained slow consumer we
// drop, log, and continue.
type warmHintBroadcaster struct {
	mu      sync.Mutex
	subs    map[chan WarmHintEvent]struct{}
	dropped atomic.Uint64
}

// newWarmHintBroadcaster returns a broadcaster ready for emit +
// subscribe.
func newWarmHintBroadcaster() *warmHintBroadcaster {
	return &warmHintBroadcaster{
		subs: make(map[chan WarmHintEvent]struct{}),
	}
}

// emit fans out ev to every subscriber. Non-blocking: a slow
// subscriber's full channel is a no-op for the producer (and a
// dropped counter increment for diagnostics). The producer is the
// engine under its per-app lock; we MUST NOT block here, or a
// stuck gatewayd-internal connection stalls the wake path.
//
// The caller (Engine.admitAndDispatch) is responsible for
// stamping WrittenAt before invoking this method — the
// broadcaster is a pure fan-out and does not mutate the event.
// Empty AppID/NodeID is rejected at the door so a programming
// bug upstream doesn't poison every subscriber.
//
// The caller (Engine.admitAndDispatch) has already verified that
// the (appID, nodeID) pair actually changed — the engine filters
// before calling emit, so this method is the unconditional
// fan-out, not the change detector.
func (b *warmHintBroadcaster) emit(ev WarmHintEvent) {
	if b == nil {
		return
	}
	if ev.AppID == "" || ev.NodeID == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- ev:
		default:
			// Drop on full. The per-subscriber buffer cap is the
			// only thing standing between a slow gatewayd-internal and
			// unbounded blocking; this branch is the natural
			// trade-off the broadcaster makes (no fallback to a
			// disk-backed queue — that's a future ADR if a real
			// ops case calls for it).
			b.dropped.Add(1)
		}
	}
}

// subscribe registers a new subscriber and returns the receive-only
// channel + an unsubscribe closure. The closure is idempotent —
// calling it twice is a no-op.
//
// bufCap <= 0 falls back to defaultWarmHintBufCap (32 events,
// matching Engine.StreamAppLogs's per-instance channel cap in
// logs.go:93). The buffer is per-subscriber, so total memory is
// O(N_gatewayd-internals × bufCap).
func (b *warmHintBroadcaster) subscribe(bufCap int) (<-chan WarmHintEvent, func()) {
	if bufCap <= 0 {
		bufCap = defaultWarmHintBufCap
	}
	ch := make(chan WarmHintEvent, bufCap)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()

	var once sync.Once
	unsub := func() {
		once.Do(func() {
			b.mu.Lock()
			delete(b.subs, ch)
			b.mu.Unlock()
			close(ch)
		})
	}
	return ch, unsub
}

// subscriberCount is the test seam for asserting fan-out
// boundaries (e.g., that unsubscribe actually drops a sub).
func (b *warmHintBroadcaster) subscriberCount() int {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs)
}

// droppedCount is the test seam for asserting non-blocking
// behaviour on a full channel.
func (b *warmHintBroadcaster) droppedCount() uint64 {
	if b == nil {
		return 0
	}
	return b.dropped.Load()
}
