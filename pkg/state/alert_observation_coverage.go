package state

import (
	"cmp"
	"context"
	"slices"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

const alertObservationCoverageWindow = 24 * time.Hour

// AlertObservationCoverageStore reads coverage for the fetched rule version.
type AlertObservationCoverageStore interface {
	AlertObservationCoverage(context.Context, AlertRule, time.Time) (api.AlertObservationCoverage, error)
}

type alertObservationCheck struct {
	at         time.Time
	skipReason api.AlertAnomalyEvaluationReason
}

func observationSkipReason(reason api.AlertAnomalyEvaluationReason) api.AlertAnomalyEvaluationReason {
	if anomalySkippedState(reason) != "" {
		return reason
	}
	return ""
}

func appendAlertObservationCheck(checks []alertObservationCheck, at time.Time, reason api.AlertAnomalyEvaluationReason) []alertObservationCheck {
	cutoff := at.Add(-alertObservationCoverageWindow)
	for len(checks) > 0 && !checks[0].at.After(cutoff) {
		checks = checks[1:]
	}
	return append(checks, alertObservationCheck{at: at, skipReason: observationSkipReason(reason)})
}

func (s *PgStore) AlertObservationCoverage(ctx context.Context, rule AlertRule, asOf time.Time) (api.AlertObservationCoverage, error) {
	id, err := alertPgUUID(rule.ID)
	if err != nil {
		return api.AlertObservationCoverage{}, err
	}
	rows, err := sqlc.New().ListCustomerAlertObservationCoverage(ctx, s.pool, sqlc.ListCustomerAlertObservationCoverageParams{
		ID:                id,
		CoverageCutoff:    pgtype.Timestamptz{Time: asOf.Add(-alertObservationCoverageWindow), Valid: true},
		ExpectedUpdatedAt: pgtype.Timestamptz{Time: rule.UpdatedAt, Valid: true},
		AsOf:              pgtype.Timestamptz{Time: asOf, Valid: true},
	})
	if err != nil {
		return api.AlertObservationCoverage{}, err
	}
	var coverage api.AlertObservationCoverage
	for _, row := range rows {
		addAlertObservationCoverage(&coverage, api.AlertAnomalyEvaluationReason(row.SkipReason), int(row.ChecksInWindow), row.LastCheckedAt.Time)
	}
	return coverage, nil
}

func (m *MemStore) AlertObservationCoverage(_ context.Context, rule AlertRule, asOf time.Time) (api.AlertObservationCoverage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	current, ok := m.alertRules[rule.ID]
	var coverage api.AlertObservationCoverage
	if !ok || !current.ObserveOnly || !current.UpdatedAt.Equal(rule.UpdatedAt) {
		return coverage, nil
	}
	cutoff := asOf.Add(-alertObservationCoverageWindow)
	skips := map[api.AlertAnomalyEvaluationReason]int{}
	for _, check := range current.observationChecks {
		if check.at.After(asOf) {
			break
		}
		coverage.LastCheckedAt = check.at
		if check.at.After(cutoff) {
			skips[check.skipReason]++
		}
	}
	for reason, count := range skips {
		addAlertObservationCoverage(&coverage, reason, count, coverage.LastCheckedAt)
	}
	slices.SortFunc(coverage.Skipped, func(a, b api.AlertObservationSkipCount) int { return cmp.Compare(a.Reason, b.Reason) })
	return coverage, nil
}

func addAlertObservationCoverage(coverage *api.AlertObservationCoverage, reason api.AlertAnomalyEvaluationReason, count int, lastCheckedAt time.Time) {
	if lastCheckedAt.After(coverage.LastCheckedAt) {
		coverage.LastCheckedAt = lastCheckedAt
	}
	coverage.Attempted += count
	if reason == "" {
		coverage.Completed += count
	} else if count != 0 {
		coverage.Skipped = append(coverage.Skipped, api.AlertObservationSkipCount{Reason: reason, Count: count})
	}
}
