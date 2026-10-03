package webhook

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

type retentionStoreStub struct {
	cutoff     time.Time
	limit      int
	pruned     int64
	bytes      int64
	err        error
	storageErr error
}

func (s *retentionStoreStub) PruneAppWebhookDeliveries(_ context.Context, cutoff time.Time, limit int) (int64, error) {
	s.cutoff, s.limit = cutoff, limit
	return s.pruned, s.err
}

func (s *retentionStoreStub) AppWebhookDeliveryStorageBytes(context.Context) (int64, error) {
	return s.bytes, s.storageErr
}

func TestRetentionWorkerBatchAndFailureSignal(t *testing.T) {
	now := time.Date(2026, 9, 28, 8, 0, 0, 0, time.UTC)
	store := &retentionStoreStub{pruned: 7, bytes: 1024}
	metrics := NewDeliveryHealthMetrics(prometheus.NewRegistry(), "schedd")
	worker := &RetentionWorker{
		Store: store, Metrics: metrics, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now: func() time.Time { return now },
	}
	worker.runOnce(context.Background())
	if want := now.Add(-DeliveryRetention); !store.cutoff.Equal(want) || store.limit != RetentionBatchSize {
		t.Fatalf("prune cutoff=%v limit=%d, want %v and %d", store.cutoff, store.limit, want, RetentionBatchSize)
	}
	if got := testutil.ToFloat64(metrics.prunedTotal); got != 7 {
		t.Fatalf("pruned = %v, want 7", got)
	}
	if got := testutil.ToFloat64(metrics.storageBytes); got != 1024 {
		t.Fatalf("storage = %v, want 1024", got)
	}
	store.err = errors.New("database unavailable")
	worker.runOnce(context.Background())
	if got := testutil.ToFloat64(metrics.retentionSuccess); got != 0 {
		t.Fatalf("retention success = %v after failure", got)
	}
	if got := testutil.ToFloat64(metrics.retentionFailures); got != 1 {
		t.Fatalf("retention failures = %v, want 1", got)
	}
	store.err = nil
	store.pruned = 3
	store.storageErr = errors.New("size unavailable")
	worker.runOnce(context.Background())
	if got := testutil.ToFloat64(metrics.prunedTotal); got != 10 {
		t.Fatalf("pruned count after storage failure = %v, want 10", got)
	}
	if got := testutil.ToFloat64(metrics.retentionFailures); got != 2 {
		t.Fatalf("retention failures = %v, want 2", got)
	}
}
