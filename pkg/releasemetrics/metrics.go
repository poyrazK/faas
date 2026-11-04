// Package releasemetrics instruments the release guards: deploy freezes
// (ADR-20261019071733), release approvals (ADR-20261020085706), scheduled
// releases (ADR-20261021152201), database switches (ADR-20261023181931) and
// the post-release watch (ADR-1445). Workers report each pass and each
// failed one; guards report their decisions. One family per kind keeps the
// alert rules and dashboards uniform across guards and daemons.
package releasemetrics

import (
	"errors"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Workers that report passes.
const (
	WorkerScheduledReleases = "scheduled_releases"
	WorkerDatabaseSwitches  = "database_switches"
	WorkerPostReleaseWatch  = "post_release_watch"
	WorkerApprovalNotices   = "approval_notices"
)

// Guards and their decision outcomes.
const (
	GuardFreeze         = "freeze"
	GuardApproval       = "approval"
	GuardSchedule       = "schedule"
	GuardDatabaseSwitch = "database_switch"
	GuardPostRelease    = "post_release"
	GuardApprovalNotice = "approval_notice"
)

var outcomes = map[string][]string{
	// A freeze refused a release, or an audited override released anyway.
	GuardFreeze: {"refused", "overridden"},
	// A release waited for people, was approved, rejected or timed out, or
	// was refused because it had no canary to wait in.
	GuardApproval: {"held", "approved", "rejected", "expired", "canary_required"},
	// A due schedule started, was blocked (or expired before it could
	// start), or ended without starting. A start that will be retried
	// counts as a worker error.
	GuardSchedule: {"started", "blocked", "ended"},
	// A database switch completed, was cancelled, began aborting or failed.
	GuardDatabaseSwitch: {"completed", "cancelled", "aborting", "failed"},
	// The post-release watch rolled a regressed release back, or kept it
	// serving because the rollback would cross release commands (ADR-1448).
	GuardPostRelease: {"rolled_back", "kept"},
	// An approval notice was delivered, will be retried, or was given up.
	GuardApprovalNotice: {"sent", "retried", "dropped"},
}

var workers = []string{WorkerScheduledReleases, WorkerDatabaseSwitches, WorkerPostReleaseWatch, WorkerApprovalNotices}

// Metrics counts what the release guards did. Its methods are nil-receiver
// safe, so code built without a registry records nothing.
type Metrics struct {
	registry  *prometheus.Registry
	lastPass  *prometheus.GaugeVec
	errors    *prometheus.CounterVec
	decisions *prometheus.CounterVec
}

// New registers the release guard metrics on registry. Registering twice on
// one registry returns metrics sharing the first collectors.
func New(registry *prometheus.Registry) *Metrics {
	m := &Metrics{registry: registry}
	m.lastPass = register(registry, prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "faas_release_worker_last_pass_timestamp_seconds",
		Help: "Unix time a release guard worker last finished a pass that could read its queue, by worker.",
	}, []string{"worker"}))
	m.errors = register(registry, prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "faas_release_worker_errors_total",
		Help: "Release guard worker passes or items that failed and will be retried, by worker.",
	}, []string{"worker"}))
	m.decisions = register(registry, prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "faas_release_guard_decisions_total",
		Help: "Release guard decisions by guard and outcome (see pkg/releasemetrics for each guard's outcomes).",
	}, []string{"guard", "outcome"}))
	for _, worker := range workers {
		m.errors.WithLabelValues(worker)
	}
	for guard, list := range outcomes {
		for _, outcome := range list {
			m.decisions.WithLabelValues(guard, outcome)
		}
	}
	return m
}

// Registry reports the registry the metrics live in.
func (m *Metrics) Registry() *prometheus.Registry {
	if m == nil {
		return nil
	}
	return m.registry
}

// Pass records a worker pass that could read its queue at at.
func (m *Metrics) Pass(worker string, at time.Time) {
	if m == nil || !known(workers, worker) {
		return
	}
	m.lastPass.WithLabelValues(worker).Set(float64(at.Unix()))
}

// Error records a failed worker pass or item.
func (m *Metrics) Error(worker string) {
	if m == nil || !known(workers, worker) {
		return
	}
	m.errors.WithLabelValues(worker).Inc()
}

// Decision records one guard decision; unknown pairs are ignored so a label
// set never grows past the documented outcomes.
func (m *Metrics) Decision(guard, outcome string) {
	if m == nil || !known(outcomes[guard], outcome) {
		return
	}
	m.decisions.WithLabelValues(guard, outcome).Inc()
}

func known(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}

func register[T prometheus.Collector](registry *prometheus.Registry, c T) T {
	err := registry.Register(c)
	if err == nil {
		return c
	}
	var already prometheus.AlreadyRegisteredError
	if errors.As(err, &already) {
		if existing, ok := already.ExistingCollector.(T); ok {
			return existing
		}
	}
	panic(err)
}
