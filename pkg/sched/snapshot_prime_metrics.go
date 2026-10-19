package sched

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

type snapshotPrimeMetrics struct {
	mu                 sync.RWMutex
	snapshot           state.SnapshotPrimeSnapshot
	ok                 bool
	observed           time.Time
	runCtx             context.Context
	stopped            bool
	now                func() time.Time
	runOnce            sync.Once
	descs              []*prometheus.Desc
	queries, decisions *prometheus.CounterVec
}

func newSnapshotPrimeMetrics(reg prometheus.Registerer) *snapshotPrimeMetrics {
	m := &snapshotPrimeMetrics{now: time.Now,
		queries:   prometheus.NewCounterVec(prometheus.CounterOpts{Name: "schedd_snapshot_prime_recovery_queries_total", Help: "Actual recovery list calls by success, error or canceled; excludes independent observation reads."}, []string{"outcome"}),
		decisions: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "schedd_snapshot_prime_recovery_candidates_total", Help: "Actual recovery candidate decisions. Dispatch_requested is a scheduling request, not proof of a prime or completed deployment."}, []string{"outcome"}),
	}
	for _, v := range []string{"success", "error", "canceled"} {
		m.queries.WithLabelValues(v)
	}
	for _, v := range []string{"not_found", "ineligible", "release_blocked", "grace", "not_owned", "snapshot_recorded", "active", "publication_pending", "dispatch_requested", "error", "canceled"} {
		m.decisions.WithLabelValues(v)
	}
	for i, item := range []struct{ name, help string }{
		{"schedd_snapshot_prime_pending", "Current snapshot_prepare attempts by durable class in this scheduler ownership scope, including work younger than recovery grace and beyond its page cap. Empty owners can overlap; never sum replicas."},
		{"schedd_snapshot_prime_age_unknown", "Pending subset without reliable attempt timing, by class. Unknown is not empty or zero wait."},
		{"schedd_snapshot_prime_oldest_attempt_timestamp_seconds", "Oldest known current-attempt boundary, no earlier than stage start, release success, runtime invalidation and known instance activity. Not continuous idle time. Zero when no known ages exist."},
		{"schedd_snapshot_prime_collection_success", "Whether the last completed durable handoff observation succeeded; check freshness separately."},
		{"schedd_snapshot_prime_collection_last_success_timestamp_seconds", "Start of the last successful durable handoff observation; frozen on failure and zero before success."},
		{"schedd_snapshot_prime_observation_enabled", "Whether observation is expected; zero on scheduler cancellation/return without implying work drained."},
	} {
		var labels []string
		if i < 3 {
			labels = []string{"class"}
		}
		m.descs = append(m.descs, prometheus.NewDesc(item.name, item.help, labels, nil))
	}
	reg.MustRegister(m)
	return m
}

func snapshotPrimeClasses(s state.SnapshotPrimeSnapshot) [6]struct {
	name  string
	value state.SnapshotPrimeCohort
} {
	var out [6]struct {
		name  string
		value state.SnapshotPrimeCohort
	}
	for i, p := range s {
		out[i].name = state.SnapshotPrimeClass(i).String()
		out[i].value = p
	}
	return out
}
func primeUnix(t time.Time) float64 {
	if t.IsZero() {
		return 0
	}
	return float64(t.Unix()) + float64(t.Nanosecond())/float64(time.Second)
}

func (m *snapshotPrimeMetrics) Describe(ch chan<- *prometheus.Desc) {
	m.queries.Describe(ch)
	m.decisions.Describe(ch)
	for _, d := range m.descs {
		ch <- d
	}
}

func (m *snapshotPrimeMetrics) Collect(ch chan<- prometheus.Metric) {
	defer m.queries.Collect(ch)
	defer m.decisions.Collect(ch)
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
	for i, value := range []float64{bit(ok), primeUnix(at), bit(enabled)} {
		ch <- prometheus.MustNewConstMetric(m.descs[i+3], prometheus.GaugeValue, value)
	}
	age := m.now().UTC().Sub(at)
	if !ok || primeUnix(at) <= 0 || age < 0 || age > api.ObsSnapshotPrimeFreshnessMaxAge {
		return
	}
	for _, phase := range snapshotPrimeClasses(s) {
		for i, value := range []float64{float64(phase.value.Pending), float64(phase.value.UnknownAge), primeUnix(phase.value.OldestAt)} {
			ch <- prometheus.MustNewConstMetric(m.descs[i], prometheus.GaugeValue, value, phase.name)
		}
	}
}

func (m *snapshotPrimeMetrics) refresh(ctx context.Context, store state.SnapshotPrimeObservationStore, node string) error {
	started := m.now().UTC()
	readCtx, cancel := context.WithTimeout(ctx, api.ObsSnapshotPrimeCollectionBudget)
	defer cancel()
	var s state.SnapshotPrimeSnapshot
	var err error
	if store == nil {
		err = errors.New("snapshot prime observation reader unavailable")
	} else {
		s, err = store.SnapshotPrimeSnapshot(readCtx, node)
	}
	if err == nil {
		err = readCtx.Err()
	}
	if err == nil {
		err = validateSnapshotPrime(s, m.now().UTC())
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ok = err == nil
	if err == nil {
		m.snapshot, m.observed = s, started
	}
	return err
}

func validateSnapshotPrime(s state.SnapshotPrimeSnapshot, now time.Time) error {
	for _, phase := range snapshotPrimeClasses(s) {
		p := phase.value
		if phase.name == "unknown" && p.Pending != p.UnknownAge {
			return errors.New("unknown prime class has known timing")
		}
		known := p.Pending - p.UnknownAge
		if p.Pending < 0 || p.UnknownAge < 0 || p.UnknownAge > p.Pending ||
			!(known == 0 && p.OldestAt.IsZero() || known > 0 && primeUnix(p.OldestAt) > 0 && !p.OldestAt.After(now)) {
			return errors.New("inconsistent snapshot prime counts and timestamps")
		}
	}
	return nil
}

func (m *snapshotPrimeMetrics) run(ctx context.Context, store state.SnapshotPrimeObservationStore, node string, log *slog.Logger) {
	m.runOnce.Do(func() {
		ticker := time.NewTicker(api.ObsSnapshotPrimeCollectionInterval)
		defer ticker.Stop()
		m.runWithTicks(ctx, store, node, log, ticker.C)
	})
}

func (m *snapshotPrimeMetrics) runWithTicks(ctx context.Context, store state.SnapshotPrimeObservationStore, node string, log *slog.Logger, ticks <-chan time.Time) {
	for ctx.Err() == nil {
		if err := m.refresh(ctx, store, node); err != nil && ctx.Err() == nil && log != nil {
			log.Warn("sched: snapshot prime handoff observation failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticks:
		}
	}
}

func (m *snapshotPrimeMetrics) start(ctx context.Context) {
	m.mu.Lock()
	m.runCtx = ctx
	m.mu.Unlock()
}

func (m *snapshotPrimeMetrics) stop() {
	m.mu.Lock()
	m.stopped = true
	m.mu.Unlock()
}

func (m *snapshotPrimeMetrics) observeQuery(ctx context.Context, err error) {
	if m == nil {
		return
	}
	v := "success"
	if err != nil {
		v = "error"
		if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
			v = "canceled"
		}
	}
	m.queries.WithLabelValues(v).Inc()
}
func (m *snapshotPrimeMetrics) observeCandidate(ctx context.Context, outcome string, err error) {
	if m == nil {
		return
	}
	if err != nil {
		outcome = "error"
		if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
			outcome = "canceled"
		}
	}
	m.decisions.WithLabelValues(outcome).Inc()
}
func (l *Loop) startSnapshotPrimeObservation(ctx context.Context) func() {
	if l.primeMetrics == nil || l.engine == nil {
		return func() {}
	}
	m := l.primeMetrics
	child, cancel := context.WithCancel(ctx)
	m.start(ctx)
	node := l.engine.ownerNodeID
	if node == "" {
		node = l.engine.defaultLocalNodeID
	}
	reader, _ := l.engine.store.(state.SnapshotPrimeObservationStore)
	go m.run(child, reader, node, l.log)
	return func() { m.stop(); cancel() }
}
