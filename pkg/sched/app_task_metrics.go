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

// AppTaskMetrics observes the shared durable queue independently of dispatch.
// It never performs I/O during a scrape or changes a coordinator decision.
type AppTaskMetrics struct {
	mu                        sync.RWMutex
	snapshot                  state.AppTaskObservationSnapshot
	ok                        bool
	observed                  time.Time
	observationCtx, runnerCtx context.Context
	observing, running        bool
	enabled                   bool
	capacity, occupied        int
	now                       func() time.Time
	runOnce                   sync.Once
	descs                     []*prometheus.Desc
	operations                *prometheus.CounterVec
	recovery                  *prometheus.CounterVec
	results                   *prometheus.CounterVec
	lastClaim, lastSweep      time.Time
}

func NewAppTaskMetrics(reg prometheus.Registerer, enabled bool, concurrency int) *AppTaskMetrics {
	config := normalizeAppTaskCoordinatorConfig(AppTaskCoordinatorConfig{MaxConcurrent: concurrency})
	m := &AppTaskMetrics{now: time.Now, observing: true, enabled: enabled, capacity: config.MaxConcurrent,
		operations: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "schedd_app_task_operations_total", Help: "Actual coordinator calls by bounded operation and return outcome. Execute success includes customer verdicts; canceled and lease_lost are separate from errors."}, []string{"operation", "outcome"}),
		recovery:   prometheus.NewCounterVec(prometheus.CounterOpts{Name: "schedd_app_task_recovery_total", Help: "Durable changes reported by successful sweeps. Requeued restores are safe before dispatch; failed runs have uncertain command outcomes. Policy retries not represented in sweep results are not inferred."}, []string{"outcome"}),
		results:    prometheus.NewCounterVec(prometheus.CounterOpts{Name: "schedd_app_task_results_total", Help: "Successful completion acknowledgements by kind and returned durable status, including queued policy retries. Not command delivery or infrastructure failure counters; excludes sweeper changes."}, []string{"kind", "status"}),
	}
	for _, op := range []string{"claim", "sweep", "renew", "attach_read", "restore", "running_fence", "attach_route", "execute", "destroy", "complete"} {
		for _, outcome := range []string{"success", "error", "canceled", "lease_lost", "empty", "cancellation_pending"} {
			m.operations.WithLabelValues(op, outcome)
		}
	}
	for _, outcome := range []string{"requeued_restores", "failed_runs", "cancelled"} {
		m.recovery.WithLabelValues(outcome)
	}
	for k := range m.snapshot {
		for _, status := range []state.AppTaskStatus{state.AppTaskSucceeded, state.AppTaskFailed, state.AppTaskTimedOut, state.AppTaskCancelled, state.AppTaskQueued} {
			m.results.WithLabelValues(string(state.AppTaskObservationKind(k)), string(status))
		}
	}
	for i, item := range []struct{ name, help string }{
		{"schedd_app_task_pending", "All nonterminal app tasks by kind and durable class in the shared fleet claim queue. Replicas overlap: never sum them."},
		{"schedd_app_task_age_unknown", "Pending subset without a trustworthy boundary. Restoring, retry_wait and held ages are intentionally unknown."},
		{"schedd_app_task_oldest_boundary_timestamp_seconds", "Oldest known boundary: queued max(created,updated,retry), running start, cancellation request, expired lease or missed start deadline. Not continuous eligibility or class residence; zero without known ages."},
		{"schedd_app_task_collection_success", "Whether the last completed durable task observation succeeded; check freshness separately."},
		{"schedd_app_task_collection_last_success_timestamp_seconds", "Start of last successful durable task read; frozen on failure and zero before success."},
		{"schedd_app_task_observation_enabled", "Whether the independent observer is running; zero on cancellation or return does not imply drained work."},
		{"schedd_app_task_dispatch_enabled", "Whether this process is configured to dispatch app tasks. Intentional opt-out is distinct from a stopped enabled runner."},
		{"schedd_app_task_runner_running", "Whether the enabled coordinator has entered Run and has not returned or been canceled. Does not prove worker progress."},
		{"schedd_app_task_slots_configured", "Normalized local app-task concurrency limit, including when dispatch is disabled."},
		{"schedd_app_task_slots_in_use", "Local claimed task handlers, including restore, execution and teardown. Not a fleet count or a VM count."},
		{"schedd_app_task_claim_last_success_timestamp_seconds", "Completion of last successful claim query, including empty results; zero before success. Busy slots need not poll."},
		{"schedd_app_task_sweep_last_success_timestamp_seconds", "Completion of last successful recovery sweep, including empty results; zero before success."},
	} {
		var labels []string
		if i < 3 {
			labels = []string{"kind", "class"}
		}
		m.descs = append(m.descs, prometheus.NewDesc(item.name, item.help, labels, nil))
	}
	reg.MustRegister(m)
	return m
}

func (m *AppTaskMetrics) Describe(ch chan<- *prometheus.Desc) {
	m.operations.Describe(ch)
	m.recovery.Describe(ch)
	m.results.Describe(ch)
	for _, d := range m.descs {
		ch <- d
	}
}

func (m *AppTaskMetrics) Collect(ch chan<- prometheus.Metric) {
	defer m.operations.Collect(ch)
	defer m.recovery.Collect(ch)
	defer m.results.Collect(ch)
	m.mu.RLock()
	s, ok, at := m.snapshot, m.ok, m.observed
	observing := m.observing && (m.observationCtx == nil || m.observationCtx.Err() == nil)
	running := m.running && m.runnerCtx.Err() == nil
	enabled, capacity, occupied, claim, sweep := m.enabled, m.capacity, m.occupied, m.lastClaim, m.lastSweep
	m.mu.RUnlock()
	bit := func(v bool) float64 {
		if v {
			return 1
		}
		return 0
	}
	for i, v := range []float64{bit(ok), primeUnix(at), bit(observing), bit(enabled), bit(running), float64(capacity), float64(occupied), primeUnix(claim), primeUnix(sweep)} {
		ch <- prometheus.MustNewConstMetric(m.descs[i+3], prometheus.GaugeValue, v)
	}
	age := m.now().UTC().Sub(at)
	if !ok || primeUnix(at) <= 0 || age < 0 || age > api.ObsAppTaskFreshnessMaxAge {
		return
	}
	for k, classes := range s {
		for c, p := range classes {
			for i, v := range []float64{float64(p.Pending), float64(p.UnknownAge), primeUnix(p.OldestAt)} {
				ch <- prometheus.MustNewConstMetric(m.descs[i], prometheus.GaugeValue, v, string(state.AppTaskObservationKind(k)), state.AppTaskObservationClass(c).String())
			}
		}
	}
}

func (m *AppTaskMetrics) refresh(ctx context.Context, reader state.AppTaskObservationStore) error {
	started := m.now().UTC()
	readCtx, cancel := context.WithTimeout(ctx, api.ObsAppTaskCollectionBudget)
	defer cancel()
	var s state.AppTaskObservationSnapshot
	var err error
	if reader == nil {
		err = errors.New("app task observation reader unavailable")
	} else {
		s, err = reader.AppTaskObservationSnapshot(readCtx, started)
	}
	if err == nil {
		err = readCtx.Err()
	}
	if err == nil {
		err = validateAppTaskObservation(s, m.now().UTC())
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ok = err == nil
	if err == nil {
		m.snapshot, m.observed = s, started
	}
	return err
}

func validateAppTaskObservation(s state.AppTaskObservationSnapshot, now time.Time) error {
	for _, classes := range s {
		for c, p := range classes {
			known := p.Pending - p.UnknownAge
			unknownOnly := c == int(state.AppTaskObservationUnknown) || c == int(state.AppTaskObservationRestoring) || c == int(state.AppTaskObservationRetryWait) || c == int(state.AppTaskObservationHeld)
			if p.Pending < 0 || p.UnknownAge < 0 || p.UnknownAge > p.Pending || (unknownOnly && known != 0) ||
				!(known == 0 && p.OldestAt.IsZero() || known > 0 && primeUnix(p.OldestAt) > 0 && !p.OldestAt.After(now)) {
				return errors.New("inconsistent app task counts and timestamps")
			}
		}
	}
	return nil
}

// RunObservation must run even when dispatch is disabled, independently of
// synchronous startup recovery and the worker/sweeper pools.
func (m *AppTaskMetrics) RunObservation(ctx context.Context, reader state.AppTaskObservationStore, log *slog.Logger) {
	if m == nil {
		return
	}
	m.runOnce.Do(func() {
		ticker := time.NewTicker(api.ObsAppTaskCollectionInterval)
		defer ticker.Stop()
		m.runWithTicks(ctx, reader, log, ticker.C)
	})
}

func (m *AppTaskMetrics) runWithTicks(ctx context.Context, reader state.AppTaskObservationStore, log *slog.Logger, ticks <-chan time.Time) {
	m.mu.Lock()
	m.observationCtx = ctx
	m.observing = true
	m.mu.Unlock()
	defer func() { m.mu.Lock(); m.observing = false; m.mu.Unlock() }()
	for ctx.Err() == nil {
		if err := m.refresh(ctx, reader); err != nil && ctx.Err() == nil && log != nil {
			// Do not emit database errors, query arguments or customer values.
			log.Warn("schedd: app task observation failed", "error_class", appTaskErrorClass(err))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticks:
		}
	}
}

func (m *AppTaskMetrics) runnerStart(ctx context.Context) func() {
	if m == nil {
		return func() {}
	}
	m.mu.Lock()
	m.runnerCtx = ctx
	m.running = true
	m.mu.Unlock()
	return func() { m.mu.Lock(); m.running = false; m.mu.Unlock() }
}
func (m *AppTaskMetrics) slot() func() {
	if m == nil {
		return func() {}
	}
	m.mu.Lock()
	m.occupied++
	m.mu.Unlock()
	return func() { m.mu.Lock(); m.occupied--; m.mu.Unlock() }
}
func (m *AppTaskMetrics) operation(ctx context.Context, op string, err error) {
	if m == nil {
		return
	}
	outcome := "success"
	switch {
	case op == "claim" && errors.Is(err, state.ErrNotFound):
		outcome = "empty"
	case err != nil && ctx.Err() != nil &&
		((op != "restore" && op != "destroy" && op != "complete") || errors.Is(ctx.Err(), context.Canceled)):
		outcome = "canceled"
	case errors.Is(err, state.ErrAppTaskCancellationPending):
		outcome = "cancellation_pending"
	case errors.Is(err, state.ErrAppTaskLeaseLost):
		outcome = "lease_lost"
	case err != nil:
		outcome = "error"
	}
	m.operations.WithLabelValues(op, outcome).Inc()
	if outcome == "success" || outcome == "empty" {
		m.mu.Lock()
		if op == "claim" {
			m.lastClaim = m.now().UTC()
		}
		if op == "sweep" {
			m.lastSweep = m.now().UTC()
		}
		m.mu.Unlock()
	}
}
func (m *AppTaskMetrics) swept(r state.AppTaskSweepResult) {
	if m == nil {
		return
	}
	m.recovery.WithLabelValues("requeued_restores").Add(float64(r.RequeuedRestores))
	m.recovery.WithLabelValues("failed_runs").Add(float64(r.FailedRuns))
	m.recovery.WithLabelValues("cancelled").Add(float64(r.Cancelled))
}
func (m *AppTaskMetrics) completed(t state.AppTask) {
	if m == nil || !t.Kind.Valid() {
		return
	}
	switch t.Status {
	case state.AppTaskSucceeded, state.AppTaskFailed, state.AppTaskTimedOut, state.AppTaskCancelled, state.AppTaskQueued:
		m.results.WithLabelValues(string(t.Kind), string(t.Status)).Inc()
	}
}
