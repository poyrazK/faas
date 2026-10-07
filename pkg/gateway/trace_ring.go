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
//   - LRU + time-based eviction. Count, byte and span caps bound retention;
//     reads reject expired traces and writes sweep expired entries from
//     the LRU tail. The diagnostic 24h window is subject to those caps.
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
// Retention is bounded by entry count, accounted bytes and spans per trace
// (ADR-431). JSON size is not a useful estimate of resident Go maps/objects.
package gateway

import (
	"container/list"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// DefaultTraceRingCap is the default cap when the env var is unset.
// The byte budget can evict entries before this count is reached.
const DefaultTraceRingCap = api.TraceRingMaxTraces

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
	mu           sync.Mutex
	cap          int
	retention    time.Duration
	ll           *list.List
	byID         map[string]*list.Element
	maxBytes     int64
	bytes        int64
	evicted      uint64
	rejected     uint64
	droppedSpans uint64
	// now is a clock function for tests. Production replaces it
	// with time.Now at construction.
	now func() time.Time
}

type traceRingEntry struct {
	trace *Trace
	bytes int64
}

// TraceRingStats reports bounded diagnostic retention, not process RSS.
type TraceRingStats struct {
	Traces       int
	Bytes        int64
	Evicted      uint64
	Rejected     uint64
	DroppedSpans uint64
}

// NewTraceRing returns a count- and byte-bounded ring with 24h retention.
// Invalid entry counts use the default.
func NewTraceRing(cap int) *TraceRing {
	return NewTraceRingWithBudget(cap, api.TraceRingMaxBytes)
}

// NewTraceRingWithBudget retains at most maxBytes of conservatively accounted
// data. The count override cannot disable the byte or per-trace span bounds.
func NewTraceRingWithBudget(cap int, maxBytes int64) *TraceRing {
	if cap < 1 {
		cap = DefaultTraceRingCap
	}
	if maxBytes <= 0 {
		maxBytes = api.TraceRingMaxBytes
	}
	return &TraceRing{
		cap:       cap,
		retention: DefaultTraceRetention,
		ll:        list.New(),
		byID:      map[string]*list.Element{},
		maxBytes:  maxBytes,
		now:       time.Now,
	}
}

// Add inserts or updates a trace. New spans are merged into the
// existing trace (if any); the LastSeen clock is bumped so the
// 24h sweep does not evict a still-active trace.
//
// The returned boolean is true when the tree is accepted, false for invalid
// input or a tree that exceeds the byte budget. Duplicate spans are ignored.
func (r *TraceRing) Add(t *Trace) bool {
	if t == nil || t.TraceID == "" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.evictExpiredLocked()

	now := r.now()
	if el, ok := r.byID[t.TraceID]; ok {
		entry := el.Value.(*traceRingEntry)
		existing := entry.trace
		spans, dropped, fits := mergeSpans(t.TraceID, existing.Spans, t.Spans, r.maxBytes)
		r.droppedSpans += dropped
		if !fits {
			r.rejected++
			return false
		}
		bytes := traceRetainedBytes(t.TraceID, spans)
		r.bytes += bytes - entry.bytes
		entry.bytes = bytes
		existing.Spans = spans
		// Update Started to the earliest start, LastSeen to the
		// current clock. The caller-supplied LastSeen is ignored on
		// update — the ring's own clock is the eviction authority.
		if t.Started.Before(existing.Started) {
			existing.Started = t.Started
		}
		existing.LastSeen = now
		r.ll.MoveToFront(el)
		r.evictOverBudgetLocked()
		return true
	}

	spans, dropped, fits := mergeSpans(t.TraceID, nil, t.Spans, r.maxBytes)
	r.droppedSpans += dropped
	if !fits {
		r.rejected++
		return false
	}
	bytes := traceRetainedBytes(t.TraceID, spans)
	trace := &Trace{
		TraceID:  strings.Clone(t.TraceID),
		Spans:    spans,
		Started:  t.Started,
		LastSeen: now,
	}
	if trace.Started.IsZero() {
		trace.Started = now
	}
	el := r.ll.PushFront(&traceRingEntry{trace: trace, bytes: bytes})
	r.byID[trace.TraceID] = el
	r.bytes += bytes
	r.evictOverBudgetLocked()
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
	t := el.Value.(*traceRingEntry).trace
	if !t.LastSeen.After(r.now().Add(-r.retention)) {
		r.removeElementLocked(el)
		return nil, false
	}
	// Return a copy so the caller never mutates the ring's state.
	cp := &Trace{
		TraceID:  t.TraceID,
		Spans:    cloneSpanRows(t.Spans),
		Started:  t.Started,
		LastSeen: t.LastSeen,
	}
	r.ll.MoveToFront(el)
	return cp, true
}

// Stats returns an atomic snapshot of retention and loss counters.
func (r *TraceRing) Stats() TraceRingStats {
	r.mu.Lock()
	defer r.mu.Unlock()
	return TraceRingStats{Traces: r.ll.Len(), Bytes: r.bytes, Evicted: r.evicted,
		Rejected: r.rejected, DroppedSpans: r.droppedSpans}
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
func mergeSpans(id string, existing, add []SpanRow, maxBytes int64) ([]SpanRow, uint64, bool) {
	bytes := traceRetainedBytes(id, existing)
	if bytes > maxBytes {
		return nil, 0, false
	}
	seen := make(map[string]struct{}, len(existing))
	for _, s := range existing {
		seen[s.SpanID] = struct{}{}
	}
	out := make([]SpanRow, 0, min(api.TraceRingMaxSpansPerTrace, len(existing)+len(add)))
	out = append(out, existing...)
	var dropped uint64
	for _, s := range add {
		if _, ok := seen[s.SpanID]; ok {
			continue
		}
		if len(out) >= api.TraceRingMaxSpansPerTrace {
			dropped++
			continue
		}
		bytes += spanRetainedBytes(s)
		if bytes > maxBytes {
			return nil, dropped, false
		}
		seen[s.SpanID] = struct{}{}
		out = append(out, cloneSpanRow(s))
	}
	return out, dropped, true
}

// Account string backing bytes and conservative object, map bucket, list and
// index overhead. Clone strings/maps on admission so neither borrowed large
// string buffers nor later caller mutations can bypass this accounting.
func traceRetainedBytes(id string, spans []SpanRow) int64 {
	n := int64(256 + len(id))
	for _, s := range spans {
		n += spanRetainedBytes(s)
	}
	return n
}

func spanRetainedBytes(s SpanRow) int64 {
	n := int64(256 + len(s.TraceID) + len(s.SpanID) + len(s.ParentSpanID) +
		len(s.Name) + len(s.Status) + len(s.StatusMessage))
	for k, v := range s.Attributes {
		n += int64(128 + len(k) + len(v))
	}
	return n
}

func cloneSpanRow(s SpanRow) SpanRow {
	s.TraceID, s.SpanID = strings.Clone(s.TraceID), strings.Clone(s.SpanID)
	s.ParentSpanID, s.Name = strings.Clone(s.ParentSpanID), strings.Clone(s.Name)
	s.Status, s.StatusMessage = strings.Clone(s.Status), strings.Clone(s.StatusMessage)
	if s.Attributes != nil {
		attrs := make(map[string]string, len(s.Attributes))
		for k, v := range s.Attributes {
			attrs[strings.Clone(k)] = strings.Clone(v)
		}
		s.Attributes = attrs
	}
	return s
}

func cloneSpanRows(spans []SpanRow) []SpanRow {
	out := make([]SpanRow, len(spans))
	for i, s := range spans {
		out[i] = cloneSpanRow(s)
	}
	return out
}

func (r *TraceRing) evictOverBudgetLocked() {
	for r.ll.Len() > r.cap || r.bytes > r.maxBytes {
		r.evictLRULocked()
	}
}

// evictExpiredLocked removes entries whose LastSeen is older than
// retention. O(N) — acceptable at the cap.
func (r *TraceRing) evictExpiredLocked() {
	cutoff := r.now().Add(-r.retention)
	for el := r.ll.Back(); el != nil; el = r.ll.Back() {
		t := el.Value.(*traceRingEntry).trace
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
	entry := el.Value.(*traceRingEntry)
	r.bytes -= entry.bytes
	r.evicted++
	r.ll.Remove(el)
	delete(r.byID, entry.trace.TraceID)
}
