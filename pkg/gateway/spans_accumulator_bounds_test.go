package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func boundsTestTrace(i int) string { return fmt.Sprintf("%032x", i+1) }

func boundsTestSpan(id string) summarizedSpan {
	return summarizedSpan{SpanID: id, EndTimeUnixNano: 1, DurationNanos: 1}
}

func drainAll(t *testing.T, acc *SpansAccumulator, cfg FlushLoopConfig) {
	t.Helper()
	acc.drainOnce(context.Background(), cfg, map[spanBucketKey]*pendingEntry{})
}

func insertAll() FlushLoopConfig {
	return FlushLoopConfig{
		Log:        slog.Default(),
		MaxRetries: 3, // RunFlushLoop's default; drainOnce is called directly here
		WriteFn:    func(context.Context, string, []byte, string) (string, int64, error) { return "inserted", 0, nil },
	}
}

// A flushed trace leaves the accumulator once it has been empty for a tick.
// Buckets used to stay forever, so apid's guest span ingest, which refuses
// work once the accumulator holds 50,000 traces, stopped for every customer
// after 50,000 traced requests since it started.
func TestSpansAccumulatorRemovesFlushedTraces(t *testing.T) {
	acc := NewSpansAccumulator()
	account := uuid.New()
	for i := 0; i < 100; i++ {
		if _, err := acc.Add(boundsTestTrace(i), account, []summarizedSpan{boundsTestSpan("a")}); err != nil {
			t.Fatal(err)
		}
	}
	if got := acc.Len(); got != 100 {
		t.Fatalf("Len = %d before flushing, want 100", got)
	}
	drainAll(t, acc, insertAll()) // writes the spans; buckets are now empty
	drainAll(t, acc, insertAll()) // removes the empty buckets
	if got := acc.Len(); got != 0 {
		t.Fatalf("Len = %d after the flushed traces went idle, want 0", got)
	}
	if _, err := acc.Add(boundsTestTrace(0), account, []summarizedSpan{boundsTestSpan("b")}); err != nil {
		t.Fatalf("a removed trace cannot buffer again: %v", err)
	}
}

// The accumulator bounds the traces it buffers, per process and per account,
// so one OTLP POST of distinct trace IDs cannot grow the gateway without
// limit or take the whole buffer from other tenants.
func TestSpansAccumulatorBounds(t *testing.T) {
	acc := NewSpansAccumulatorWithBounds(4, 2)
	noisy, quiet := uuid.New(), uuid.New()
	for i := 0; i < 2; i++ {
		if _, err := acc.Add(boundsTestTrace(i), noisy, []summarizedSpan{boundsTestSpan("a")}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := acc.Add(boundsTestTrace(2), noisy, []summarizedSpan{boundsTestSpan("a")}); !errors.Is(err, ErrAccumulatorFull) {
		t.Fatalf("third trace for one account: err = %v, want ErrAccumulatorFull", err)
	}
	if _, err := acc.Add(boundsTestTrace(0), noisy, []summarizedSpan{boundsTestSpan("b")}); err != nil {
		t.Fatalf("an existing trace must keep accepting spans: %v", err)
	}
	for i := 3; i < 5; i++ {
		if _, err := acc.Add(boundsTestTrace(i), quiet, []summarizedSpan{boundsTestSpan("a")}); err != nil {
			t.Fatalf("another account refused under the per-account bound: %v", err)
		}
	}
	if _, err := acc.Add(boundsTestTrace(9), uuid.New(), []summarizedSpan{boundsTestSpan("a")}); !errors.Is(err, ErrAccumulatorFull) {
		t.Fatalf("process bound: err = %v, want ErrAccumulatorFull", err)
	}
	drainAll(t, acc, insertAll())
	drainAll(t, acc, insertAll())
	if _, err := acc.Add(boundsTestTrace(7), noisy, []summarizedSpan{boundsTestSpan("a")}); err != nil {
		t.Fatalf("bounds are not released when traces leave: %v", err)
	}
}

// The flush bounds a trace by the plan its producer resolved, not by the
// most permissive plan for everyone.
func TestSpansAccumulatorTruncatesByPlan(t *testing.T) {
	acc := NewSpansAccumulator()
	account := uuid.New()
	spans := make([]summarizedSpan, 10)
	for i := range spans {
		spans[i] = summarizedSpan{SpanID: fmt.Sprintf("s%d", i), EndTimeUnixNano: 1, DurationNanos: uint64(i)}
	}
	if _, err := acc.AddForPlan(boundsTestTrace(0), account, "hobby", spans); err != nil {
		t.Fatal(err)
	}
	var plans []string
	written := 0
	cfg := insertAll()
	cfg.MaxSpansPerTrace = func(plan string) int {
		plans = append(plans, plan)
		if plan == "hobby" {
			return 3
		}
		return 1000
	}
	cfg.WriteFn = func(_ context.Context, _ string, summary []byte, _ string) (string, int64, error) {
		written = len(summary)
		return "inserted", 0, nil
	}
	drainAll(t, acc, cfg)
	if len(plans) == 0 || plans[0] != "hobby" {
		t.Fatalf("cap asked for plans %v, want hobby", plans)
	}
	if written == 0 {
		t.Fatal("nothing written")
	}
}

// Adds racing the flush never lose spans: an Add that loaded a bucket the
// flush retired retries against the map.
func TestSpansAccumulatorConcurrentAddAndFlush(t *testing.T) {
	acc := NewSpansAccumulator()
	account := uuid.New()
	var mu sync.Mutex
	seen := map[string]int{}
	cfg := insertAll()
	cfg.WriteFn = func(_ context.Context, traceID string, summary []byte, _ string) (string, int64, error) {
		mu.Lock()
		seen[traceID]++
		mu.Unlock()
		return "inserted", 0, nil
	}
	const traces = 200
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < traces; i++ {
				if _, err := acc.Add(boundsTestTrace(i), account, []summarizedSpan{boundsTestSpan(fmt.Sprintf("w%d", w))}); err != nil {
					t.Error(err)
					return
				}
			}
		}(w)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		pending := map[spanBucketKey]*pendingEntry{}
		for i := 0; i < 50; i++ {
			acc.drainOnce(context.Background(), cfg, pending)
		}
	}()
	wg.Wait()
	<-done
	pending := map[spanBucketKey]*pendingEntry{}
	acc.drainOnce(context.Background(), cfg, pending)
	for i := 0; i < traces; i++ {
		if seen[boundsTestTrace(i)] == 0 {
			t.Fatalf("trace %d was never flushed", i)
		}
	}
}
