// adr: 602 — alert fire outbox pins exact canary rollback intent.
package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var ErrAlertRollbackChanged = errors.New("state: alert rollback selection changed")

type AlertRollbackStore interface {
	ReadAlertRollback(context.Context, string) (api.AlertRollback, error)
	GetAlertRollback(context.Context, string, string, string) (api.AlertRollback, error)
	ListAlertRollbacks(context.Context, string, string) ([]api.AlertRollback, error)
	ListPendingAlertRollbacks(context.Context) ([]api.AlertRollback, error)
	UpdateAlertRollback(context.Context, api.AlertRollback, string, string, []api.BindingCheckFinding) error
	CommitAlertRollback(context.Context, api.AlertRollback) (api.AlertRollback, error)
}

type ServiceAlertRollbackStore interface {
	RefreshServiceAlertRollback(context.Context, api.AlertRollback) (api.AlertRollback, error)
}

type alertRollbackDeployment struct {
	ID                    string           `json:"id"`
	AppID                 string           `json:"app_id"`
	Scope                 string           `json:"scope"`
	Status                DeploymentStatus `json:"status"`
	TrafficPercent        int              `json:"traffic_percent"`
	CanaryTotalSteps      int              `json:"canary_total_steps"`
	CanaryStep            int              `json:"canary_step"`
	RolloutState          string           `json:"rollout_state"`
	CreatedAt             time.Time        `json:"created_at"`
	PredecessorID         string           `json:"predecessor_deployment_id"`
	RecoveryPredecessorID string           `json:"recovery_predecessor_id"`
	CompletedAt           *time.Time       `json:"completed_at"`
	Recovered             bool             `json:"recovered"`
}
type alertRollbackFacts struct {
	Comparison    AlertComparison           `json:"comparison"`
	Threshold     float64                   `json:"threshold"`
	WindowSpec    AlertWindowSpec           `json:"window_spec"`
	WindowSeconds int                       `json:"window_seconds"`
	RuleID        string                    `json:"rule_id"`
	AccountID     string                    `json:"account_id"`
	AppID         string                    `json:"app_id"`
	AppAccountID  string                    `json:"app_account_id"`
	Enabled       bool                      `json:"enabled"`
	Action        AlertAction               `json:"action"`
	Metric        AlertMetric               `json:"metric"`
	Name          string                    `json:"name"`
	Service       bool                      `json:"service"`
	Deployments   []alertRollbackDeployment `json:"deployments"`
}

func alertRollbackActive(d alertRollbackDeployment) bool {
	return d.Status == DeployLive && d.CanaryTotalSteps > 0 && d.CanaryStep < d.CanaryTotalSteps && (d.RolloutState == "pending" || d.RolloutState == "rolling_out")
}
func alertRollbackMetricAllowed(metric AlertMetric) bool {
	return !api.IsEventRecoveryAlertMetric(string(metric)) && !api.IsEventConsumerAlertMetric(string(metric)) && metric != AlertMetricPreAuthTargetThreshold && metric != AlertMetricPreAuthTargetSignalGapPct
}
func alertRollbackFireID(id string) string {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return id
	}
	return parsed.String()
}
func captureAlertRollback(id string, f alertRollbackFacts, observed float64, at time.Time) api.AlertRollback {
	r := api.AlertRollback{ID: alertRollbackFireID(id), RuleID: f.RuleID, AccountID: f.AccountID, AppID: f.AppID, Status: "pending", ObservedValue: observed, FiredAt: at.UTC(), UpdatedAt: at.UTC(), Reason: normalizeRolloutReason(fmt.Sprintf("alert %q fired for %s (observed %.6g)", f.Name, f.Metric, observed))}
	fail := func(code string) api.AlertRollback { r.Status, r.Code = "failed", code; return r }
	if !f.Enabled || f.Action != AlertActionRollback || !alertRollbackMetricAllowed(f.Metric) || f.AppID == "" || f.AppAccountID != f.AccountID {
		return fail("alert_rollback_rule_invalid")
	}
	r.Service = f.Service
	var candidate alertRollbackDeployment
	for _, d := range f.Deployments {
		active := alertRollbackActive(d)
		if f.Service {
			active = d.Status == DeployLive && d.CanaryTotalSteps == 0 && d.RolloutState == "rolling_out"
		}
		if d.AppID != f.AppID || !active {
			continue
		}
		if candidate.ID != "" {
			return fail("alert_rollback_target_ambiguous")
		}
		candidate = d
	}
	if candidate.ID == "" {
		return captureHistoricalAlertRollback(r, f)
	}
	r.CandidateDeploymentID, r.Scope = candidate.ID, normalizedDeploymentScope(candidate.Scope)
	if f.Service {
		return captureServiceAlertPredecessor(r, f, candidate)
	}
	var predecessor alertRollbackDeployment
	for _, d := range f.Deployments {
		if d.ID == candidate.ID || d.AppID != f.AppID || d.Status != DeployLive || d.TrafficPercent <= 0 || normalizedDeploymentScope(d.Scope) != r.Scope {
			continue
		}
		if predecessor.ID != "" {
			return fail("alert_rollback_target_ambiguous")
		}
		if !d.CreatedAt.Before(candidate.CreatedAt) || alertRollbackActive(d) {
			return fail("alert_rollback_target_unavailable")
		}
		predecessor = d
	}
	if predecessor.ID == "" {
		return fail("alert_rollback_target_unavailable")
	}
	r.PredecessorDeploymentID = predecessor.ID
	return r
}

func captureServiceAlertPredecessor(r api.AlertRollback, f alertRollbackFacts, candidate alertRollbackDeployment) api.AlertRollback {
	fail := func(code string) api.AlertRollback { r.Status, r.Code = "failed", code; return r }
	for _, d := range f.Deployments {
		if d.ID == candidate.ID || d.AppID != f.AppID || d.Status != DeployLive || normalizedDeploymentScope(d.Scope) != r.Scope {
			continue
		}
		if candidate.PredecessorID != "" && d.ID != candidate.PredecessorID {
			if d.TrafficPercent > 0 {
				return fail("alert_rollback_target_ambiguous")
			}
			continue
		}
		if candidate.PredecessorID == "" && d.TrafficPercent <= 0 {
			continue
		}
		if r.PredecessorDeploymentID != "" {
			return fail("alert_rollback_target_ambiguous")
		}
		if !d.CreatedAt.Before(candidate.CreatedAt) || d.CanaryTotalSteps > 0 || d.RolloutState == "rolling_out" {
			return fail("alert_rollback_target_unavailable")
		}
		r.PredecessorDeploymentID = d.ID
	}
	if r.PredecessorDeploymentID == "" {
		return fail("alert_rollback_target_unavailable")
	}
	return r
}

func alertRollbackPairMatches(fresh, current api.AlertRollback) bool {
	return fresh.Status == "pending" && fresh.AppID == current.AppID && fresh.AccountID == current.AccountID && fresh.Service == current.Service && fresh.Historical == current.Historical && fresh.CandidateDeploymentID == current.CandidateDeploymentID && fresh.PredecessorDeploymentID == current.PredecessorDeploymentID && fresh.Scope == current.Scope && historicalAlertEvidenceMatches(fresh, current)
}
func alertRollbackTerminal(r api.AlertRollback) bool {
	return r.Status == "complete" || r.Status == "failed"
}
func decodeAlertRollback(raw []byte, err error) (api.AlertRollback, error) {
	var r api.AlertRollback
	if err != nil {
		return r, mapErr(err)
	}
	err = json.Unmarshal(raw, &r)
	return r, err
}
func alertRollbackProgress(r api.AlertRollback, status, code string, blockers []api.BindingCheckFinding) api.AlertRollback {
	gate := blockedServiceBindingGate(&api.ServiceRolloutBindingGate{}, code, blockers)
	r.Status, r.Code, r.Blockers, r.UpdatedAt = status, gate.Code, gate.Blockers, time.Now().UTC()
	if code == "alert_rollback_evidence_expired" && r.RollbackOperationID == "" && r.DeploymentEvidence != nil {
		e := *r.DeploymentEvidence
		e.Status, e.Code, e.CheckedAt = "expired", code, &r.UpdatedAt
		r.DeploymentEvidence = &e
	}
	return r
}
func completeAlertRollback(r api.AlertRollback, auditID int64) api.AlertRollback {
	r = alertRollbackProgress(r, "complete", "", nil)
	r.CompletedAt, r.AuditID = &r.UpdatedAt, strconv.FormatInt(auditID, 10)
	return r
}
func alertRollbackAudit(ctx context.Context, r api.AlertRollback) DeploymentAudit {
	acct, rule := uuid.MustParse(r.AccountID), uuid.MustParse(r.RuleID)
	data := json.RawMessage(rolloutRecoveryAuditData(r.Reason, r.PredecessorDeploymentID, bindingReleaseFences(ctx)))
	var fields map[string]any
	_ = json.Unmarshal(data, &fields)
	fields["alert_fire_id"] = r.ID
	fields["observed_value"], fields["fired_at"] = r.ObservedValue, r.FiredAt
	data, _ = json.Marshal(fields)
	return DeploymentAudit{DeploymentID: uuid.MustParse(r.CandidateDeploymentID), AccountID: &acct, AlertRuleID: &rule, Kind: DeployRolledBack, Actor: "apid:alert_rollback", At: time.Now().UTC(), Data: data}
}
