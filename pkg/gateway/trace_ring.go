// Package gateway — trace_ring.go holds the in-memory trace storage
// for issue #555 PR-2. TraceRing keeps the last N completed span
// trees for a 24h rolling window so `GET /v1/traces/{trace_id}` can
// answer the operator's "why is my app slow" follow-up question
// without a backend.
//
// Design notes:
//
//   - One TraceRing per gatewayd-public instance. The 24h ring is the
//     smallest piece of state that satisfies issue #555 acceptance #3
//     ("GET /v1/traces/{trace_id} returns the trace tree in JSON,
//     last 24h"). Per-daemon means the metric is per-box; cross-box
//     fan-out is Gate-B work.
//
//   - LRU + time-based eviction. The cap is a hard safety net against
//     runaway traces; the time-based sweep ensures the 24h guarantee
//     even at low traffic. The sweep is O(N) on every Put to keep
//     the data structure simple — at 100k entries that's a few
//     hundred microseconds, well under the per-request budget.
//
//   - Modeled on pkg/gateway/routes.go RouteCache. The same LRU
//     shape (list.List front = MRU, byID map) so the code feels
//     familiar to anyone who has read the routing layer.
//
//   - The ring stores *Trace (a flat slice of spans with parent
//     pointers), not the OTel SDK's internal representation. The
//     public API is JSON-serialisable directly so the handler is
//     trivial.
//
// Memory: 100k traces × ~256 B JSON ≈ 25 MB. Reasonable for a
// one-box; the cap is configurable via FAAS_TRACE_RING_CAP env.
package gateway

import (
	"container/list"
	"sync"
	"time"
)

// DefaultTraceRingCap is the default cap when the env var is unset.
// 100k entries × ~256 B ≈ 25 MB resident. Sized to hold 24h of
// traces at one wake/second sustained, with burst headroom.
const DefaultTraceRingCap = 100_000

// DefaultTraceRetention is the TTL for entries. After this elapsed
// from a trace's last update, it is evicted on the next Put.
const DefaultTraceRetention = 24 * time.Hour

// Trace is the JSON-serialisable form of a single trace tree. The
// OTel SDK emits ReadOnlySpan values; the ring converts them on
// write so the public API never exposes upstream types (which lets
// us upgrade the SDK without breaking the JSON shape).
type Trace struct {
	TraceID  string    `json:"trace_id"`
	Spans    []SpanRow `json:"spans"`
	Started  time.Time `json:"started_at"`
	LastSeen time.Time `json:"last_seen_at"`
}

// SpanRow is a JSON-friendly span (matches the OTel attribute
// vocabulary used in the §12 dashboard). ParentSpanID is empty for
// root spans; TraceID is repeated for every span in the tree so a
// span can be rendered standalone.
type SpanRow struct {
	TraceID       string            `json:"trace_id"`
	SpanID        string            `json:"span_id"`
	ParentSpanID  string            `json:"parent_span_id,omitempty"`
	Name          string            `json:"name"`
	StartTime     time.Time         `json:"start_time"`
	EndTime       time.Time         `json:"end_time"`
	Attributes    map[string]string `json:"attributes,omitempty"`
	Status        string            `json:"status,omitempty"`
	StatusMessage string            `json:"status_message,omitempty"`
}

// TraceRing is the in-memory trace store. Safe for concurrent use;
// the LRU list is mutex-guarded, the time sweep runs under the same
// lock to keep the list consistent.
type TraceRing struct {
	mu        sync.Mutex
	cap       int
	retention time.Duration
	ll        *list.List
	byID      map[string]*list.Element
	// now is a clock function for tests. Production replaces it
	// with time.Now at construction.
	now func() time.Time
}

// NewTraceRing returns a ring holding up to cap entries with the
// default 24h retention. The cap is clamped to >= 1.
func NewTraceRing(cap int) *TraceRing {
	if cap < 1 {
		cap = DefaultTraceRingCap
	}
	return &TraceRing{
		cap:       cap,
		retention: DefaultTraceRetention,
		ll:        list.New(),
		byID:      map[string]*list.Element{},
		now:       time.Now,
	}
}

// Add inserts or updates a trace. New spans are merged into the
// existing trace (if any); the LastSeen clock is bumped so the
// 24h sweep does not evict a still-active trace.
//
// The returned boolean is true when the trace was added (new entry
// or appended span); false on a no-op (e.g. span already present).
func (r *TraceRing) Add(t *Trace) bool {
	if t == nil || t.TraceID == "" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.evictExpiredLocked()

	now := r.now()
	if el, ok := r.byID[t.TraceID]; ok {
		existing := el.Value.(*Trace)
		merged := mergeSpans(existing.Spans, t.Spans)
		existing.Spans = merged.Spans
		// Update Started to the earliest start, LastSeen to the
		// current clock. The caller-supplied LastSeen is ignored on
		// update — the ring's own clock is the eviction authority.
		if t.Started.Before(existing.Started) {
			existing.Started = t.Started
		}
		existing.LastSeen = now
		r.ll.MoveToFront(el)
		return true
	}

	trace := &Trace{
		TraceID:  t.TraceID,
		Spans:    append([]SpanRow(nil), t.Spans...),
		Started:  t.Started,
		LastSeen: now,
	}
	if trace.Started.IsZero() {
		trace.Started = now
	}
	el := r.ll.PushFront(trace)
	r.byID[t.TraceID] = el
	if r.ll.Len() > r.cap {
		r.evictLRULocked()
	}
	return true
}

// Get returns the trace by ID. The boolean is true on hit.
func (r *TraceRing) Get(traceID string) (*Trace, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	el, ok := r.byID[traceID]
	if !ok {
		return nil, false
	}
	t := el.Value.(*Trace)
	// Return a copy so the caller never mutates the ring's state.
	cp := &Trace{
		TraceID:  t.TraceID,
		Spans:    append([]SpanRow(nil), t.Spans...),
		Started:  t.Started,
		LastSeen: t.LastSeen,
	}
	r.ll.MoveToFront(el)
	return cp, true
}

// Len returns the current entry count.
func (r *TraceRing) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ll.Len()
}

// SetNowForTest replaces the clock function. Test-only helper; the
// clock is internal otherwise.
func (r *TraceRing) SetNowForTest(now func() time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.now = now
}

// mergeSpans appends new spans to existing, deduping by (span_id).
// Spans emitted from a single request are usually all new; the
// dedup is a safety net for retries that re-emit the same span.
func mergeSpans(existing, add []SpanRow) *Trace {
	seen := make(map[string]struct{}, len(existing))
	for _, s := range existing {
		seen[s.SpanID] = struct{}{}
	}
	out := make([]SpanRow, 0, len(existing)+len(add))
	out = append(out, existing...)
	for _, s := range add {
		if _, ok := seen[s.SpanID]; ok {
			continue
		}
		seen[s.SpanID] = struct{}{}
		out = append(out, s)
	}
	return &Trace{Spans: out}
}

// evictExpiredLocked removes entries whose LastSeen is older than
// retention. O(N) — acceptable at the cap.
func (r *TraceRing) evictExpiredLocked() {
	cutoff := r.now().Add(-r.retention)
	for el := r.ll.Back(); el != nil; el = r.ll.Back() {
		t := el.Value.(*Trace)
		if t.LastSeen.After(cutoff) {
			// list is in MRU order; once we hit a fresh entry,
			// nothing older is behind it.
			return
		}
		r.removeElementLocked(el)
	}
}

// evictLRULocked drops the LRU entry. Caller must hold mu.
func (r *TraceRing) evictLRULocked() {
	if el := r.ll.Back(); el != nil {
		r.removeElementLocked(el)
	}
}

func (r *TraceRing) removeElementLocked(el *list.Element) {
	r.ll.Remove(el)
	delete(r.byID, el.Value.(*Trace).TraceID)
}
