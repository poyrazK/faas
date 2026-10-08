package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

type observedHistoryStore struct {
	state.Store
	observed chan struct{}
}

func (s *observedHistoryStore) PruneExpiredManagedRealtimeChannelMessages(context.Context, int) (int64, error) {
	return 2, nil
}

func (s *observedHistoryStore) ObserveManagedRealtimeHistoryStorage(context.Context) (state.ManagedRealtimeHistoryStorageStats, error) {
	close(s.observed)
	return state.ManagedRealtimeHistoryStorageStats{HeadsRelationBytes: 8_192, MessagesRelationBytes: 16_384, UsageRelationBytes: 4_096}, nil
}

func TestManagedRealtimeHistoryMetricsKeepLastGoodStorageSample(t *testing.T) {
	registry := prometheus.NewRegistry()
	_ = newManagedRealtimeHistoryMetrics(registry, "apid")
	// A second server sharing the registry must reuse the same collectors.
	metrics := newManagedRealtimeHistoryMetrics(registry, "apid")
	at := time.Unix(1_800_000_000, 0)
	metrics.observeStorage(state.ManagedRealtimeHistoryStorageStats{
		HeadsRelationBytes: 12_288, MessagesRelationBytes: 32_768, UsageRelationBytes: 4_096,
	}, at)
	metrics.observePrune(3, nil)
	metrics.observePrune(0, errors.New("database unavailable"))
	metrics.observeStorageFailure()
	if got := testutil.ToFloat64(metrics.relationBytes.WithLabelValues("heads")); got != 12_288 {
		t.Fatalf("heads bytes = %v", got)
	}
	if got := testutil.ToFloat64(metrics.relationBytes.WithLabelValues("messages")); got != 32_768 {
		t.Fatalf("message bytes = %v", got)
	}
	if got := testutil.ToFloat64(metrics.relationBytes.WithLabelValues("usage")); got != 4_096 {
		t.Fatalf("usage bytes = %v", got)
	}
	if got := testutil.ToFloat64(metrics.sampleSuccess); got != 0 {
		t.Fatalf("sample success = %v after failed observation", got)
	}
	if got := testutil.ToFloat64(metrics.lastSample); got != float64(at.Unix()) {
		t.Fatalf("last successful sample = %v", got)
	}
	if got := testutil.ToFloat64(metrics.prunedMessages); got != 3 {
		t.Fatalf("pruned messages = %v", got)
	}
	if got := testutil.ToFloat64(metrics.pruneFailures); got != 1 {
		t.Fatalf("prune failures = %v", got)
	}
}

func TestManagedRealtimeHistoryReaperSamplesPhysicalStorage(t *testing.T) {
	metrics := newManagedRealtimeHistoryMetrics(prometheus.NewRegistry(), "apid")
	store := &observedHistoryStore{observed: make(chan struct{})}
	s := &server{store: store, realtimeHistoryMetrics: metrics}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.runManagedRealtimeHistoryReaper(ctx)
		close(done)
	}()
	select {
	case <-store.observed:
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("reaper did not sample storage")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("reaper did not stop")
	}
	if got := testutil.ToFloat64(metrics.relationBytes.WithLabelValues("messages")); got != 16_384 {
		t.Fatalf("sampled message allocation = %v", got)
	}
	if got := testutil.ToFloat64(metrics.prunedMessages); got != 2 {
		t.Fatalf("pruned messages = %v", got)
	}
}
