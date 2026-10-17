package imaged

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/buildexport"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
)

type buildImageHandoffMetrics struct {
	mu                           sync.RWMutex
	snapshot                     state.BuildImageHandoffSnapshot
	ok                           bool
	observed, attempt, completed time.Time
	inflight, stopped            bool
	runCtx                       context.Context
	now                          func() time.Time
	runOnce                      sync.Once
	descs                        []*prometheus.Desc
	queries, work, commits       *prometheus.CounterVec
}

func newBuildImageHandoffMetrics(reg prometheus.Registerer) *buildImageHandoffMetrics {
	m := &buildImageHandoffMetrics{now: time.Now,
		queries: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "imaged_build_handoff_recovery_queries_total", Help: "Recovery list calls by source (resume or awaiting) and outcome (success, error, canceled); does not count observation reads."}, []string{"source", "outcome"}),
		work:    prometheus.NewCounterVec(prometheus.CounterOpts{Name: "imaged_build_handoff_recovery_work_total", Help: "Recovery handler returns by source and bounded outcome. Returned does not establish a durable transition or successful deployment."}, []string{"source", "outcome"}),
		commits: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "imaged_image_preparation_commits_total", Help: "Successful fenced checkpoint writes by transition; claimed includes resumed claims and is not unique builds. Handed_off is not deployment readiness."}, []string{"transition"}),
	}
	for _, item := range []struct{ name, help string }{
		{"imaged_build_handoff_pending", "Eligible succeeded build rows awaiting an image-preparation checkpoint in this imaged recovery scope; legacy empty owners can overlap, never sum replicas. Omitted when unknown."},
		{"imaged_build_handoff_oldest_finished_timestamp_seconds", "Oldest eligible succeeded build finish time in Unix seconds; zero only for an observed empty set. Checkpointed active/resumable preparations are excluded."},
		{"imaged_build_handoff_collection_success", "Whether the latest completed handoff snapshot read succeeded; freshness is checked independently."},
		{"imaged_build_handoff_collection_last_success_timestamp_seconds", "Start of the last successful handoff snapshot read in Unix seconds; frozen on failure and zero before success."},
		{"imaged_build_handoff_recovery_last_attempt_timestamp_seconds", "Start of the latest recovery pass, which can execute up to two serial batches of image work; zero before its first attempt."},
		{"imaged_build_handoff_recovery_last_completed_timestamp_seconds", "End of the latest recovery pass, including unsuccessful/no-op passes; zero before its first return."},
		{"imaged_build_handoff_recovery_inflight", "Whether a recovery pass is executing; synchronous image work can legitimately keep a pass active."},
		{"imaged_build_handoff_recovery_enabled", "Whether recovery is expected; zero after the loop context is canceled or the loop returns. Does not prove work drained."},
	} {
		m.descs = append(m.descs, prometheus.NewDesc(item.name, item.help, nil, nil))
	}
	for _, source := range []string{"resume", "awaiting"} {
		for _, outcome := range []string{"success", "error", "canceled"} {
			m.queries.WithLabelValues(source, outcome)
		}
		for _, outcome := range []string{"returned", "retryable", "deferred", "not_owned", "error", "canceled"} {
			m.work.WithLabelValues(source, outcome)
		}
	}
	for _, transition := range []string{"claimed", "layer_published", "scan_complete", "handed_off"} {
		m.commits.WithLabelValues(transition)
	}
	reg.MustRegister(m)
	return m
}

func (m *buildImageHandoffMetrics) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range m.descs {
		ch <- d
	}
	m.queries.Describe(ch)
	m.work.Describe(ch)
	m.commits.Describe(ch)
}

func handoffUnix(t time.Time) float64 {
	if t.IsZero() {
		return 0
	}
	return float64(t.Unix()) + float64(t.Nanosecond())/float64(time.Second)
}

func (m *buildImageHandoffMetrics) Collect(ch chan<- prometheus.Metric) {
	m.mu.RLock()
	s, ok, observed := m.snapshot, m.ok, m.observed
	attempt, completed, inflight, enabled := m.attempt, m.completed, m.inflight, !m.stopped && (m.runCtx == nil || m.runCtx.Err() == nil)
	m.mu.RUnlock()
	bit := func(v bool) float64 {
		if v {
			return 1
		}
		return 0
	}
	values := []float64{float64(s.Pending), handoffUnix(s.OldestFinishedAt), bit(ok), handoffUnix(observed), handoffUnix(attempt), handoffUnix(completed), bit(inflight), bit(enabled)}
	age := m.now().UTC().Sub(observed)
	fresh := ok && handoffUnix(observed) > 0 && age >= 0 && age <= api.ObsBuildImageHandoffFreshnessMaxAge
	for i, value := range values {
		if i < 2 && !fresh {
			continue
		}
		ch <- prometheus.MustNewConstMetric(m.descs[i], prometheus.GaugeValue, value)
	}
	m.queries.Collect(ch)
	m.work.Collect(ch)
	m.commits.Collect(ch)
}

func (m *buildImageHandoffMetrics) run(ctx context.Context, store state.BuildImageHandoffObservationStore, node string, log *slog.Logger) {
	m.runOnce.Do(func() {
		ticker := time.NewTicker(api.ObsBuildImageHandoffCollectionInterval)
		defer ticker.Stop()
		m.runWithTicks(ctx, store, node, log, ticker.C)
	})
}

func (m *buildImageHandoffMetrics) runWithTicks(ctx context.Context, store state.BuildImageHandoffObservationStore, node string, log *slog.Logger, ticks <-chan time.Time) {
	for ctx.Err() == nil {
		if err := m.refresh(ctx, store, node); err != nil && ctx.Err() == nil && log != nil {
			log.Warn("imaged: build handoff observation failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticks:
		}
	}
}

func (m *buildImageHandoffMetrics) refresh(ctx context.Context, store state.BuildImageHandoffObservationStore, node string) error {
	started := m.now().UTC()
	readCtx, cancel := context.WithTimeout(ctx, api.ObsBuildImageHandoffCollectionBudget)
	defer cancel()
	var s state.BuildImageHandoffSnapshot
	var err error
	if store == nil {
		err = errors.New("build handoff snapshot reader unavailable")
	} else {
		s, err = store.BuildImageHandoffSnapshot(readCtx, node)
	}
	if err == nil {
		err = readCtx.Err()
	}
	if err == nil && !(s.Pending == 0 && s.OldestFinishedAt.IsZero() || s.Pending > 0 && handoffUnix(s.OldestFinishedAt) > 0 && !s.OldestFinishedAt.After(m.now().UTC())) {
		err = errors.New("inconsistent build handoff counts and timestamps")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ok = err == nil
	if err == nil {
		m.snapshot, m.observed = s, started
	}
	return err
}

func (m *buildImageHandoffMetrics) start(ctx context.Context) {
	m.mu.Lock()
	m.runCtx = ctx
	m.mu.Unlock()
}
func (m *buildImageHandoffMetrics) stop() { m.mu.Lock(); m.stopped = true; m.mu.Unlock() }
func (m *buildImageHandoffMetrics) beginRecovery() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.attempt, m.inflight = m.now().UTC(), true
	m.mu.Unlock()
}
func (m *buildImageHandoffMetrics) finishRecovery() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.completed, m.inflight = m.now().UTC(), false
	m.mu.Unlock()
}
func (m *buildImageHandoffMetrics) observeQuery(ctx context.Context, source string, err error) {
	if m == nil {
		return
	}
	outcome := "success"
	if err != nil {
		outcome = "error"
		if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
			outcome = "canceled"
		}
	}
	m.queries.WithLabelValues(source, outcome).Inc()
}
func (m *buildImageHandoffMetrics) observeWork(ctx context.Context, source string, err error) {
	if m == nil {
		return
	}
	outcome := "error"
	switch {
	case err == nil:
		outcome = "returned"
	case ctx.Err() != nil && errors.Is(err, ctx.Err()):
		outcome = "canceled"
	case errors.Is(err, db.ErrNotificationNotOwned):
		outcome = "not_owned"
	case errors.Is(err, buildexport.ErrBusy), errors.Is(err, state.ErrConflict), errors.Is(err, state.ErrInvalidStateTransition):
		outcome = "deferred"
	case errors.Is(err, errImagePreparationRecovery):
		outcome = "retryable"
	}
	m.work.WithLabelValues(source, outcome).Inc()
}
func (m *buildImageHandoffMetrics) commit(transition string) {
	if m != nil {
		m.commits.WithLabelValues(transition).Inc()
	}
}
