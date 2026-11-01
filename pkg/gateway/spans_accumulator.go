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
	// plan is the account's plan as the producer resolved it, so the flush
	// bounds the trace to that plan's spans-per-trace cap ("" = unknown).
	plan string
	// retired is set, under mu, when the flush loop removes an empty bucket
	// from the map. An Add that loaded the bucket just before retries
	// against the map instead of writing into a bucket nobody will flush.
	retired bool
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
	// ErrorType is the failure class of an error span (error.type or the
	// first exception.type); sanitized and bounded on the debugger read path.
	ErrorType string `json:"error_type,omitempty"`
}

// add merges a new batch of spans into this accumulator. The
// within-window dedupe set drops any span seen before (keyed by
// (span_id, end_time_unix_nano)). Returns the post-dedupe span
// count (for the metric counter).
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
	return a.appendLocked(spans)
}

// appendLocked adds spans with mu held, deduplicating by span identity.
func (a *spansAccumulator) appendLocked(spans []summarizedSpan) int {
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
	out, accountID, _ := a.swapAndClearWithPlan()
	return out, accountID
}

// swapAndClearWithPlan is swapAndClear plus the plan the producer gave.
func (a *spansAccumulator) swapAndClearWithPlan() ([]summarizedSpan, uuid.UUID, string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := a.spans
	if out == nil {
		return nil, a.accountID, a.plan
	}
	a.spans = nil
	a.seenSpans = nil
	a.firstSeenAt = time.Time{}
	return out, a.accountID, a.plan
}
