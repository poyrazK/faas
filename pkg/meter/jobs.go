// Package meter — job-task metering (issue #1184 Workstream A / ADR-099).
//
// Distinct from the app sampler at sampler.go. Job-task instances
// have kind="job_task" and carry no AppID; the billing key is
// (account_id, job_id, instance_id). The current usage_minutes
// schema is widened by migration 00257: job rows use app_id=NULL,
// job_id=<jobs.id>, and meter_kind='job'. The daily compatibility
// rollup keeps app_id=<job id> because usage_daily's existing primary
// key is app_id-based, while its meter_kind column distinguishes jobs.
//
// Sampler wiring (cmd/meterd/main.go): on each 1m tick, call
// sampler.SampleAndRoll (app rows) THEN sampler.SampleJobsAndRoll
// (job rows). Both append to usage_minutes; the rollup cron
// (pkg/meter/rollup.go) groups them the same way. Daily-level
// separation between apps and jobs lives in the (account_id,
// app_id, day) row identity — a job's daily row has app_id=<job
// id> and no matching apps row, so dashboards can filter
// `WHERE NOT EXISTS (SELECT 1 FROM apps WHERE apps.id =
// usage_daily.app_id)` to extract job rows.
//
// 7 metrics (this file):
//
//	jobs_runs_total{plan,status}              — counter
//	jobs_tasks_total{plan,status}             — counter
//	job_task_duration_seconds{plan,status}   — histogram
//	jobs_queue_depth{plan}                    — gauge (per-plan)
//	jobs_concurrent{plan}                     — gauge (per-plan)
//	jobs_dispatch_seconds{plan,result}        — histogram
//	jobs_dispatch_rejected_total{plan,reason} — counter

package meter

import (
	"context"
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// JobMetrics holds the 7 Prometheus counters / gauges / histograms
// for job workload observability. Wired by cmd/meterd/main.go
// alongside the existing app sampler. Distinct from
// ColdBootMetrics / WakePhaseMetrics / LivenessMetrics so the
// job dashboards can `sum by (plan)` cleanly without a metric
// name collision.
//
// Each constructor returns a *JobMetrics + a fresh
// *prometheus.Registry so vmmd / meterd can mount the registry
// on the http mux without polluting the global default
// (issue #554 / ADR-078 — same per-daemon-registry pattern).
type JobMetrics struct {
	reg *prometheus.Registry
	// RunsTotal counts run transitions. Status is the canonical
	// aggregate: 'started' / 'succeeded' / 'failed' /
	// 'cancelled'. Counter — never decreases.
	RunsTotal *prometheus.CounterVec
	// TasksTotal counts per-task terminal transitions. Status
	// is the canonical per-task error_class from
	// mapExitToTerminalStatus: 'succeeded' / 'failed' /
	// 'timeout' / 'oom' / 'cancelled' / 'infra'.
	TasksTotal *prometheus.CounterVec
	// TaskDurationSeconds observes the per-task wall-clock
	// duration (claimed→terminal). Buckets chosen to cover
	// Hobby (≤300s) and Scale (≤3600s) plans plus a long-tail
	// bucket for the rare >1h task (mostly retried-dead-letter
	// after timeout). Histogram buckets are seconds and
	// log10-spaced for the metric-aggregation sweet spot.
	TaskDurationSeconds *prometheus.HistogramVec
	// QueueDepth is the per-plan gauge of currently-queued
	// tasks (status='queued'). Sampled once per 1m tick by
	// meterd. Plan label keeps the dashboard per-plan without
	// a SUM aggregation.
	QueueDepth *prometheus.GaugeVec
	// Concurrent is the per-plan gauge of currently-claimed
	// tasks (status='claimed'). Sampled once per 1m tick.
	Concurrent *prometheus.GaugeVec
	// DispatchSeconds observes the per-dispatch wall-clock
	// cost (WakeJob start → instance created). Result is
	// 'ok' or 'failed'. Histogram covers the expected cold-
	// boot range (1-15s) plus a 60s bucket for backpressure.
	DispatchSeconds *prometheus.HistogramVec
	// DispatchRejectedTotal counts WakeJob admissions
	// refused by the engine. Reason is one of
	// 'plan_quota' / 'plan_jobs_disabled' / 'ram_ceiling'
	// / 'missing_task' / 'lease_taken'.
	DispatchRejectedTotal *prometheus.CounterVec
}

// NewJobMetrics registers the 7 metrics on a fresh per-daemon
// registry and returns the typed wrapper. Mirrors the
// registration pattern of NewFrameworkReadyMetrics at
// pkg/fcvm/metrics.go:178 — failures surface immediately so a
// missing dashboard panel is loud (the rule is "register at
// startup, fail fast").
func NewJobMetrics() *JobMetrics {
	reg := prometheus.NewRegistry()
	m := &JobMetrics{
		reg: reg,
		RunsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "jobs_runs_total",
			Help: "Job runs by terminal status (started/succeeded/failed/cancelled)",
		}, []string{"plan", "status"}),
		TasksTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "jobs_tasks_total",
			Help: "Job tasks by terminal error_class (succeeded/failed/timeout/oom/cancelled/infra)",
		}, []string{"plan", "status"}),
		TaskDurationSeconds: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "job_task_duration_seconds",
			Help:    "Per-task wall-clock duration in seconds (claimed → terminal)",
			Buckets: []float64{1, 5, 10, 30, 60, 120, 300, 600, 1800, 3600},
		}, []string{"plan", "status"}),
		QueueDepth: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "jobs_queue_depth",
			Help: "Currently-queued job tasks per plan (status='queued')",
		}, []string{"plan"}),
		Concurrent: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "jobs_concurrent",
			Help: "Currently-claimed job tasks per plan (status='claimed')",
		}, []string{"plan"}),
		DispatchSeconds: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "jobs_dispatch_seconds",
			Help:    "Per-dispatch wall-clock cost in seconds (WakeJob start → instance created)",
			Buckets: []float64{0.5, 1, 2, 5, 10, 15, 30, 60},
		}, []string{"plan", "result"}),
		DispatchRejectedTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "jobs_dispatch_rejected_total",
			Help: "WakeJob admissions refused by the engine, by reason",
		}, []string{"plan", "reason"}),
	}
	reg.MustRegister(
		m.RunsTotal, m.TasksTotal, m.TaskDurationSeconds,
		m.QueueDepth, m.Concurrent, m.DispatchSeconds,
		m.DispatchRejectedTotal,
	)
	return m
}

// Registry exposes the underlying registry — meterd's mux mounts
// this alongside the OpsMetrics + ColdBootMetrics registries
// via promhttp.HandlerFor. Mirrors FrameworkReadyMetrics.Registry.
func (m *JobMetrics) Registry() *prometheus.Registry { return m.reg }

// SampleJobsAndRoll walks every kind="job_task" instance and
// appends one minute of billable usage per instance to
// usage_minutes. Parallel to SampleAndRoll but:
//
//   - reads the per-account job ledger (NOT ListAllApps)
//   - writes through state.JobUsageAppender so usage_minutes rows
//     use the dedicated job_id + meter_kind columns
//   - returns the rows it wrote for the test surface + telemetry
//
// Idempotent on (instance_id, minute) via the existing
// AppendUsage INSERT ... ON CONFLICT contract (mirrors the
// app path's idempotency).
//
// Safe to call from a single meterd goroutine; the Store
// implementation owns the concurrency contract (MemStore uses a
// single mutex; PgStore's INSERT ... ON CONFLICT is atomic per
// row).
//
// Wired by cmd/meterd/main.go after SampleAndRoll on the same
// 1m ticker. Errors are logged Warn and the tick continues; a
// persistent failure surfaces as a flood of WARN logs that an
// operator can alert on.
func (s *Sampler) SampleJobsAndRoll(ctx context.Context) ([]JobRolledRow, error) {
	minute := MinuteKey(s.now())
	instances, err := s.store.ListJobInstances(ctx)
	if err != nil {
		return nil, fmt.Errorf("meter: SampleJobsAndRoll list: %w", err)
	}
	if len(instances) == 0 {
		return nil, nil
	}
	appender, ok := s.store.(state.JobUsageAppender)
	if !ok {
		return nil, fmt.Errorf("meter: job usage appender unavailable")
	}
	var out []JobRolledRow
	for _, ins := range instances {
		if ins.State == "destroyed" || ins.State == "parked" {
			continue
		}
		// Mirror the app path's billable-RAM math (spec §4.7:
		// plan RAM + 8 MB per running second; NOT changed by
		// this PR — jobs get the same billable MB as apps).
		// Jobs have no sidecar drives (single-workload path),
		// so BillableRAMMBWithSidecars collapses to its first
		// arg.
		admissionMB := api.BillableRAMMBWithSidecars(ins.RAMMB, nil)
		accountID := s.lookupJobAccountID(ctx, ins.JobID)
		row := JobRolledRow{
			InstanceID:  ins.ID,
			JobID:       ins.JobID,
			AccountID:   accountID,
			Minute:      minute,
			AdmissionMB: admissionMB,
			MBSeconds:   MBSecondsPerMinute(admissionMB),
		}
		if err := appender.AppendJobUsage(ctx, accountID, ins.JobID, ins.ID, minute, row.MBSeconds, 0, 0, 0, 0, 0, 0, 0); err != nil {
			return out, err
		}
		out = append(out, row)
	}
	return out, nil
}

// JobRolledRow is the typed per-(job_instance, minute) billable
// line. Parallel to RolledRow but the AppID field is renamed to
// JobID (no AppID on a job row — see file doc).
type JobRolledRow struct {
	InstanceID  string
	JobID       string
	AccountID   string
	Minute      time.Time
	MBSeconds   int64
	AdmissionMB int
}

// lookupJobAccountID resolves jobs.account_id from jobs.id.
// Wraps the store call in a closure so the inner loop stays
// flat; a missing job (e.g. soft-deleted between list and
// lookup) returns "" and the AppendUsage call still succeeds
// (the dashboard's job filter renders "" as unknown_account).
func (s *Sampler) lookupJobAccountID(ctx context.Context, jobID string) string {
	if jobID == "" {
		return ""
	}
	j, err := s.store.JobGetByID(ctx, jobID)
	if err != nil {
		return ""
	}
	return j.AccountID
}
