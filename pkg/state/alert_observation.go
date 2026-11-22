package state

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type AlertObservationStore interface {
	RecordAlertObservation(context.Context, AlertRule, time.Time, api.AlertAnomalyEvaluation) (bool, error)
}

var (
	_ AlertObservationStore = (*PgStore)(nil)
	_ AlertObservationStore = (*MemStore)(nil)
)

func validAlertObservationMode(rule AlertRule) bool {
	if rule.EnvironmentScope != "" {
		return rule.AppID != "" && api.IsAnomalyAlertComparison(string(rule.Comparison)) &&
			api.SupportsEnvironmentAnomalyMetric(string(rule.Metric)) &&
			(rule.ObserveOnly || rule.Action == "" || rule.Action == AlertActionWebhook) &&
			(rule.EnvironmentScope == api.DefaultEnvScope || api.ValidProjectEnvironmentSlug(rule.EnvironmentScope))
	}
	return !rule.ObserveOnly || (rule.AppID != "" && api.IsAnomalyAlertComparison(string(rule.Comparison)))
}

// Scope edits during observation keep a live incident and its delivery retries
// tied to their original environment until the rule changes mode.
func alertScopeEditAllowed(rule AlertRule, patch UpdateAlertRuleParams, observeOnly bool) bool {
	return patch.EnvironmentScope == nil || *patch.EnvironmentScope == rule.EnvironmentScope || rule.ObserveOnly || observeOnly
}

func alertFireMatchesScope(payload []byte, scope string) bool {
	var envelope struct {
		EnvironmentScope string `json:"environment_scope"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return scope == ""
	}
	return envelope.EnvironmentScope == scope
}

func observationEndsOnEdit(rule AlertRule, patch UpdateAlertRuleParams, observeOnly, enabled bool) bool {
	return rule.ObserveOnly && (!observeOnly || !enabled || observationConditionChanged(rule, patch))
}

func observationConditionChanged(rule AlertRule, patch UpdateAlertRuleParams) bool {
	return patch.EnvironmentScope != nil && *patch.EnvironmentScope != rule.EnvironmentScope ||
		patch.Metric != nil && *patch.Metric != rule.Metric ||
		patch.Comparison != nil && *patch.Comparison != rule.Comparison ||
		patch.WindowSpec != nil && *patch.WindowSpec != rule.WindowSpec ||
		patch.Threshold != nil && *patch.Threshold != rule.Threshold
}

func endObservedIncident(incidents []api.AlertObservedIncident, at time.Time, outcome string) []api.AlertObservedIncident {
	if len(incidents) == 0 || incidents[0].EndedAt != nil {
		return incidents
	}
	if at.Before(incidents[0].OpenedAt) {
		at = incidents[0].OpenedAt
	}
	incidents = slices.Clone(incidents)
	incidents[0].EndedAt = &at
	incidents[0].Outcome = outcome
	return incidents
}

// ApplyAlertObservation advances newest-first incident history by one check.
// The caller supplies a positive retention limit for the history it keeps.
func ApplyAlertObservation(history []api.AlertObservedIncident, limit int, at time.Time, cooldownMinutes int, reason api.AlertAnomalyEvaluationReason, environmentScope string) []api.AlertObservedIncident {
	if reason == api.AlertAnomalyObservedBreach {
		if len(history) != 0 && history[0].EndedAt == nil {
			cooldown := max(time.Duration(cooldownMinutes)*time.Minute, time.Minute)
			if !at.Before(history[0].LastEligibleAt.Add(cooldown)) {
				history = slices.Clone(history)
				history[0].EligibleNotifications++
				history[0].LastEligibleAt = at
			}
			return history
		}
		opened := api.AlertObservedIncident{EnvironmentScope: environmentScope, OpenedAt: at, EligibleNotifications: 1, LastEligibleAt: at}
		return append([]api.AlertObservedIncident{opened}, history[:min(len(history), limit-1)]...)
	}
	if anomalySkippedState(reason) != "" || reason == api.AlertAnomalyRecoveryPending {
		return history
	}
	return endObservedIncident(history, at, api.AlertObservationRecovered)
}

func marshalObservedIncidents(incidents []api.AlertObservedIncident) ([]byte, error) {
	if len(incidents) == 0 {
		return nil, nil
	}
	return json.Marshal(incidents)
}

func applyAlertRuleObservation(rule AlertRule, at time.Time, evidence api.AlertAnomalyEvaluation) []api.AlertObservedIncident {
	incidents := ApplyAlertObservation(rule.ObservedIncidents, api.AnomalyObservedIncidentLimit, at, rule.CooldownMinutes, evidence.Reason, rule.EnvironmentScope)
	if evidence.Reason == api.AlertAnomalyObservedBreach && incidents[0].OpenedAt.Equal(at) &&
		evidence.Observed != nil && evidence.Baseline != nil && evidence.TriggerValue != nil {
		opening := &api.AlertObservedIncidentOpeningEvidence{
			Metric: string(rule.Metric), Window: string(rule.WindowSpec), Comparison: string(rule.Comparison),
			Observed: *evidence.Observed, Baseline: *evidence.Baseline, TriggerValue: *evidence.TriggerValue,
		}
		if evidence.BaselineDays != nil {
			opening.BaselineDays = *evidence.BaselineDays
		}
		incidents[0].OpeningEvidence = opening
	}
	return incidents
}

func (s *PgStore) RecordAlertObservation(ctx context.Context, rule AlertRule, at time.Time, evidence api.AlertAnomalyEvaluation) (bool, error) {
	id, err := alertPgUUID(rule.ID)
	if err != nil {
		return false, err
	}
	at = at.UTC().Truncate(time.Microsecond)
	evidence = observeAlertRecovery(rule, evidence)
	evidence.EvaluatedAt = at
	payload, err := json.Marshal(evidence)
	if err != nil {
		return false, err
	}
	incidents, err := marshalObservedIncidents(applyAlertRuleObservation(rule, at, evidence))
	if err != nil {
		return false, err
	}
	n, err := sqlc.New().RecordCustomerAlertObservation(ctx, s.pool, sqlc.RecordCustomerAlertObservationParams{
		ID:                       id,
		EvaluatedAt:              pgtype.Timestamptz{Time: at, Valid: true},
		ExpectedUpdatedAt:        pgtype.Timestamptz{Time: rule.UpdatedAt, Valid: true},
		ExpectedEnvironmentScope: rule.EnvironmentScope,
		ExpectedLastEvaluatedAt:  pgtype.Timestamptz{Time: rule.LastEvaluatedAt, Valid: !rule.LastEvaluatedAt.IsZero()},
		Evidence:                 payload,
		Incidents:                incidents,
		SkipReason:               string(observationSkipReason(evidence.Reason)),
		CoverageCutoff:           pgtype.Timestamptz{Time: at.Add(-alertObservationCoverageWindow), Valid: true},
	})
	return n > 0, err
}

func (m *MemStore) RecordAlertObservation(_ context.Context, rule AlertRule, at time.Time, evidence api.AlertAnomalyEvaluation) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	current, ok := m.alertRules[rule.ID]
	at = at.UTC().Truncate(time.Microsecond)
	if !ok || !current.Enabled || !current.ObserveOnly || !rule.UpdatedAt.Equal(current.UpdatedAt) ||
		rule.EnvironmentScope != current.EnvironmentScope ||
		!rule.LastEvaluatedAt.Equal(current.LastEvaluatedAt) || !at.After(current.LastEvaluatedAt) {
		return false, nil
	}
	evidence = observeAlertRecovery(current, evidence)
	evidence.EvaluatedAt = at
	current.LastEvaluatedAt = at
	current.AnomalyEvaluation = evidence
	current.ObservedIncidents = applyAlertRuleObservation(current, at, evidence)
	current.observationChecks = appendAlertObservationCheck(current.observationChecks, at, evidence.Reason)
	m.alertRules[rule.ID] = current
	return true, nil
}

func observeAlertRecovery(rule AlertRule, evidence api.AlertAnomalyEvaluation) api.AlertAnomalyEvaluation {
	if evidence.Reason != api.AlertAnomalyObservedBreach && anomalySkippedState(evidence.Reason) == "" &&
		len(rule.ObservedIncidents) > 0 && rule.ObservedIncidents[0].EndedAt == nil {
		return ConfirmAlertAnomalyRecovery(rule, evidence)
	}
	return evidence
}
