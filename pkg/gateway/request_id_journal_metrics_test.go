package gateway

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
)

// adr: 634 — request-ID journal writes are asynchronous and never fail a request (amends ADR-127).
func TestObserveRequestIDJournalWriteMetrics(t *testing.T) {
	m := NewMetrics()
	m.ObserveRequestIDJournalWrite(25*time.Millisecond, nil)
	m.ObserveRequestIDJournalWrite(2*time.Second, errors.New("apid unavailable"))

	if got := testutil.ToFloat64(m.requestIDJournalWrites.WithLabelValues("recorded")); got != 1 {
		t.Errorf("recorded writes = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.requestIDJournalWrites.WithLabelValues("failed")); got != 1 {
		t.Errorf("failed writes = %v, want 1", got)
	}

	metric := &dto.Metric{}
	if err := m.requestIDJournalWriteTime.Write(metric); err != nil {
		t.Fatalf("write histogram metric: %v", err)
	}
	if got := metric.GetHistogram().GetSampleCount(); got != 2 {
		t.Errorf("journal write duration samples = %d, want 2", got)
	}
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

// adr: 634 — the queue writes asynchronously, observes each write, and drops
// (counting) rather than blocking when its writers fall behind.
func TestRequestIDJournalQueueWritesAndObserves(t *testing.T) {
	m := NewMetrics()
	writes := make(chan RequestIDJournalRecord, 2)
	q := NewRequestIDJournalQueue(func(_ context.Context, r RequestIDJournalRecord) error {
		writes <- r
		if r.RequestID == "bad" {
			return errors.New("apid unavailable")
		}
		return nil
	}, m, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go q.Run(ctx)
	for _, id := range []string{"good", "bad"} {
		if err := q.Submit(context.Background(), RequestIDJournalRecord{RequestID: id}); err != nil {
			t.Fatalf("Submit(%s): %v", id, err)
		}
	}
	<-writes
	<-writes
	deadline := time.Now().Add(2 * time.Second)
	for testutil.ToFloat64(m.requestIDJournalWrites.WithLabelValues("recorded"))+testutil.ToFloat64(m.requestIDJournalWrites.WithLabelValues("failed")) < 2 {
		if time.Now().After(deadline) {
			t.Fatal("writes were not observed")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := testutil.ToFloat64(m.requestIDJournalWrites.WithLabelValues("failed")); got != 1 {
		t.Errorf("failed writes = %v, want 1", got)
	}
}

func TestRequestIDJournalQueueDropsWhenFull(t *testing.T) {
	m := NewMetrics()
	q := NewRequestIDJournalQueue(func(context.Context, RequestIDJournalRecord) error { return nil }, m, nil)
	for i := 0; i < requestIDJournalQueueSize; i++ {
		if err := q.Submit(context.Background(), RequestIDJournalRecord{}); err != nil {
			t.Fatalf("Submit %d: %v", i, err)
		}
	}
	if err := q.Submit(context.Background(), RequestIDJournalRecord{}); !errors.Is(err, errRequestIDJournalQueueFull) {
		t.Fatalf("Submit on a full queue = %v, want errRequestIDJournalQueueFull", err)
	}
	if got := testutil.ToFloat64(m.requestIDJournalWrites.WithLabelValues("dropped")); got != 1 {
		t.Errorf("dropped = %v, want 1", got)
	}
}

func TestObserveRequestIDJournalWriteMetrics_NilReceiverSafe(t *testing.T) {
	var m *Metrics
	m.ObserveRequestIDJournalWrite(time.Second, errors.New("ignored"))
}
