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

type commandCronMetrics struct {
	mu                             sync.RWMutex
	snapshot                       commandCronSnapshot
	ok                             bool
	observed, lastStart, lastSweep time.Time
	runCtx                         context.Context
	stopped, sweeping              bool
	now                            func() time.Time
	runOnce                        sync.Once
	descs                          []*prometheus.Desc
	sweeps, decisions, admissions  *prometheus.CounterVec
	sweepDuration, admissionLag    prometheus.Histogram
	lagUnknown                     prometheus.Counter
}

func newCommandCronMetrics(reg prometheus.Registerer) *commandCronMetrics {
	m := &commandCronMetrics{now: time.Now,
		sweeps:        prometheus.NewCounterVec(prometheus.CounterOpts{Name: "schedd_command_cron_sweeps_total", Help: "Actual shared cron scans by completion outcome, including empty lists. Success proves enumeration and owner lookup, not successful admission of every command."}, []string{"outcome"}),
		decisions:     prometheus.NewCounterVec(prometheus.CounterOpts{Name: "schedd_command_cron_decisions_total", Help: "Actual command-cron pre-admission and legacy-path decisions. Not occurrence counts or delivery acknowledgements."}, []string{"outcome"}),
		admissions:    prometheus.NewCounterVec(prometheus.CounterOpts{Name: "schedd_command_cron_admissions_total", Help: "Actual scheduled command occurrence calls by bounded returned disposition, including stale compare-and-swap results and errors. Queued/pending do not prove execution."}, []string{"outcome"}),
		sweepDuration: prometheus.NewHistogram(prometheus.HistogramOpts{Name: "schedd_command_cron_sweep_duration_seconds", Help: "Wall duration of actual shared cron scans, including inline HTTP work. Not command admission latency."}),
		admissionLag:  prometheus.NewHistogram(prometheus.HistogramOpts{Name: "schedd_command_cron_admission_lag_seconds", Help: "Returned occurrence evaluation time minus ScheduledFor after committed command admission or policy disposition. Includes catch-up history; not database commit time, execution-start or continuous eligibility delay.", Buckets: []float64{1, 5, 15, 30, 60, 120, 300, 600, 1800, 3600}}),
		lagUnknown:    prometheus.NewCounter(prometheus.CounterOpts{Name: "schedd_command_cron_admission_lag_unknown_total", Help: "Committed occurrence returns without trustworthy nominal/admission timestamps; no fabricated zero latency."}),
	}
	for _, v := range []string{"success", "error", "canceled"} {
		m.sweeps.WithLabelValues(v)
	}
	for _, v := range []string{"disabled", "not_due", "account_held", "replacement_wait", "not_owned", "invalid_schedule", "lookup_error", "store_unavailable", "no_live_deployment", "canceled", "legacy_queued", "legacy_skipped", "legacy_stale", "legacy_error"} {
		m.decisions.WithLabelValues(v)
	}
	for _, v := range []string{"queued", "exclusive_pending", "coalesced", "skipped_overlap", "missed_deadline", "stale", "deployment_unavailable", "rejected", "error", "canceled", "unknown"} {
		m.admissions.WithLabelValues(v)
	}
	for i, item := range []struct{ name, help string }{
		{"schedd_command_cron_schedules", "Command schedules by observation class in this cron ownership scope, including disabled and suspended intent. Legacy scopes can overlap; never sum replicas."},
		{"schedd_command_cron_age_unknown", "Schedules without a trusted unconsumed-due boundary. Disabled, suspended, account-held and not-due ages are intentionally unknown."},
		{"schedd_command_cron_oldest_due_timestamp_seconds", "Oldest first unconsumed cron boundary after last_fired_at or creation, using canonical timezone/DST rules. Not continuous eligibility, last successful command or a promise the command should run. Zero without known ages."},
		{"schedd_command_cron_collection_success", "Whether the last independent durable observation succeeded; check freshness separately."},
		{"schedd_command_cron_collection_last_success_timestamp_seconds", "Start of the last successful observation; frozen on failure and zero before success."},
		{"schedd_command_cron_observation_enabled", "Whether this loop expects cron observation; zero on cancellation/return without implying schedules drained."},
		{"schedd_command_cron_sweep_last_start_timestamp_seconds", "Start of last actual shared cron sweep, including when no commands are due; zero before a sweep."},
		{"schedd_command_cron_sweep_last_success_timestamp_seconds", "Completion of last scan with successful listing and owner lookups; zero before success. Per-command errors are separate."},
		{"schedd_command_cron_sweep_running", "Whether a shared cron scan is in progress and its loop context has not been canceled."},
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
func (m *commandCronMetrics) Describe(ch chan<- *prometheus.Desc) {
	m.sweeps.Describe(ch)
	m.decisions.Describe(ch)
	m.admissions.Describe(ch)
	m.sweepDuration.Describe(ch)
	m.admissionLag.Describe(ch)
	m.lagUnknown.Describe(ch)
	for _, d := range m.descs {
		ch <- d
	}
}
func (m *commandCronMetrics) Collect(ch chan<- prometheus.Metric) {
	defer m.sweeps.Collect(ch)
	defer m.decisions.Collect(ch)
	defer m.admissions.Collect(ch)
	defer m.sweepDuration.Collect(ch)
	defer m.admissionLag.Collect(ch)
	defer m.lagUnknown.Collect(ch)
	m.mu.RLock()
	s, ok, at, start, sweep := m.snapshot, m.ok, m.observed, m.lastStart, m.lastSweep
	enabled := !m.stopped && (m.runCtx == nil || m.runCtx.Err() == nil)
	running := m.sweeping && enabled
	m.mu.RUnlock()
	bit := func(v bool) float64 {
		if v {
			return 1
		}
		return 0
	}
	for i, v := range []float64{bit(ok), primeUnix(at), bit(enabled), primeUnix(start), primeUnix(sweep), bit(running)} {
		ch <- prometheus.MustNewConstMetric(m.descs[i+3], prometheus.GaugeValue, v)
	}
	age := m.now().UTC().Sub(at)
	if !ok || primeUnix(at) <= 0 || age < 0 || age > api.ObsCommandCronFreshnessMaxAge {
		return
	}
	for c, p := range s {
		for i, v := range []float64{float64(p.Pending), float64(p.UnknownAge), primeUnix(p.OldestAt)} {
			ch <- prometheus.MustNewConstMetric(m.descs[i], prometheus.GaugeValue, v, commandCronClass(c).String())
		}
	}
}
func (m *commandCronMetrics) refresh(ctx context.Context, reader state.CommandCronObservationStore, owner, local string) error {
	started := m.now().UTC()
	readCtx, cancel := context.WithTimeout(ctx, api.ObsCommandCronCollectionBudget)
	defer cancel()
	var snapshot commandCronSnapshot
	var err error
	if reader == nil {
		err = errors.New("command cron observation reader unavailable")
	} else {
		var rows []state.CommandCronObservation
		rows, err = reader.CommandCronObservations(readCtx, owner, local)
		if err == nil {
			snapshot, err = observeCommandCrons(readCtx, rows, started)
		}
	}
	if err == nil {
		err = readCtx.Err()
	}
	if err == nil {
		err = validateCommandCronSnapshot(snapshot, m.now().UTC())
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ok = err == nil
	if err == nil {
		m.snapshot, m.observed = snapshot, started
	}
	return err
}
func (m *commandCronMetrics) run(ctx context.Context, reader state.CommandCronObservationStore, owner, local string, log *slog.Logger) {
	m.runOnce.Do(func() {
		ticker := time.NewTicker(api.ObsCommandCronCollectionInterval)
		defer ticker.Stop()
		m.runWithTicks(ctx, reader, owner, local, log, ticker.C)
	})
}
func (m *commandCronMetrics) runWithTicks(ctx context.Context, reader state.CommandCronObservationStore, owner, local string, log *slog.Logger, ticks <-chan time.Time) {
	for ctx.Err() == nil {
		if err := m.refresh(ctx, reader, owner, local); err != nil && ctx.Err() == nil && log != nil {
			log.Warn("sched: command cron admission observation failed", "error_class", appTaskErrorClass(err))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticks:
		}
	}
}
func (l *Loop) startCommandCronObservation(ctx context.Context) func() {
	if l.cronMetrics == nil || l.engine == nil {
		return func() {}
	}
	m := l.cronMetrics
	child, cancel := context.WithCancel(ctx)
	m.mu.Lock()
	m.runCtx = ctx
	m.mu.Unlock()
	reader, _ := l.engine.store.(state.CommandCronObservationStore)
	go m.run(child, reader, l.engine.ownerNodeID, l.engine.defaultLocalNodeID, l.log)
	return func() { m.mu.Lock(); m.stopped = true; m.mu.Unlock(); cancel() }
}
func (m *commandCronMetrics) beginSweep() time.Time {
	if m == nil {
		return time.Time{}
	}
	at := m.now().UTC()
	m.mu.Lock()
	m.lastStart, m.sweeping = at, true
	m.mu.Unlock()
	return at
}
func (m *commandCronMetrics) finishSweep(ctx context.Context, start time.Time, err error) {
	if m == nil {
		return
	}
	outcome := "success"
	if ctx.Err() != nil {
		outcome = "canceled"
	} else if err != nil {
		outcome = "error"
	}
	m.sweeps.WithLabelValues(outcome).Inc()
	at := m.now().UTC()
	if !at.Before(start) {
		m.sweepDuration.Observe(at.Sub(start).Seconds())
	}
	m.mu.Lock()
	m.sweeping = false
	if err == nil && ctx.Err() == nil {
		m.lastSweep = at
	}
	m.mu.Unlock()
}
func (m *commandCronMetrics) decision(outcome string) {
	if m != nil {
		m.decisions.WithLabelValues(outcome).Inc()
	}
}
func (m *commandCronMetrics) occurrence(ctx context.Context, o state.ScheduleOccurrence, created bool, err error) {
	if m == nil {
		return
	}
	v := "unknown"
	switch {
	case err != nil && ctx.Err() != nil:
		v = "canceled"
	case errors.Is(err, state.ErrAppTaskDeploymentUnavailable):
		v = "deployment_unavailable"
	case err != nil:
		v = "error"
		if exclusiveAdmissionOutcome(err, false, false) == "rejected" {
			v = "rejected"
		}
	case o.ID == "":
		v = "stale"
	default:
		switch o.Status {
		case "queued":
			if created && o.AppTaskID != "" {
				v = "queued"
			}
		case "pending":
			if o.ExclusiveOperationID != "" {
				v = "exclusive_pending"
			}
		case "coalesced", "skipped_overlap", "missed_deadline":
			v = o.Status
		}
	}
	m.admissions.WithLabelValues(v).Inc()
	if err != nil || o.ID == "" {
		return
	}
	now := m.now().UTC()
	if !o.ScheduledFor.After(time.Unix(0, 0)) || o.CreatedAt.Before(o.ScheduledFor) || o.CreatedAt.After(now) {
		m.lagUnknown.Inc()
		return
	}
	m.admissionLag.Observe(o.CreatedAt.Sub(o.ScheduledFor).Seconds())
}

func (m *commandCronMetrics) decisionError(ctx context.Context) {
	m.decisionFailure(ctx, "lookup_error")
}

func (m *commandCronMetrics) decisionFailure(ctx context.Context, failure string) {
	if ctx.Err() != nil {
		m.decision("canceled")
	} else {
		m.decision(failure)
	}
}
