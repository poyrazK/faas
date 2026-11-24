package gateway

// adr: 957 — spans that arrive before their request row are retried and merged.

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
)

func noRowTestSpan(id string, durationNanos uint64) summarizedSpan {
	return summarizedSpan{TraceID: "0000000000000000000000000000000a", SpanID: id, StartTimeUnixNano: 1, EndTimeUnixNano: 1 + durationNanos, DurationNanos: durationNanos}
}

func TestDrainOnceRetriesNoRowAndMergesPendingSpans(t *testing.T) {
	const tid = "0000000000000000000000000000000a"
	acc := NewSpansAccumulator()
	account := uuid.New()
	var outcomes = []string{"no_row", "inserted"}
	var written [][]summarizedSpan
	cfg := FlushLoopConfig{
		Log:        slog.Default(),
		MaxRetries: 3,
		WriteFn: func(_ context.Context, traceID string, summaryJSON []byte, accountID string) (string, int64, error) {
			if traceID != tid || accountID != account.String() {
				t.Errorf("write for %s/%s", traceID, accountID)
			}
			var spans []summarizedSpan
			if err := json.Unmarshal(summaryJSON, &spans); err != nil {
				t.Fatal(err)
			}
			written = append(written, spans)
			outcome := outcomes[0]
			outcomes = outcomes[1:]
			return outcome, 0, nil
		},
	}
	pending := map[spanBucketKey]*pendingEntry{}

	if _, err := acc.Add(tid, account, []summarizedSpan{noRowTestSpan("a", 100)}); err != nil {
		t.Fatal(err)
	}
	acc.drainOnce(context.Background(), cfg, pending)
	if entry := pending[spanBucketKey{target: SpanTarget{AccountID: account}, traceID: tid}]; entry == nil || entry.noRows != 1 || entry.retries != 0 {
		t.Fatalf("no_row entry = %+v, want kept with one no_row flush and no failed retry", entry)
	}

	// Spans arriving while the trace is pending are merged, not substituted.
	if _, err := acc.Add(tid, account, []summarizedSpan{noRowTestSpan("b", 200)}); err != nil {
		t.Fatal(err)
	}
	acc.drainOnce(context.Background(), cfg, pending)
	if len(pending) != 0 {
		t.Fatalf("pending after insert = %+v", pending)
	}
	if len(written) != 2 || len(written[1]) != 2 {
		t.Fatalf("writes = %+v, want the second write to carry both spans", written)
	}
}

func TestDrainOnceDropsNoRowAfterMaxRetries(t *testing.T) {
	const tid = "0000000000000000000000000000000b"
	acc := NewSpansAccumulator()
	calls := 0
	cfg := FlushLoopConfig{
		Log:          slog.Default(),
		MaxRetries:   1,
		NoRowRetries: 2,
		WriteFn: func(context.Context, string, []byte, string) (string, int64, error) {
			calls++
			return "no_row", 0, nil
		},
	}
	pending := map[spanBucketKey]*pendingEntry{}
	if _, err := acc.Add(tid, uuid.New(), []summarizedSpan{noRowTestSpan("a", 100)}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		acc.drainOnce(context.Background(), cfg, pending)
	}
	if calls != 2 || len(pending) != 0 {
		t.Fatalf("calls=%d pending=%d, want 2 bounded no_row flushes then drop (MaxRetries does not cap no_row)", calls, len(pending))
	}
}

func TestDrainOncePendingMergeKeepsPerTraceCap(t *testing.T) {
	const tid = "0000000000000000000000000000000c"
	acc := NewSpansAccumulator()
	account := uuid.New()
	var lastWrite []summarizedSpan
	cfg := FlushLoopConfig{
		Log:              slog.Default(),
		MaxRetries:       5,
		MaxSpansPerTrace: func(string) int { return 2 },
		WriteFn: func(_ context.Context, _ string, summaryJSON []byte, _ string) (string, int64, error) {
			lastWrite = nil
			_ = json.Unmarshal(summaryJSON, &lastWrite)
			return "no_row", 0, nil
		},
	}
	pending := map[spanBucketKey]*pendingEntry{}
	_, _ = acc.Add(tid, account, []summarizedSpan{noRowTestSpan("slow", 900), noRowTestSpan("fast", 10)})
	acc.drainOnce(context.Background(), cfg, pending)
	_, _ = acc.Add(tid, account, []summarizedSpan{noRowTestSpan("slower", 1000)})
	acc.drainOnce(context.Background(), cfg, pending)
	if len(lastWrite) != 2 || lastWrite[0].SpanID != "slower" || lastWrite[1].SpanID != "slow" {
		t.Fatalf("merged write = %+v, want the two slowest spans", lastWrite)
	}
}

// Pending traces are capped; the oldest are dropped first and counted.
func TestDrainOnceCapsPendingTraces(t *testing.T) {
	acc := NewSpansAccumulator()
	account := uuid.New()
	var dropped int
	cfg := FlushLoopConfig{
		Log: slog.Default(), MaxRetries: 3, MaxPendingTraces: 2,
		OnPendingDropped: func(n int) { dropped += n },
		WriteFn: func(context.Context, string, []byte, string) (string, int64, error) {
			return "no_row", 0, nil
		},
	}
	pending := map[spanBucketKey]*pendingEntry{}
	old := spanBucketKey{target: SpanTarget{AccountID: account}, traceID: "0000000000000000000000000000000d"}
	pending[old] = &pendingEntry{traceID: old.traceID, target: old.target, spans: []summarizedSpan{noRowTestSpan("o", 1)}, queuedAt: time.Now().Add(-time.Minute)}
	for _, tid := range []string{"0000000000000000000000000000000e", "0000000000000000000000000000000f"} {
		if _, err := acc.Add(tid, account, []summarizedSpan{noRowTestSpan("a", 100)}); err != nil {
			t.Fatal(err)
		}
	}
	acc.drainOnce(context.Background(), cfg, pending)
	if len(pending) != 2 || pending[old] != nil || dropped != 1 {
		t.Fatalf("pending %d (oldest kept %v), dropped %d; want the 2 newest kept and 1 dropped", len(pending), pending[old] != nil, dropped)
	}
}
