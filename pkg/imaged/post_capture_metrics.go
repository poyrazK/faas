package imaged

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apihostingreceipt"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
)

type postCaptureMetrics struct {
	mu       sync.RWMutex
	snapshot state.PostCaptureSnapshot
	ok       bool
	observed time.Time
	runCtx   context.Context
	stopped  bool
	now      func() time.Time
	runOnce  sync.Once
	descs    []*prometheus.Desc
	attempts *prometheus.CounterVec
}

func newPostCaptureMetrics(reg prometheus.Registerer) *postCaptureMetrics {
	m := &postCaptureMetrics{now: time.Now, attempts: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "imaged_deployment_activation_attempts_total", Help: "Handler returns by input, last reached phase and outcome; evidence success may be a warm publication or replay, not a new activation. Errors include app/policy verdicts, not just infrastructure faults."}, []string{"input", "phase", "outcome"})}
	for _, input := range []string{"snapshot", "ready"} {
		for _, phase := range []string{"evidence", "dependency", "verification", "promotion", "finalize"} {
			for _, outcome := range []string{"success", "error", "deferred", "held", "canceled"} {
				m.attempts.WithLabelValues(input, phase, outcome)
			}
		}
	}
	for i, item := range []struct{ name, help string }{
		{"imaged_post_capture_pending", "Current durable snapshot-flow evidence by class across the shared fleet, without a deployment batch cap. Compare replicas, never sum. Omitted when unknown."},
		{"imaged_post_capture_age_unknown", "Pending subset lacking reliable capture/publication/receipt timing. Unknown is not empty or zero wait."},
		{"imaged_post_capture_oldest_boundary_timestamp_seconds", "Oldest known capture parked time, init publication, or retained gate/verification boundary by class. Not continuous class residence. Zero when no known ages exist."},
		{"imaged_post_capture_collection_success", "Whether the last completed post-capture observation succeeded; pair with freshness."},
		{"imaged_post_capture_collection_last_success_timestamp_seconds", "Start of the last successful post-capture read; frozen on failure and zero before success."},
		{"imaged_post_capture_observation_enabled", "Whether post-capture observation is expected; zero on cancellation/return without implying work drained."},
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

func postCaptureClasses(s state.PostCaptureSnapshot) [8]struct {
	name  string
	value state.SnapshotPrimeCohort
} {
	var out [8]struct {
		name  string
		value state.SnapshotPrimeCohort
	}
	for i, p := range s {
		out[i].name = state.PostCaptureClass(i).String()
		out[i].value = p
	}
	return out
}

func (m *postCaptureMetrics) Describe(ch chan<- *prometheus.Desc) {
	m.attempts.Describe(ch)
	for _, d := range m.descs {
		ch <- d
	}
}

func (m *postCaptureMetrics) Collect(ch chan<- prometheus.Metric) {
	defer m.attempts.Collect(ch)
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
	if !ok || handoffUnix(at) <= 0 || age < 0 || age > api.ObsPostCaptureFreshnessMaxAge {
		return
	}
	for _, phase := range postCaptureClasses(s) {
		for i, value := range []float64{float64(phase.value.Pending), float64(phase.value.UnknownAge), handoffUnix(phase.value.OldestAt)} {
			ch <- prometheus.MustNewConstMetric(m.descs[i], prometheus.GaugeValue, value, phase.name)
		}
	}
}

func (m *postCaptureMetrics) refresh(ctx context.Context, store state.PostCaptureObservationStore) error {
	started := m.now().UTC()
	readCtx, cancel := context.WithTimeout(ctx, api.ObsPostCaptureCollectionBudget)
	defer cancel()
	var s state.PostCaptureSnapshot
	var err error
	if store == nil {
		err = errors.New("post-capture observation reader unavailable")
	} else {
		s, err = store.PostCaptureSnapshot(readCtx)
	}
	if err == nil {
		err = readCtx.Err()
	}
	if err == nil {
		err = validatePostCapture(s, m.now().UTC())
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ok = err == nil
	if err == nil {
		m.snapshot, m.observed = s, started
	}
	return err
}

func validatePostCapture(s state.PostCaptureSnapshot, now time.Time) error {
	for _, phase := range postCaptureClasses(s) {
		p := phase.value
		if phase.name == "unknown" && p.Pending != p.UnknownAge {
			return errors.New("unknown post-capture class has known timing")
		}
		known := p.Pending - p.UnknownAge
		if p.Pending < 0 || p.UnknownAge < 0 || p.UnknownAge > p.Pending ||
			!(known == 0 && p.OldestAt.IsZero() || known > 0 && handoffUnix(p.OldestAt) > 0 && !p.OldestAt.After(now)) {
			return errors.New("inconsistent post-capture counts and timestamps")
		}
	}
	return nil
}

func (m *postCaptureMetrics) run(ctx context.Context, store state.PostCaptureObservationStore, log *slog.Logger) {
	m.runOnce.Do(func() {
		ticker := time.NewTicker(api.ObsPostCaptureCollectionInterval)
		defer ticker.Stop()
		m.runWithTicks(ctx, store, log, ticker.C)
	})
}

func (m *postCaptureMetrics) runWithTicks(ctx context.Context, store state.PostCaptureObservationStore, log *slog.Logger, ticks <-chan time.Time) {
	for ctx.Err() == nil {
		if err := m.refresh(ctx, store); err != nil && ctx.Err() == nil && log != nil {
			log.Warn("imaged: post-capture observation failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticks:
		}
	}
}

func (m *postCaptureMetrics) start(ctx context.Context) {
	m.mu.Lock()
	m.runCtx = ctx
	m.mu.Unlock()
}

func (m *postCaptureMetrics) stop() {
	m.mu.Lock()
	m.stopped = true
	m.mu.Unlock()
}

func (m *postCaptureMetrics) observeAttempt(ctx context.Context, input, phase string, err error) {
	if m == nil {
		return
	}
	outcome := "success"
	if err != nil {
		outcome = "error"
		var deferred *db.DeferredNotificationError
		if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
			outcome = "canceled"
		} else if errors.Is(err, state.ErrMCPTaskCompatibilityGate) {
			outcome = "held"
		} else if errors.Is(err, state.ErrHostingVerificationDeferred) || errors.As(err, &deferred) || apihostingreceipt.VerificationRecoveryCode(err) != "" {
			outcome = "deferred"
		}
	}
	m.attempts.WithLabelValues(input, phase, outcome).Inc()
}
