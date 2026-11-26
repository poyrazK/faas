package state

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// AlertAnomalyEvaluationStore stamps the latest anomaly evaluation and its
// explanation together. The passed rule is the version the evaluator read.
type AlertAnomalyEvaluationStore interface {
	RecordAlertAnomalyEvaluation(context.Context, AlertRule, time.Time, api.AlertAnomalyEvaluation) (bool, error)
}

var (
	_ AlertAnomalyEvaluationStore = (*PgStore)(nil)
	_ AlertAnomalyEvaluationStore = (*MemStore)(nil)
)

func anomalySkippedState(reason api.AlertAnomalyEvaluationReason) AlertState {
	switch reason {
	case api.AlertAnomalyInsufficientTraffic, api.AlertAnomalyInsufficientHistory:
		return AlertStateUnknown
	case api.AlertAnomalyTelemetryMissing, api.AlertAnomalyTelemetryUnavailable:
		return AlertStateDegraded
	default:
		return ""
	}
}

func (s *PgStore) RecordAlertAnomalyEvaluation(ctx context.Context, rule AlertRule, at time.Time, evidence api.AlertAnomalyEvaluation) (bool, error) {
	id, err := alertPgUUID(rule.ID)
	if err != nil {
		return false, err
	}
	// PostgreSQL stores timestamps at microsecond precision. Match the JSON
	// timestamp to the row so the read can reject mismatched evaluation stamps.
	at = at.UTC().Truncate(time.Microsecond)
	var payload []byte
	if evidence.Reason != "" {
		evidence.EvaluatedAt = at
		payload, err = json.Marshal(evidence)
		if err != nil {
			return false, err
		}
	}
	skippedState := anomalySkippedState(evidence.Reason)
	rows, err := sqlc.New().RecordCustomerAlertAnomalyEvaluation(ctx, s.pool, sqlc.RecordCustomerAlertAnomalyEvaluationParams{
		ID: id, EvaluatedAt: pgtype.Timestamptz{Time: at, Valid: true},
		ExpectedUpdatedAt:       pgtype.Timestamptz{Time: rule.UpdatedAt, Valid: true},
		ExpectedLastEvaluatedAt: pgtype.Timestamptz{Time: rule.LastEvaluatedAt, Valid: !rule.LastEvaluatedAt.IsZero()},
		Evidence:                payload, SkippedState: pgtype.Text{String: string(skippedState), Valid: skippedState != ""},
	})
	return rows > 0, err
}

func (m *MemStore) RecordAlertAnomalyEvaluation(_ context.Context, rule AlertRule, at time.Time, evidence api.AlertAnomalyEvaluation) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	current, ok := m.alertRules[rule.ID]
	at = at.UTC().Truncate(time.Microsecond)
	if !ok || !current.Enabled || current.ObserveOnly || !api.IsAnomalyAlertComparison(string(current.Comparison)) ||
		!current.UpdatedAt.Equal(rule.UpdatedAt) || !at.After(current.LastEvaluatedAt) {
		return false, nil
	}
	if current.RecoveryConfirmationChecks > 1 && !current.LastEvaluatedAt.Equal(rule.LastEvaluatedAt) {
		return false, nil
	}
	if evidence.Reason != "" {
		evidence.EvaluatedAt = at
	}
	current.LastEvaluatedAt = at
	current.AnomalyEvaluation = evidence
	if skippedState := anomalySkippedState(evidence.Reason); skippedState != "" && current.State != skippedState {
		current.State = skippedState
		if at.After(current.UpdatedAt) {
			current.UpdatedAt = at
		}
	}
	m.alertRules[rule.ID] = current
	return true, nil
}
