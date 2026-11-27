package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Stage alert rules (ADR-20261104120000 amendment 2026-11-22). A rule with an
// EnvironmentID belongs to that project environment. It can run only on a
// signal the stage has of its own, compared with a fixed threshold and
// answered by a webhook; any other stage rule stays paused. The
// alert_rules_environment_enabled_chk constraint holds the same rule.

// stageAlertRuleMetrics are the metrics a stage rule can run on: request
// metrics the gateway counts per stage host, and failed invocations, which
// carry their environment.
var stageAlertRuleMetrics = []AlertMetric{
	"request_count", "error_rate_pct", "latency_p50_ms", "latency_p95_ms", "latency_p99_ms", "cold_start_pct", "failed_invocations",
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
	if r.AppID == "" || r.Action != "" && r.Action != "webhook" {
		return false
	}
	for _, m := range stageAlertRuleMetrics {
		if r.Metric == m {
			return true
		}
	}
	return false
}

// StageAlertRuleMetrics lists the metrics StageAlertRuleRunnable accepts.
func StageAlertRuleMetrics() []AlertMetric {
	return append([]AlertMetric(nil), stageAlertRuleMetrics...)
}

// AlertRuleEnvironmentStore keeps production's and each stage's alert rules
// to their own failed invocations.
type AlertRuleEnvironmentStore interface {
	// CountFailedProductionInvocationsSince is CountFailedInvocationsSince
	// over production's invocations only; appID "" counts every app.
	CountFailedProductionInvocationsSince(ctx context.Context, accountID, appID string, source InvocationSource, since time.Time) (int, error)
	// CountFailedInvocationsInEnvironmentSince is CountFailedInvocationsSince
	// over one app's invocations owned by environmentID.
	CountFailedInvocationsInEnvironmentSince(ctx context.Context, accountID, appID, environmentID string, source InvocationSource, since time.Time) (int, error)
}

func (m *MemStore) CountFailedProductionInvocationsSince(_ context.Context, accountID, appID string, source InvocationSource, since time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.countFailedInvocationsLocked(accountID, appID, since, func(inv Invocation) bool {
		return inv.EnvironmentID == "" && (source == "" || inv.Source == source) && !m.rehearsalAlertExcludedLocked(appID, inv.AppID)
	}), nil
}

func (m *MemStore) CountFailedInvocationsInEnvironmentSince(_ context.Context, accountID, appID, environmentID string, source InvocationSource, since time.Time) (int, error) {
	if appID == "" || environmentID == "" {
		return 0, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.countFailedInvocationsLocked(accountID, appID, since, func(inv Invocation) bool {
		return inv.EnvironmentID == environmentID && (source == "" || inv.Source == source)
	}), nil
}

// countFailedInvocationsLocked counts the account's terminal failures since
// since, of appID ("" = every app), that keep accepts.
func (m *MemStore) countFailedInvocationsLocked(accountID, appID string, since time.Time, keep func(Invocation) bool) int {
	n := 0
	for _, inv := range m.invocations {
		if inv.AccountID != accountID || appID != "" && inv.AppID != appID {
			continue
		}
		if inv.State != InvocationFailed && inv.State != InvocationDeadLetter {
			continue
		}
		failedAt := inv.CreatedAt
		if inv.CompletedAt != nil {
			failedAt = *inv.CompletedAt
		}
		if failedAt.Before(since) || !keep(inv) {
			continue
		}
		n++
	}
	return n
}

func (s *PgStore) CountFailedProductionInvocationsSince(ctx context.Context, accountID, appID string, source InvocationSource, since time.Time) (int, error) {
	account, accountErr := alertPgUUID(accountID)
	app, appErr := alertPgUUID(appID)
	if accountErr != nil || appErr != nil || !account.Valid {
		return 0, ErrInvalidArgument
	}
	n, err := sqlc.New().CountFailedProductionInvocationsSince(ctx, s.pool, sqlc.CountFailedProductionInvocationsSinceParams{
		AccountID: account, AppID: app, Source: invocationSourceText(source), Since: pgtype.Timestamptz{Time: since.UTC(), Valid: true}})
	if err != nil {
		return 0, mapErr(err)
	}
	return int(n), nil
}

func (s *PgStore) CountFailedInvocationsInEnvironmentSince(ctx context.Context, accountID, appID, environmentID string, source InvocationSource, since time.Time) (int, error) {
	account, accountErr := alertPgUUID(accountID)
	app, appErr := alertPgUUID(appID)
	environment, envErr := alertPgUUID(environmentID)
	if accountErr != nil || appErr != nil || envErr != nil || !account.Valid || !app.Valid || !environment.Valid {
		return 0, ErrInvalidArgument
	}
	n, err := sqlc.New().CountFailedInvocationsInEnvironmentSince(ctx, s.pool, sqlc.CountFailedInvocationsInEnvironmentSinceParams{
		AccountID: account, AppID: app, EnvironmentID: environment, Source: invocationSourceText(source),
		Since: pgtype.Timestamptz{Time: since.UTC(), Valid: true}})
	if err != nil {
		return 0, mapErr(err)
	}
	return int(n), nil
}

func invocationSourceText(source InvocationSource) pgtype.Text {
	return pgtype.Text{String: string(source), Valid: source != ""}
}
