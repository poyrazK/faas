package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// AlertAnomalyRecoveryStore closes the live incident only while its confirmed
// healthy evaluation is still current.
type AlertAnomalyRecoveryStore interface {
	AlertAnomalyEvaluationStore
	AlertRuleEpisodeOpen(ctx context.Context, ruleID string) (bool, error)
	ResolveConfirmedAlertAnomaly(ctx context.Context, ruleID string, evaluatedAt time.Time) (bool, error)
}

var (
	_ AlertAnomalyRecoveryStore = (*PgStore)(nil)
	_ AlertAnomalyRecoveryStore = (*MemStore)(nil)
)

func validAlertRecoveryConfirmation(rule AlertRule) bool {
	return rule.RecoveryConfirmationChecks == 1 ||
		(rule.RecoveryConfirmationChecks == api.AnomalyReplayRecoveryChecks &&
			rule.AppID != "" && api.IsAnomalyAlertComparison(string(rule.Comparison)))
}

// ConfirmAlertAnomalyRecovery advances the streak after a healthy check on
// an open incident. The latest recorded evaluation is the durable checkpoint.
func ConfirmAlertAnomalyRecovery(rule AlertRule, evidence api.AlertAnomalyEvaluation) api.AlertAnomalyEvaluation {
	if rule.RecoveryConfirmationChecks != api.AnomalyReplayRecoveryChecks {
		return evidence
	}
	evidence.Reason = api.AlertAnomalyRecoveryPending
	evidence.Count = 1
	evidence.RequiredCount = int64(rule.RecoveryConfirmationChecks)
	previous := rule.AnomalyEvaluation
	if previous.EvaluatedAt.Equal(rule.LastEvaluatedAt) &&
		(previous.Reason == api.AlertAnomalyRecoveryPending || previous.Reason == api.AlertAnomalyRecoveryConfirmed) {
		evidence.Reason = api.AlertAnomalyRecoveryConfirmed
		evidence.Count = evidence.RequiredCount
	}
	return evidence
}

func (s *PgStore) ResolveConfirmedAlertAnomaly(ctx context.Context, ruleID string, at time.Time) (bool, error) {
	at = at.UTC().Truncate(time.Microsecond)
	return s.setAlertRuleState(ctx, ruleID, AlertStateOk, at, &at)
}

func (m *MemStore) ResolveConfirmedAlertAnomaly(_ context.Context, ruleID string, at time.Time) (bool, error) {
	at = at.UTC().Truncate(time.Microsecond)
	return m.setAlertRuleState(ruleID, AlertStateOk, at, &at)
}
