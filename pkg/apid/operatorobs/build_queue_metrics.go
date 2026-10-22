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

// BuildQueueReader returns coherent count/oldest evidence and honors deadlines.
type BuildQueueReader = state.BuildQueueObservationStore

// BuildQueueMetrics publishes one coherent, process-local observation. Scrapes
// only copy memory; they never query the store. Multiple apids observe the same
// fleet queue independently, so their depths must never be summed.
type BuildQueueMetrics struct {
	depth, oldest, success, lastSuccess *prometheus.Desc
	mu                                  sync.RWMutex
	snapshot                            state.BuildQueueSnapshot
	ok                                  bool
	observed                            time.Time
	runOnce                             sync.Once
	now                                 func() time.Time
}

func NewBuildQueueMetrics(reg prometheus.Registerer) *BuildQueueMetrics {
	m := &BuildQueueMetrics{
		depth:       prometheus.NewDesc("faas_build_queue_depth", "Fleet builds in queued state. Omitted until a successful read and after failure or expiry; never sum across apids.", nil, nil),
		oldest:      prometheus.NewDesc("faas_build_queue_oldest_enqueued_timestamp_seconds", "Original enqueue time of the oldest currently queued build, in Unix seconds. Zero only for a successfully observed empty queue; omitted with unavailable depth.", nil, nil),
		success:     prometheus.NewDesc("apid_build_queue_collection_success", "Whether the latest completed queue read succeeded. Zero before the first successful read; check last-success freshness independently.", nil, nil),
		lastSuccess: prometheus.NewDesc("apid_build_queue_collection_last_success_timestamp_seconds", "Start time of the last successful queue read, in Unix seconds. Zero until success; frozen on failure.", nil, nil),
		now:         time.Now,
	}
	reg.MustRegister(m)
	return m
}

func (m *BuildQueueMetrics) Describe(ch chan<- *prometheus.Desc) {
	ch <- m.depth
	ch <- m.oldest
	ch <- m.success
	ch <- m.lastSuccess
}

func (m *BuildQueueMetrics) Collect(ch chan<- prometheus.Metric) {
	m.mu.RLock()
	snapshot, ok, observed := m.snapshot, m.ok, m.observed
	m.mu.RUnlock()
	success, timestamp := 0.0, 0.0
	if ok {
		success = 1
	}
	if !observed.IsZero() {
		timestamp = float64(observed.UnixNano()) / float64(time.Second)
	}
	ch <- prometheus.MustNewConstMetric(m.success, prometheus.GaugeValue, success)
	ch <- prometheus.MustNewConstMetric(m.lastSuccess, prometheus.GaugeValue, timestamp)
	age := m.now().Sub(observed)
	if ok && timestamp > 0 && age >= 0 && age <= api.ObsBuildQueueFreshnessMaxAge {
		oldest := 0.0
		if !snapshot.OldestEnqueuedAt.IsZero() {
			oldest = unixSeconds(snapshot.OldestEnqueuedAt)
		}
		ch <- prometheus.MustNewConstMetric(m.depth, prometheus.GaugeValue, float64(snapshot.Queued))
		ch <- prometheus.MustNewConstMetric(m.oldest, prometheus.GaugeValue, oldest)
	}
}

// Run polls immediately, then every interval, with one bounded read at a time.
// Only one runner may own this observation, even if wiring calls Run twice.
func (m *BuildQueueMetrics) Run(ctx context.Context, store BuildQueueReader, log *slog.Logger) {
	m.runOnce.Do(func() {
		ticker := time.NewTicker(api.ObsBuildQueueCollectionInterval)
		defer ticker.Stop()
		m.run(ctx, store, log, ticker.C)
	})
}

func (m *BuildQueueMetrics) run(ctx context.Context, store BuildQueueReader, log *slog.Logger, ticks <-chan time.Time) {
	for ctx.Err() == nil {
		if err := m.refresh(ctx, store); err != nil && ctx.Err() == nil && log != nil {
			log.Warn("apid: build queue observation failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticks:
		}
	}
}

func (m *BuildQueueMetrics) refresh(ctx context.Context, store BuildQueueReader) error {
	// Strip monotonic time so expiry agrees with the exported wall-clock
	// timestamp, including after a backward system-clock adjustment.
	started := m.now().UTC()
	readCtx, cancel := context.WithTimeout(ctx, api.ObsBuildQueueCollectionBudget)
	defer cancel()
	var snapshot state.BuildQueueSnapshot
	var err error
	if store == nil {
		err = errors.New("queued-build snapshot reader unavailable")
	} else {
		snapshot, err = store.QueuedBuildsSnapshot(readCtx)
	}
	if err == nil {
		err = readCtx.Err() // A late result cannot renew the observation.
	}
	if err == nil && (snapshot.Queued < 0 ||
		(snapshot.Queued == 0 && !snapshot.OldestEnqueuedAt.IsZero()) ||
		(snapshot.Queued > 0 && (snapshot.OldestEnqueuedAt.IsZero() || unixSeconds(snapshot.OldestEnqueuedAt) <= 0 || snapshot.OldestEnqueuedAt.After(m.now().UTC())))) {
		err = errors.New("inconsistent queued-build count and oldest timestamp")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ok = err == nil
	if err == nil {
		m.snapshot, m.observed = snapshot, started
	}
	return err
}

func unixSeconds(at time.Time) float64 {
	return float64(at.Unix()) + float64(at.Nanosecond())/float64(time.Second)
}
