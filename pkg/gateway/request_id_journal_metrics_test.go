package gateway

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
)

func journalCounts(m *Metrics) map[string]float64 {
	out := map[string]float64{}
	for _, result := range []string{"recorded", "rejected", "failed", "dropped"} {
		out[result] = testutil.ToFloat64(m.requestIDJournalWrites.WithLabelValues(result))
	}
	return out
}

// adr: 1521 — each record counts once as recorded, rejected or failed; a
// batch error fails the whole batch; the duration is observed per batch.
func TestObserveRequestIDJournalBatchMetrics(t *testing.T) {
	for _, tc := range []struct {
		name    string
		records int
		result  RequestIDJournalBatchResult
		err     error
		want    map[string]float64
		samples uint64
	}{
		{"all recorded", 4, RequestIDJournalBatchResult{Recorded: 4}, nil,
			map[string]float64{"recorded": 4}, 1},
		{"mixed outcomes", 5, RequestIDJournalBatchResult{Recorded: 2, Rejected: 1, Failed: 2}, nil,
			map[string]float64{"recorded": 2, "rejected": 1, "failed": 2}, 1},
		{"batch error", 3, RequestIDJournalBatchResult{Recorded: 3}, errors.New("apid unavailable"),
			map[string]float64{"failed": 3}, 1},
		{"overcount is clamped", 2, RequestIDJournalBatchResult{Recorded: 5, Rejected: 5}, nil,
			map[string]float64{"recorded": 2}, 1},
		{"empty batch is ignored", 0, RequestIDJournalBatchResult{}, nil,
			map[string]float64{}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := NewMetrics()
			m.ObserveRequestIDJournalBatch(25*time.Millisecond, tc.records, tc.result, tc.err)
			for result, got := range journalCounts(m) {
				if got != tc.want[result] {
					t.Errorf("%s = %v, want %v", result, got, tc.want[result])
				}
			}
			metric := &dto.Metric{}
			if err := m.requestIDJournalWriteTime.Write(metric); err != nil {
				t.Fatalf("write histogram metric: %v", err)
			}
			if got := metric.GetHistogram().GetSampleCount(); got != tc.samples {
				t.Errorf("duration samples = %d, want %d", got, tc.samples)
			}
		})
	}
}

func TestObserveRequestIDJournalBatchMetrics_NilReceiverSafe(t *testing.T) {
	var m *Metrics
	m.ObserveRequestIDJournalBatch(time.Second, 3, RequestIDJournalBatchResult{}, errors.New("ignored"))
}

func TestRecordRequestIDJournalReturnsTheWriterError(t *testing.T) {
	h := &Handler{metrics: NewMetrics()}
	writeErr := errors.New("queue full")
	h.WithRequestIDJournalWriter(func(_ context.Context, _ RequestIDJournalRecord) error {
		return writeErr
	})
	app := App{ID: uuid.NewString(), AccountID: uuid.NewString()}
	if err := h.recordRequestIDJournal(context.Background(), app, "public-request-id", time.Now()); !errors.Is(err, writeErr) {
		t.Fatalf("recordRequestIDJournal error = %v, want %v", err, writeErr)
	}
}

// batchRecorder is a RequestIDJournalBatchWriter that keeps the size of every
// batch and answers with result.
type batchRecorder struct {
	mu     sync.Mutex
	sizes  []int
	result func(n int) RequestIDJournalBatchResult
	done   chan struct{}
	want   int
	total  int
}

func (b *batchRecorder) write(_ context.Context, batch []RequestIDJournalRecord) (RequestIDJournalBatchResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sizes = append(b.sizes, len(batch))
	b.total += len(batch)
	if b.total == b.want {
		close(b.done)
	}
	return b.result(len(batch)), nil
}

func runJournalQueue(t *testing.T, q *RequestIDJournalQueue) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		q.Run(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
}

// adr: 1521 — queued records leave in batches of at most maxBatch, and a
// partial batch is flushed once the linger window after its first record
// closes.
func TestRequestIDJournalQueueBatchesRecords(t *testing.T) {
	m := NewMetrics()
	rec := &batchRecorder{done: make(chan struct{}), want: 7, result: func(n int) RequestIDJournalBatchResult {
		return RequestIDJournalBatchResult{Recorded: n}
	}}
	q := NewRequestIDJournalQueue(rec.write, m, nil)
	q.maxBatch, q.linger = 3, 20*time.Millisecond
	for i := range 7 {
		if err := q.Submit(context.Background(), RequestIDJournalRecord{RequestID: string(rune('a' + i))}); err != nil {
			t.Fatalf("Submit %d: %v", i, err)
		}
	}
	runJournalQueue(t, q)
	select {
	case <-rec.done:
	case <-time.After(5 * time.Second):
		t.Fatal("records were not written")
	}
	rec.mu.Lock()
	sizes := slices.Sorted(slices.Values(rec.sizes))
	rec.mu.Unlock()
	if !slices.Equal(sizes, []int{1, 3, 3}) {
		t.Fatalf("batch sizes = %v, want [1 3 3]", sizes)
	}
	deadline := time.Now().Add(2 * time.Second)
	for journalCounts(m)["recorded"] < 7 {
		if time.Now().After(deadline) {
			t.Fatalf("recorded = %v, want 7", journalCounts(m)["recorded"])
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// adr: 1521 — the writer's per-record outcomes reach the metrics.
func TestRequestIDJournalQueueCountsBatchOutcomes(t *testing.T) {
	m := NewMetrics()
	rec := &batchRecorder{done: make(chan struct{}), want: 3, result: func(int) RequestIDJournalBatchResult {
		return RequestIDJournalBatchResult{Recorded: 1, Rejected: 1}
	}}
	q := NewRequestIDJournalQueue(rec.write, m, nil)
	q.linger = 20 * time.Millisecond
	for range 3 {
		if err := q.Submit(context.Background(), RequestIDJournalRecord{}); err != nil {
			t.Fatal(err)
		}
	}
	runJournalQueue(t, q)
	<-rec.done
	want := map[string]float64{"recorded": 1, "rejected": 1, "failed": 1}
	deadline := time.Now().Add(2 * time.Second)
	for {
		got := journalCounts(m)
		if got["recorded"] == want["recorded"] && got["rejected"] == want["rejected"] && got["failed"] == want["failed"] {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("journal counts = %v, want %v", got, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// adr: 634 — Submit never blocks: a full queue drops and counts the record.
func TestRequestIDJournalQueueDropsWhenFull(t *testing.T) {
	m := NewMetrics()
	q := NewRequestIDJournalQueue(func(context.Context, []RequestIDJournalRecord) (RequestIDJournalBatchResult, error) {
		return RequestIDJournalBatchResult{}, nil
	}, m, nil)
	for i := 0; i < requestIDJournalQueueSize; i++ {
		if err := q.Submit(context.Background(), RequestIDJournalRecord{}); err != nil {
			t.Fatalf("Submit %d: %v", i, err)
		}
	}
	if err := q.Submit(context.Background(), RequestIDJournalRecord{}); !errors.Is(err, errRequestIDJournalQueueFull) {
		t.Fatalf("Submit on a full queue = %v, want errRequestIDJournalQueueFull", err)
	}
	if got := journalCounts(m)["dropped"]; got != 1 {
		t.Errorf("dropped = %v, want 1", got)
	}
}
