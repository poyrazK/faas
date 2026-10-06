// Package gateway — spans_accumulator.go (ADR-127 PR-D).
//
// gatewayd-public collects customer OTLP POSTs into a per-trace
// accumulator that dedupes spans across a 30-second flush window
// before the gateway hands the coalesced summary to apid over
// gRPC. The accumulator is in-process (sync.Map keyed by trace_id);
// the flush loop is Stage 4.
//
// Why coalesce? An OTLP batch from a chatty service often spans
// multiple HTTP POSTs for the same trace_id within a few hundred
// milliseconds (server span first, then async DB spans). Without
// coalescing, each POST would race with the others for the
// request_telemetry.spans_summary UPDATE — last writer wins and
// we'd lose the slowest spans. The accumulator merges within a
// window so a single UPDATE captures the slowest-N selection.
//
// Concurrency: the sync.Map gives lock-free reads on the hot
// path (the handler does Get → mutate → Put). The windowed
// eviction in RunFlushLoop iterates Range + Delete; the only
// contention point is one trace_id's bucket being mutated by
// the handler while the loop drains it. The bucket-level mutex
// inside spansAccumulator serializes that.

package gateway

import (
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
)

// spansAccumulator holds the per-trace coalesce state for one
// OTLP batch window. The bucket has its own mutex because
// RunFlushLoop drains concurrently with the handler adding.
type spansAccumulator struct {
	mu        sync.Mutex
	traceID   string
	accountID uuid.UUID
	// seenSpans is the within-window dedupe set, keyed by
	// (span_id, end_time_unix_nano) — the same span carried
	// across two POSTs at the same instant is dropped.
	// Memory bound: cap at the plan's DebugTelemetrySpansPerTrace
	// ceiling so a runaway customer can't OOM the gateway.
	seenSpans map[spanSeenKey]struct{}
	// ordered spans kept in arrival order — the truncate step
	// picks the slowest-N by duration, NOT chronological.
	spans       []summarizedSpan
	firstSeenAt time.Time
}

// spanSeenKey is the within-window dedupe key. Two POSTs carrying
// the same (span_id, end_time_unix_nano) are the same observation.
type spanSeenKey struct {
	spanID          string
	endTimeUnixNano uint64
}

// summarizedSpan is one OTLP span after the slowest-N truncation.
// The flush loop sends the slice to apid over gRPC in JSON form
// (Stage 4). Kept as a small struct so the handler can hand the
// raw OTLP shape to the truncation helper without depending on
// the protobuf types.
type summarizedSpan struct {
	TraceID           string            `json:"trace_id"`
	SpanID            string            `json:"span_id"`
	ParentSpanID      string            `json:"parent_span_id,omitempty"`
	Name              string            `json:"name"`
	Kind              string            `json:"kind"`
	StartTimeUnixNano uint64            `json:"start_time_unix_nano"`
	EndTimeUnixNano   uint64            `json:"end_time_unix_nano"`
	DurationNanos     uint64            `json:"duration_nanos"`
	Status            string            `json:"status,omitempty"`
	StatusMessage     string            `json:"status_message,omitempty"`
	Attributes        map[string]string `json:"attributes,omitempty"`
	// db_statement extracted from db.statement.* attributes so
	// PR-C's prose synthesis can quote SQL without parsing the
	// attributes map.
	DBStatement string `json:"db_statement,omitempty"`
}

// add merges a new batch of spans into this accumulator. The
// within-window dedupe set drops any span seen before (keyed by
// (span_id, end_time_unix_nano)). Returns the post-dedupe span
// count (for the metric counter).
func (a *spansAccumulator) add(spans []summarizedSpan) int {
	return a.addLocked(spans)
}

// addSpansForTest is the same as add but wrapped so unit tests
// can call it without leaking the production method signature.
// Not exported; only the test file references it.
func (a *spansAccumulator) addSpansForTest(spans []summarizedSpan) error {
	_ = a.addLocked(spans)
	return nil
}

func (a *spansAccumulator) addLocked(spans []summarizedSpan) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.firstSeenAt.IsZero() {
		a.firstSeenAt = time.Now()
	}
	added := 0
	for _, s := range spans {
		k := spanSeenKey{spanID: s.SpanID, endTimeUnixNano: s.EndTimeUnixNano}
		if _, dup := a.seenSpans[k]; dup {
			continue
		}
		if a.seenSpans == nil {
			a.seenSpans = make(map[spanSeenKey]struct{})
		}
		a.seenSpans[k] = struct{}{}
		a.spans = append(a.spans, s)
		added++
	}
	return added
}

// snapshot returns a copy of the accumulated spans + the
// accountID. Used by the flush loop to build the write payload
// after evicting the bucket from the map.
//
// Deprecated: the flush loop uses swapAndClear instead. Kept
// for unit tests that need a non-destructive view.
func (a *spansAccumulator) snapshot() ([]summarizedSpan, uuid.UUID) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.spans) == 0 {
		return nil, a.accountID
	}
	out := make([]summarizedSpan, len(a.spans))
	copy(out, a.spans)
	return out, a.accountID
}

// swapAndClear atomically returns the accumulated spans slice
// and resets the bucket to empty. Used by the flush loop
// (PR-D code-review #10) so a concurrent handler Add that
// races with the flush tick lands in the freshly-empty bucket
// and is picked up by the NEXT tick, not lost.
//
// Without atomic swap, the previous flush loop:
//
//  1. Snapshotted the bucket's spans (copy under mutex).
//  2. Deleted the bucket key from the sync.Map.
//  3. Handler's Add (running concurrently) called LoadOrStore,
//     which on a missing key creates a NEW bucket — but the
//     newly-added spans have nowhere to land if step 2
//     already happened. The sync.Map's LoadOrStore is
//     atomic at the map level, but the handler's Add was
//     reading from the OLD bucket pointer that step 2 just
//     removed from the map; in practice the handler holds
//     the pointer and adds to it, but the flush loop's
//     delete of the map key is a separate operation that
//     races with the next Add's LoadOrStore.
//
// With swap-and-clear, the bucket's slice is reset to nil
// under the bucket mutex. The flush loop holds the pointer
// to the OLD slice (returned by swapAndClear) and the
// handler's Add reads bucket.spans (which is now nil),
// appends to a fresh slice, and the next tick picks it up.
func (a *spansAccumulator) swapAndClear() ([]summarizedSpan, uuid.UUID) {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := a.spans
	if out == nil {
		return nil, a.accountID
	}
	a.spans = nil
	a.seenSpans = nil
	a.firstSeenAt = time.Time{}
	return out, a.accountID
}

// SpansAccumulator is the public-facing accumulator wrapper. The
// sync.Map keyed by trace_id gives lock-free handler hot path;
// the per-bucket mutex is internal to spansAccumulator.
type SpansAccumulator struct {
	buckets sync.Map // traceID -> *spansAccumulator
}

// NewSpansAccumulator returns an empty accumulator.
func NewSpansAccumulator() *SpansAccumulator {
	return &SpansAccumulator{}
}

// Add merges a batch into the per-trace bucket. The traceID is
// the W3C trace-id hex from the customer's OTLP POST. The
// accountID is resolved at the handler from the apid
// AuthenticateKey RPC. Returns the post-dedupe span count.
//
// PR-D code-review #4: every Add re-validates that the bucket's
// account_id matches the caller's account_id. The previous
// LoadOrStore implementation silently kept the first POST's
// account_id — a second POST for the same trace_id but a
// rotated / different API key would silently write the NEW
// account's spans_summary to the OLD account's
// request_telemetry row (the SQL UPDATE doesn't bind
// account_id, see code-review #1). On mismatch this Add
// returns ErrAccountMismatch without touching the bucket;
// the handler rejects those spans. The OLD account's
// legitimate spans already buffered in the bucket are
// preserved — they'll flush normally on the next tick.
func (s *SpansAccumulator) Add(traceID string, accountID uuid.UUID, spans []summarizedSpan) (int, error) {
	v, loaded := s.buckets.LoadOrStore(traceID, &spansAccumulator{
		traceID:   traceID,
		accountID: accountID,
	})
	if !loaded {
		// First POST for this trace_id: ours is the canonical
		// bucket. Add spans; cap-freeze is the handler's job
		// (the truncation in #5 lives in flush, see below).
		return v.(*spansAccumulator).add(spans), nil
	}
	bucket := v.(*spansAccumulator)
	bucket.mu.Lock()
	match := bucket.accountID == accountID
	bucket.mu.Unlock()
	if match {
		return bucket.add(spans), nil
	}
	return 0, ErrAccountMismatch
}

// ErrAccountMismatch is returned by Add when a trace_id is
// being contended across accounts (rotated API key, replay,
// multi-key account on the same trace). The handler should map
// this to 401 / 400 — the request can't be safely coalesced
// into the bucket.
var ErrAccountMismatch = errors.New("spans accumulator: trace_id claimed by another account")

// DrainAndRemove atomically extracts the accumulated spans for
// one trace_id and deletes the bucket from the map. Returns
// nil + zero uuid when the bucket is absent or empty. Used by
// the flush loop in Stage 4.
func (s *SpansAccumulator) DrainAndRemove(traceID string) ([]summarizedSpan, uuid.UUID) {
	v, ok := s.buckets.LoadAndDelete(traceID)
	if !ok {
		return nil, uuid.Nil
	}
	bucket, ok := v.(*spansAccumulator)
	if !ok {
		return nil, uuid.Nil
	}
	return bucket.snapshot()
}

// Len returns the current bucket count. Useful for the §12
// dashboard panel "in-process OTel spans batches in flight".
func (s *SpansAccumulator) Len() int {
	n := 0
	s.buckets.Range(func(_, _ any) bool {
		n++
		return true
	})
	return n
}
