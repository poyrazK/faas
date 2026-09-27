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

func TestRecordRequestIDJournalObservesWriteOutcome(t *testing.T) {
	m := NewMetrics()
	h := &Handler{metrics: m}
	writeErr := errors.New("apid unavailable")
	h.WithRequestIDJournalWriter(func(_ context.Context, _ RequestIDJournalRecord) error {
		return writeErr
	})
	app := App{ID: uuid.NewString(), AccountID: uuid.NewString()}

	if err := h.recordRequestIDJournal(context.Background(), app, "public-request-id", time.Now()); !errors.Is(err, writeErr) {
		t.Fatalf("recordRequestIDJournal error = %v, want %v", err, writeErr)
	}
	h.WithRequestIDJournalWriter(func(context.Context, RequestIDJournalRecord) error { return nil })
	if err := h.recordRequestIDJournal(context.Background(), app, "another-request-id", time.Now()); err != nil {
		t.Fatalf("recordRequestIDJournal success: %v", err)
	}

	if got := testutil.ToFloat64(m.requestIDJournalWrites.WithLabelValues("failed")); got != 1 {
		t.Errorf("failed writes = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.requestIDJournalWrites.WithLabelValues("recorded")); got != 1 {
		t.Errorf("recorded writes = %v, want 1", got)
	}
	metric := &dto.Metric{}
	if err := m.requestIDJournalWriteTime.Write(metric); err != nil {
		t.Fatalf("write histogram metric: %v", err)
	}
	if got := metric.GetHistogram().GetSampleCount(); got != 2 {
		t.Errorf("journal write duration samples = %d, want 2", got)
	}
}

func TestObserveRequestIDJournalWriteMetrics_NilReceiverSafe(t *testing.T) {
	var m *Metrics
	m.ObserveRequestIDJournalWrite(time.Second, errors.New("ignored"))
}
