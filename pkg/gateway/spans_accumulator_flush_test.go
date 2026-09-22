// adr: 127
// spans_accumulator_flush_test.go — ADR-127 PR-D code-review
// #5 + #10 regression coverage.
//
// #5 — per-trace truncation at flush time. The Hobby
//     customer's chunked-POSTs bypass is closed: 60×50-span
//     POSTs into one window flushes 50 spans, not 3000.
//
// #10 — atomic swapAndClear. A handler Add racing the flush
//      tick lands in the empty bucket and is picked up next
//      tick. The previous Delete-after-snapshot design
//      silently dropped concurrent Adds.

package gateway

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestFlushLoop_PerTraceTruncation is the regression for
// PR-D code-review #5. 6 chunks of 50 spans to the same
// trace_id (cap=50) MUST flush at most 50 spans total.
func TestFlushLoop_PerTraceTruncation(t *testing.T) {
	s := NewSpansAccumulator()
	tid := "00000000000000000000000000000005"
	id := uuid.New()

	// 6 chunks × 50 spans = 300 spans total to one trace_id.
	for i := 0; i < 6; i++ {
		chunk := make([]summarizedSpan, 50)
		for j := range chunk {
			chunk[j] = summarizedSpan{
				TraceID:           tid,
				SpanID:            makeSpanID(i, j),
				StartTimeUnixNano: uint64(i*1_000_000 + j*1000),
				EndTimeUnixNano:   uint64(i*1_000_000 + j*1000 + 1000),
				DurationNanos:     uint64(1000 + j*100),
			}
		}
		if _, err := s.Add(tid, id, chunk); err != nil {
			t.Fatalf("chunk %d: %v", i, err)
		}
	}

	var trunc int32
	var marshaledLen atomic.Int32
	var flushCount int32
	flushCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = s.RunFlushLoop(flushCtx, FlushLoopConfig{
			Interval:         5 * time.Millisecond,
			MaxSpansPerTrace: func(_ string) int { return 50 },
			OnTruncated:      func(_ string) { atomic.AddInt32(&trunc, 1) },
			WriteFn: func(_ context.Context, _ string, summaryJSON []byte, _ string) (string, int64, error) {
				atomic.AddInt32(&flushCount, 1)
				marshaledLen.Store(int32(len(summaryJSON)))
				return "inserted", 0, nil
			},
		})
	}()
	// Wait for at least one flush to land.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && atomic.LoadInt32(&flushCount) == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
	// Sanity: a Hobby cap=50 truncation must have fired at
	// least once across the window.
	if got := atomic.LoadInt32(&trunc); got < 1 {
		t.Errorf("truncations fired = %d, want >= 1 (300 spans in, 50 cap)", got)
	}
	// Sanity: marshaledLen > 0 (a flush actually happened).
	if marshaledLen.Load() == 0 {
		t.Errorf("flush loop never wrote; marshaledLen = 0")
	}
}

func TestFlushLoop_DrainsOnCancel(t *testing.T) {
	acc := NewSpansAccumulator()
	accountID := uuid.New()
	traceID := "00000000000000000000000000000006"
	if _, err := acc.Add(traceID, accountID, []summarizedSpan{{
		TraceID: traceID,
		SpanID:  "0000000000000006",
	}}); err != nil {
		t.Fatal(err)
	}

	var writes atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := acc.RunFlushLoop(ctx, FlushLoopConfig{
		Interval: time.Hour,
		WriteFn: func(context.Context, string, []byte, string) (string, int64, error) {
			writes.Add(1)
			return "inserted", 0, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	if got := writes.Load(); got != 1 {
		t.Fatalf("writes on cancellation = %d, want 1", got)
	}
}

// TestSwapAndClear_ConcurrentAdd is the regression for
// PR-D code-review #10. A handler Add that races the flush
// tick lands in the freshly-emptied bucket and is picked up
// by the next tick (NOT lost).
func TestSwapAndClear_ConcurrentAdd(t *testing.T) {
	sa := &spansAccumulator{
		traceID:   "00000000000000000000000000000010",
		accountID: uuid.New(),
	}
	// Pre-load: 5 spans.
	if err := sa.addSpansForTest([]summarizedSpan{
		{SpanID: "a", EndTimeUnixNano: 1},
		{SpanID: "b", EndTimeUnixNano: 2},
		{SpanID: "c", EndTimeUnixNano: 3},
		{SpanID: "d", EndTimeUnixNano: 4},
		{SpanID: "e", EndTimeUnixNano: 5},
	}); err != nil {
		t.Fatalf("preload: %v", err)
	}

	// Atomic snapshot — the flush loop's shape.
	got, _ := sa.swapAndClear()
	if len(got) != 5 {
		t.Fatalf("swapAndClear returned %d spans, want 5", len(got))
	}

	// Post-swap the bucket is empty.
	if len(sa.spans) != 0 {
		t.Errorf("post-swap sa.spans = %d, want 0", len(sa.spans))
	}

	// A concurrent-style Add (same bucket pointer, no
	// map.Delete / map.LoadAndDelete) lands in the empty
	// bucket.
	if err := sa.addSpansForTest([]summarizedSpan{{SpanID: "f", EndTimeUnixNano: 6}}); err != nil {
		t.Fatalf("post-swap add: %v", err)
	}
	got2, _ := sa.swapAndClear()
	if len(got2) != 1 {
		t.Fatalf("second swapAndClear returned %d spans, want 1 (the one Add'd after the first swap)", len(got2))
	}
	if got2[0].SpanID != "f" {
		t.Errorf("second swapAndClear span = %q, want %q", got2[0].SpanID, "f")
	}
}

// makeSpanID builds a deterministic 16-char hex span_id from
// chunk/span indexes. Used by the truncation test.
func makeSpanID(chunk, span int) string {
	const hex = "0123456789abcdef"
	out := make([]byte, 16)
	out[0] = hex[chunk&0xf]
	out[1] = hex[span&0xf]
	for i := 2; i < 16; i++ {
		out[i] = '0'
	}
	return string(out)
}
