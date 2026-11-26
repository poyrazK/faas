package state

import (
	"context"
	"errors"
	"time"
)

// Stage alert rules (ADR-20261104120000 amendment 2026-11-22). A rule with an
// EnvironmentID belongs to that project environment. It can run only on a
// signal the stage has of its own, compared with a fixed threshold and
// answered by a webhook; any other stage rule stays paused. The
// alert_rules_environment_enabled_chk constraint holds the same rule.

// stageAlertRuleMetrics are the metrics a stage rule can run on: request
// metrics the gateway counts per environment host, and failed invocations,
// which carry their environment.
var stageAlertRuleMetrics = map[AlertMetric]bool{
	"request_count": true, "error_rate_pct": true, "latency_p50_ms": true, "latency_p95_ms": true,
	"latency_p99_ms": true, "cold_start_pct": true, "failed_invocations": true,
}

// ErrStageAlertRuleNotRunnable refuses enabling a stage rule that reads a
// signal stages do not have yet.
var ErrStageAlertRuleNotRunnable = errors.New("state: this alert rule cannot run in a stage")

// StageAlertRuleRunnable reports whether a stage rule can be enabled: its
// metric is one a stage counts, its comparison a fixed threshold and its
// action a webhook.
func StageAlertRuleRunnable(r AlertRule) bool {
	switch r.Comparison {
	case "gt", "gte", "lt", "lte":
	default:
		return false
	}
	return stageAlertRuleMetrics[r.Metric] && (r.Action == "" || r.Action == "webhook") && r.AppID != ""
}

// StageAlertRuleMetrics lists the metrics StageAlertRuleRunnable accepts.
func StageAlertRuleMetrics() []AlertMetric {
	out := make([]AlertMetric, 0, len(stageAlertRuleMetrics))
	for _, m := range []AlertMetric{"request_count", "error_rate_pct", "latency_p50_ms", "latency_p95_ms", "latency_p99_ms", "cold_start_pct", "failed_invocations"} {
		if stageAlertRuleMetrics[m] {
			out = append(out, m)
		}
	}
	return out
}

// AlertRuleEnvironmentStore reads a stage's own failed invocations for its
// alert rules.
type AlertRuleEnvironmentStore interface {
	// CountFailedInvocationsInEnvironmentSince is CountFailedInvocationsSince
	// over one app's invocations owned by environmentID.
	CountFailedInvocationsInEnvironmentSince(ctx context.Context, accountID, appID, environmentID string, source InvocationSource, since time.Time) (int, error)
}

func (m *MemStore) CountFailedInvocationsInEnvironmentSince(_ context.Context, accountID, appID, environmentID string, source InvocationSource, since time.Time) (int, error) {
	if appID == "" || environmentID == "" {
		return 0, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, inv := range m.invocations {
		if inv.AccountID != accountID || inv.AppID != appID || inv.EnvironmentID != environmentID {
			continue
		}
		if inv.State != InvocationFailed && inv.State != InvocationDeadLetter {
			continue
		}
		failedAt := inv.CreatedAt
		if inv.CompletedAt != nil {
			failedAt = *inv.CompletedAt
		}
		if failedAt.Before(since) || source != "" && inv.Source != source {
			continue
		}
		n++
	}
	return n, nil
}

func (s *PgStore) CountFailedInvocationsInEnvironmentSince(ctx context.Context, accountID, appID, environmentID string, source InvocationSource, since time.Time) (int, error) {
	if appID == "" || environmentID == "" {
		return 0, ErrInvalidArgument
	}
	var sourceArg any
	if source != "" {
		sourceArg = string(source)
	}
	var n int
	if err := s.pool.QueryRow(ctx, `
		select count(*) from invocations
		 where account_id = $1::uuid and app_id = $2::uuid and environment_id = $3::uuid
		   and state in ('failed', 'dead_letter')
		   and coalesce(completed_at, created_at) >= $4
		   and ($5::text is null or source = $5::text)`,
		accountID, appID, environmentID, since.UTC(), sourceArg).Scan(&n); err != nil {
		return 0, mapErr(err)
	}
	return n, nil
}
