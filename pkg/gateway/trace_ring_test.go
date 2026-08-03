// Tests for pkg/gateway trace_ring.go (issue #555 PR-2).
//
// The TraceRing is the single source of truth for "what does a
// customer's trace look like" when the operator hits GET
// /v1/traces/{trace_id}. Pinning the ring's contract here means a
// regression in the LRU eviction or the merge logic fails fast
// under -race, before the integration test (which only runs on
// EX44) catches it.
package gateway_test

import (
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/gateway"
)

func newRing(cap int) *gateway.TraceRing {
	return gateway.NewTraceRing(cap)
}

func sampleSpan(id, parent string, start time.Time) gateway.SpanRow {
	return gateway.SpanRow{
		TraceID:      "trace-1",
		SpanID:       id,
		ParentSpanID: parent,
		Name:         "test." + id,
		StartTime:    start,
		EndTime:      start.Add(time.Millisecond),
	}
}

func TestTraceRing_AddAndGet(t *testing.T) {
	r := newRing(10)
	now := time.Now()
	trace := &gateway.Trace{
		TraceID: "trace-1",
		Spans:   []gateway.SpanRow{sampleSpan("a", "", now)},
		Started: now,
	}
	if !r.Add(trace) {
		t.Fatal("Add returned false")
	}
	got, ok := r.Get("trace-1")
	if !ok {
		t.Fatal("Get on fresh trace returned false")
	}
	if got.TraceID != "trace-1" {
		t.Errorf("TraceID = %q, want trace-1", got.TraceID)
	}
	if len(got.Spans) != 1 {
		t.Errorf("Spans = %d, want 1", len(got.Spans))
	}
	// Mutating the returned slice must not mutate the ring's state.
	got.Spans[0].Name = "mutated"
	again, _ := r.Get("trace-1")
	if again.Spans[0].Name == "mutated" {
		t.Error("Get returned a shared slice (mutation leaked)")
	}
}

func TestTraceRing_MergeDedupesBySpanID(t *testing.T) {
	r := newRing(10)
	now := time.Now()
	trace := &gateway.Trace{
		TraceID: "trace-1",
		Spans:   []gateway.SpanRow{sampleSpan("a", "", now)},
	}
	r.Add(trace)
	// Re-add with same span + a new span.
	r.Add(&gateway.Trace{
		TraceID: "trace-1",
		Spans: []gateway.SpanRow{
			sampleSpan("a", "", now),  // duplicate
			sampleSpan("b", "a", now), // new
		},
	})
	got, _ := r.Get("trace-1")
	if len(got.Spans) != 2 {
		t.Errorf("merged Spans = %d, want 2 (dedup)", len(got.Spans))
	}
}

func TestTraceRing_LRUEvicts(t *testing.T) {
	r := newRing(2)
	now := time.Now()
	// Add t1, t2 (cap=2, no eviction). List order: t2, t1.
	r.Add(&gateway.Trace{TraceID: "t1", Spans: []gateway.SpanRow{sampleSpan("a", "", now)}, Started: now})
	r.Add(&gateway.Trace{TraceID: "t2", Spans: []gateway.SpanRow{sampleSpan("a", "", now)}, Started: now})
	// Promote t1 to MRU. List order: t1, t2.
	if _, ok := r.Get("t1"); !ok {
		t.Fatal("t1 missing after Add")
	}
	// Add t3 — cap exceeded, evict LRU (t2). List order: t3, t1.
	r.Add(&gateway.Trace{TraceID: "t3", Spans: []gateway.SpanRow{sampleSpan("a", "", now)}, Started: now})
	if _, ok := r.Get("t2"); ok {
		t.Error("LRU eviction skipped t2 (the LRU after Get-promote)")
	}
	if _, ok := r.Get("t1"); !ok {
		t.Error("t1 was incorrectly evicted despite Get-promote")
	}
	if _, ok := r.Get("t3"); !ok {
		t.Error("t3 was incorrectly evicted (it was just inserted)")
	}
}

func TestTraceRing_TimeEviction(t *testing.T) {
	r := newRing(10)
	// Walk the clock: record an "ancient" trace at t0, advance the
	// clock past the retention, then trigger a Put which runs the
	// sweep.
	var clock time.Time
	clock = time.Now()
	r.SetNowForTest(func() time.Time { return clock })
	r.Add(&gateway.Trace{
		TraceID: "ancient",
		Spans:   []gateway.SpanRow{},
		Started: clock,
	})
	// Advance the clock past 24h.
	clock = clock.Add(25 * time.Hour)
	// Trigger the sweep on the next Put.
	r.Add(&gateway.Trace{
		TraceID: "fresh",
		Spans:   []gateway.SpanRow{},
		Started: clock,
	})
	if _, ok := r.Get("ancient"); ok {
		t.Error("ancient trace was not evicted (>24h)")
	}
	if _, ok := r.Get("fresh"); !ok {
		t.Error("fresh trace was incorrectly evicted")
	}
}

func TestTraceRing_Concurrent(t *testing.T) {
	r := newRing(1000)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				trace := &gateway.Trace{
					TraceID: "trace-" + string(rune('a'+n%26)),
					Spans:   []gateway.SpanRow{},
				}
				r.Add(trace)
				_, _ = r.Get(trace.TraceID)
			}
		}(i)
	}
	wg.Wait()
	if r.Len() == 0 {
		t.Error("ring is empty after concurrent adds")
	}
}
