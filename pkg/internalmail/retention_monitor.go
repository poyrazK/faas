package internalmail

import (
	"context"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	q "github.com/onebox-faas/faas/pkg/internalmail/sqlc"
	"github.com/prometheus/client_golang/prometheus"
)

type RetentionStatistics struct {
	RetainedMessages      int64
	PrunedMessages        int64
	RetainedBytes         int64
	EligibleMessages      int64
	EligibleBytes         int64
	OldestEligibleSeconds float64
}

func (s *PGStore) RetentionStatistics(ctx context.Context, policy RetentionPolicy) (RetentionStatistics, error) {
	if err := policy.validate(); err != nil {
		return RetentionStatistics{}, err
	}
	r, err := s.queries.RetentionStatistics(ctx, s.pool, q.RetentionStatisticsParams{Namespace: s.namespace, RetentionDays: int32(policy.Days)})
	if err != nil {
		return RetentionStatistics{}, err
	}
	return RetentionStatistics{r.RetainedMessages, r.PrunedMessages, r.RetainedBytes, r.EligibleMessages, r.EligibleBytes, r.OldestEligibleSeconds}, nil
}

type RetentionStatisticsReader interface {
	RetentionStatistics(context.Context, RetentionPolicy) (RetentionStatistics, error)
}

// RetentionMonitor exposes durable namespace aggregates and process-local pass
// health. No collection scans payload bytes, decrypts content or runs cleanup.
type RetentionMonitor struct {
	source                   RetentionStatisticsReader
	enabled                  func() bool
	policy                   RetentionPolicy
	desc                     map[string]*prometheus.Desc
	mu                       sync.Mutex
	lastAttempt, lastSuccess float64
	lastUp                   bool
	lastResult               RetentionResult
}

func NewRetentionMonitor(source RetentionStatisticsReader, enabled func() bool, policy RetentionPolicy) *RetentionMonitor {
	m := &RetentionMonitor{source: source, enabled: enabled, policy: policy, desc: map[string]*prometheus.Desc{}}
	for _, def := range []struct{ name, help string }{
		{"enabled", "Internal mail payload retention gate."},
		{"dry_run", "Whether automatic retention is preview only."},
		{"days", "Configured quiet period after all recipients settle, in days."},
		{"stats_up", "Whether durable retention aggregates were collected successfully."},
		{"retained_messages", "Messages with their encrypted payload still retained."},
		{"pruned_messages", "Durable messages whose payload has been pruned."},
		{"retained_bytes", "Logical bytes of encrypted payload still retained, excluding database overhead and backups."},
		{"eligible_messages", "Payloads currently eligible for retention cleanup."},
		{"eligible_bytes", "Logical encrypted payload bytes currently eligible for cleanup."},
		{"oldest_eligible_seconds", "Age of the oldest message currently eligible for cleanup."},
		{"last_attempt_seconds", "Last completed retention pass attempt time; resets on process restart."},
		{"last_success_seconds", "Last successful retention pass time; resets on process restart."},
		{"last_run_up", "Whether the latest completed pass succeeded; zero before the first pass after restart."},
		{"last_pruned_messages", "Payloads pruned in the last successfully reported pass; resets on restart."},
		{"last_pruned_bytes", "Logical payload bytes pruned in the last successfully reported pass; resets on restart."},
	} {
		m.desc[def.name] = prometheus.NewDesc("maild_mail_retention_"+def.name, def.help, nil, nil)
	}
	return m
}

func (m *RetentionMonitor) Observe(result RetentionResult, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastAttempt, m.lastUp = float64(time.Now().Unix()), err == nil
	if err == nil {
		m.lastSuccess, m.lastResult = m.lastAttempt, result
	}
}

func (m *RetentionMonitor) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range m.desc {
		ch <- desc
	}
}

func (m *RetentionMonitor) Collect(ch chan<- prometheus.Metric) {
	emit := func(name string, value float64) {
		ch <- prometheus.MustNewConstMetric(m.desc[name], prometheus.GaugeValue, value)
	}
	active := m.enabled != nil && m.enabled()
	emit("enabled", boolMetric(active))
	if !active {
		return
	}
	emit("dry_run", boolMetric(m.policy.DryRun))
	emit("days", float64(m.policy.Days))
	m.mu.Lock()
	attempt, success, up, result := m.lastAttempt, m.lastSuccess, m.lastUp, m.lastResult
	m.mu.Unlock()
	emit("last_attempt_seconds", attempt)
	emit("last_success_seconds", success)
	emit("last_run_up", boolMetric(up))
	emit("last_pruned_messages", float64(result.PrunedMessages))
	emit("last_pruned_bytes", float64(result.PrunedBytes))
	ctx, cancel := context.WithTimeout(context.Background(), api.InternalMailMonitoringTimeout)
	defer cancel()
	stats, err := m.source.RetentionStatistics(ctx, m.policy)
	emit("stats_up", boolMetric(err == nil))
	if err != nil {
		return
	}
	emit("retained_messages", float64(stats.RetainedMessages))
	emit("pruned_messages", float64(stats.PrunedMessages))
	emit("retained_bytes", float64(stats.RetainedBytes))
	emit("eligible_messages", float64(stats.EligibleMessages))
	emit("eligible_bytes", float64(stats.EligibleBytes))
	emit("oldest_eligible_seconds", stats.OldestEligibleSeconds)
}
