package operatorobs

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
)

// BuildRecoveryMetrics publishes a coherent fleet snapshot from a sequential,
// deadline-bounded reader. Scrapes only copy memory; replicas must not be summed.
type BuildRecoveryMetrics struct {
	running, started, pending, created, retried, success, lastSuccess *prometheus.Desc
	mu                                                                sync.RWMutex
	snapshot                                                          state.BuildRecoverySnapshot
	ok                                                                bool
	observed                                                          time.Time
	runOnce                                                           sync.Once
	now                                                               func() time.Time
}

func NewBuildRecoveryMetrics(reg prometheus.Registerer) *BuildRecoveryMetrics {
	m := &BuildRecoveryMetrics{
		running:     prometheus.NewDesc("faas_build_running_count", "Fleet builds currently running. Omitted on unavailable observations; never sum across apids.", nil, nil),
		started:     prometheus.NewDesc("faas_build_running_oldest_started_timestamp_seconds", "Oldest running claim start in Unix seconds. Zero only for a successfully observed empty set.", nil, nil),
		pending:     prometheus.NewDesc("faas_build_vm_cleanup_pending", "Fleet outstanding durable builder VM cleanup obligations, including claimed and retrying rows.", nil, nil),
		created:     prometheus.NewDesc("faas_build_vm_cleanup_oldest_created_timestamp_seconds", "Creation time of the oldest outstanding cleanup obligation in Unix seconds; preserved across retries. Zero only when empty.", nil, nil),
		retried:     prometheus.NewDesc("faas_build_vm_cleanup_retry_pending", "Outstanding cleanup obligations with a recorded failed attempt, including subsequent live claims.", nil, nil),
		success:     prometheus.NewDesc("apid_build_recovery_collection_success", "Whether the latest completed recovery snapshot read succeeded. Check last-success freshness independently.", nil, nil),
		lastSuccess: prometheus.NewDesc("apid_build_recovery_collection_last_success_timestamp_seconds", "Start of the last successful recovery snapshot read in Unix seconds; zero before success and frozen on failure.", nil, nil),
		now:         time.Now,
	}
	reg.MustRegister(m)
	return m
}

func (m *BuildRecoveryMetrics) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range []*prometheus.Desc{m.running, m.started, m.pending, m.created, m.retried, m.success, m.lastSuccess} {
		ch <- desc
	}
}

func (m *BuildRecoveryMetrics) Collect(ch chan<- prometheus.Metric) {
	m.mu.RLock()
	snapshot, ok, observed := m.snapshot, m.ok, m.observed
	m.mu.RUnlock()
	success, timestamp := 0.0, 0.0
	if ok {
		success = 1
	}
	if !observed.IsZero() {
		timestamp = unixSeconds(observed)
	}
	ch <- prometheus.MustNewConstMetric(m.success, prometheus.GaugeValue, success)
	ch <- prometheus.MustNewConstMetric(m.lastSuccess, prometheus.GaugeValue, timestamp)
	age := m.now().UTC().Sub(observed)
	if !ok || timestamp <= 0 || age < 0 || age > api.ObsBuildRecoveryFreshnessMaxAge {
		return
	}
	started, created := 0.0, 0.0
	if !snapshot.OldestStartedAt.IsZero() {
		started = unixSeconds(snapshot.OldestStartedAt)
	}
	if !snapshot.OldestCleanupCreatedAt.IsZero() {
		created = unixSeconds(snapshot.OldestCleanupCreatedAt)
	}
	for _, item := range []struct {
		desc  *prometheus.Desc
		value float64
	}{
		{m.running, float64(snapshot.Running)}, {m.started, started}, {m.pending, float64(snapshot.PendingCleanup)},
		{m.created, created}, {m.retried, float64(snapshot.RetriedCleanup)},
	} {
		ch <- prometheus.MustNewConstMetric(item.desc, prometheus.GaugeValue, item.value)
	}
}

func (m *BuildRecoveryMetrics) Run(ctx context.Context, store state.BuildRecoveryObservationStore, log *slog.Logger) {
	m.runOnce.Do(func() {
		ticker := time.NewTicker(api.ObsBuildRecoveryCollectionInterval)
		defer ticker.Stop()
		m.run(ctx, store, log, ticker.C)
	})
}

func (m *BuildRecoveryMetrics) run(ctx context.Context, store state.BuildRecoveryObservationStore, log *slog.Logger, ticks <-chan time.Time) {
	for ctx.Err() == nil {
		if err := m.refresh(ctx, store); err != nil && ctx.Err() == nil && log != nil {
			log.Warn("apid: build recovery observation failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticks:
		}
	}
}

func (m *BuildRecoveryMetrics) refresh(ctx context.Context, store state.BuildRecoveryObservationStore) error {
	started := m.now().UTC()
	readCtx, cancel := context.WithTimeout(ctx, api.ObsBuildRecoveryCollectionBudget)
	defer cancel()
	var snapshot state.BuildRecoverySnapshot
	var err error
	if store == nil {
		err = errors.New("build recovery snapshot reader unavailable")
	} else {
		snapshot, err = store.BuildRecoverySnapshot(readCtx)
	}
	if err == nil {
		err = readCtx.Err()
	}
	if err == nil && !validRecoverySnapshot(snapshot, m.now().UTC()) {
		err = errors.New("inconsistent build recovery counts and timestamps")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ok = err == nil
	if err == nil {
		m.snapshot, m.observed = snapshot, started
	}
	return err
}

func validRecoverySnapshot(s state.BuildRecoverySnapshot, now time.Time) bool {
	validPair := func(count int64, oldest time.Time) bool {
		return count == 0 && oldest.IsZero() || count > 0 && !oldest.IsZero() && unixSeconds(oldest) > 0 && !oldest.After(now)
	}
	return validPair(s.Running, s.OldestStartedAt) && validPair(s.PendingCleanup, s.OldestCleanupCreatedAt) && s.RetriedCleanup >= 0 && s.RetriedCleanup <= s.PendingCleanup
}
