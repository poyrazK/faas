package imaged

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

type imagePreparationProgressMetrics struct {
	mu       sync.RWMutex
	snapshot state.ImagePreparationProgressSnapshot
	ok       bool
	observed time.Time
	runCtx   context.Context
	stopped  bool
	now      func() time.Time
	runOnce  sync.Once
	descs    []*prometheus.Desc
}

func newImagePreparationProgressMetrics(reg prometheus.Registerer) *imagePreparationProgressMetrics {
	m := &imagePreparationProgressMetrics{now: time.Now}
	for i, item := range []struct{ name, help string }{
		{"imaged_image_preparation_pending", "Active and recoverable checkpoints by durable phase in this node recovery scope, without a batch limit; empty owners can overlap, never sum replicas. Omitted when unknown."},
		{"imaged_image_preparation_phase_age_unknown", "Checkpoints without historical phase timing by phase; included in pending, not in the oldest known timestamp. Omitted when the snapshot is unknown."},
		{"imaged_image_preparation_oldest_phase_started_timestamp_seconds", "Oldest KNOWN committed phase start in Unix seconds, including time waiting to resume. Zero when no known ages exist, even if legacy checkpoints are pending; pair with pending and phase_age_unknown."},
		{"imaged_image_preparation_collection_success", "Whether the latest completed preparation-progress read succeeded; legacy unknown ages do not fail the read. Check freshness separately."},
		{"imaged_image_preparation_collection_last_success_timestamp_seconds", "Start of the last successful preparation-progress read in Unix seconds; frozen on failure and zero before success."},
		{"imaged_image_preparation_observation_enabled", "Whether the preparation observer is expected; zero on loop cancellation or return, without implying work drained."},
	} {
		var labels []string
		if i < 3 {
			labels = []string{"phase"}
		}
		m.descs = append(m.descs, prometheus.NewDesc(item.name, item.help, labels, nil))
	}
	reg.MustRegister(m)
	return m
}

func preparationPhases(s state.ImagePreparationProgressSnapshot) [3]struct {
	name  string
	value state.ImagePreparationPhaseSnapshot
} {
	return [3]struct {
		name  string
		value state.ImagePreparationPhaseSnapshot
	}{{"preparing", s.Preparing}, {"layer_published", s.LayerPublished}, {"scan_complete", s.ScanComplete}}
}

func (m *imagePreparationProgressMetrics) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range m.descs {
		ch <- d
	}
}

func (m *imagePreparationProgressMetrics) Collect(ch chan<- prometheus.Metric) {
	m.mu.RLock()
	s, ok, at := m.snapshot, m.ok, m.observed
	enabled := !m.stopped && (m.runCtx == nil || m.runCtx.Err() == nil)
	m.mu.RUnlock()
	bit := func(v bool) float64 {
		if v {
			return 1
		}
		return 0
	}
	for i, value := range []float64{bit(ok), handoffUnix(at), bit(enabled)} {
		ch <- prometheus.MustNewConstMetric(m.descs[i+3], prometheus.GaugeValue, value)
	}
	age := m.now().UTC().Sub(at)
	if !ok || handoffUnix(at) <= 0 || age < 0 || age > api.ObsImagePreparationFreshnessMaxAge {
		return
	}
	for _, phase := range preparationPhases(s) {
		for i, value := range []float64{float64(phase.value.Pending), float64(phase.value.UnknownAge), handoffUnix(phase.value.OldestPhaseStartedAt)} {
			ch <- prometheus.MustNewConstMetric(m.descs[i], prometheus.GaugeValue, value, phase.name)
		}
	}
}

func (m *imagePreparationProgressMetrics) refresh(ctx context.Context, store state.ImagePreparationProgressObservationStore, node string) error {
	started := m.now().UTC()
	readCtx, cancel := context.WithTimeout(ctx, api.ObsImagePreparationCollectionBudget)
	defer cancel()
	var s state.ImagePreparationProgressSnapshot
	var err error
	if store == nil {
		err = errors.New("image preparation progress reader unavailable")
	} else {
		s, err = store.ImagePreparationProgressSnapshot(readCtx, node)
	}
	if err == nil {
		err = readCtx.Err()
	}
	if err == nil {
		err = validatePreparationProgress(s, m.now().UTC())
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ok = err == nil
	if err == nil {
		m.snapshot, m.observed = s, started
	}
	return err
}

func validatePreparationProgress(s state.ImagePreparationProgressSnapshot, now time.Time) error {
	for _, phase := range preparationPhases(s) {
		p := phase.value
		known := p.Pending - p.UnknownAge
		if p.Pending < 0 || p.UnknownAge < 0 || p.UnknownAge > p.Pending ||
			!(known == 0 && p.OldestPhaseStartedAt.IsZero() || known > 0 && handoffUnix(p.OldestPhaseStartedAt) > 0 && !p.OldestPhaseStartedAt.After(now)) {
			return errors.New("inconsistent image preparation phase counts and timestamps")
		}
	}
	return nil
}

func (m *imagePreparationProgressMetrics) run(ctx context.Context, store state.ImagePreparationProgressObservationStore, node string, log *slog.Logger) {
	m.runOnce.Do(func() {
		ticker := time.NewTicker(api.ObsImagePreparationCollectionInterval)
		defer ticker.Stop()
		m.runWithTicks(ctx, store, node, log, ticker.C)
	})
}

func (m *imagePreparationProgressMetrics) runWithTicks(ctx context.Context, store state.ImagePreparationProgressObservationStore, node string, log *slog.Logger, ticks <-chan time.Time) {
	for ctx.Err() == nil {
		if err := m.refresh(ctx, store, node); err != nil && ctx.Err() == nil && log != nil {
			log.Warn("imaged: image preparation progress observation failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticks:
		}
	}
}

func (m *imagePreparationProgressMetrics) start(ctx context.Context) {
	m.mu.Lock()
	m.runCtx = ctx
	m.mu.Unlock()
}

func (m *imagePreparationProgressMetrics) stop() {
	m.mu.Lock()
	m.stopped = true
	m.mu.Unlock()
}
