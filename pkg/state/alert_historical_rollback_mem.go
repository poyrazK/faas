package state

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var _ HistoricalAlertRollbackStore = (*MemStore)(nil)

func (m *MemStore) recordRecoveryPredecessorLocked(d Deployment) {
	var predecessor string
	for _, other := range m.deployments {
		if other.AppID != d.AppID || normalizedDeploymentScope(other.Scope) != normalizedDeploymentScope(d.Scope) || other.Status != DeployLive || other.TrafficPercent <= 0 {
			continue
		}
		if predecessor != "" || other.TrafficPercent != 100 || other.RolloutState != "complete" || other.CanaryTotalSteps > 0 && other.CanaryStep < other.CanaryTotalSteps {
			return
		}
		predecessor = other.ID
	}
	if predecessor != "" {
		if m.recoveryPredecessors == nil {
			m.recoveryPredecessors = map[string]string{}
		}
		m.recoveryPredecessors[d.ID] = predecessor
	}
}
func (m *MemStore) recoveredDeploymentLocked(id string) bool {
	for _, op := range m.checkedRollbacks {
		if op.TargetDeploymentID == id {
			return true
		}
	}
	for _, r := range m.alertRollbacks {
		if r.PredecessorDeploymentID == id && r.Status == "complete" {
			return true
		}
	}
	canonical := id
	if parsed, err := uuid.Parse(id); err == nil {
		canonical = parsed.String()
	}
	for _, a := range m.deploymentAudit {
		if a.Kind == DeployRolledBack {
			var data struct {
				PredecessorID string `json:"predecessor_deployment_id"`
			}
			_ = json.Unmarshal(a.Data, &data)
			if a.DeploymentID.String() == canonical || data.PredecessorID == id {
				return true
			}
		}
	}
	return false
}
func (m *MemStore) requestHistoricalAlertRollbackLocked(_ context.Context, r api.AlertRollback) (api.AlertRollback, error) {
	f := m.alertRollbackFactsLocked(m.alertRules[r.RuleID])
	if !historicalAlertWindowOpen(r, f, time.Now().UTC()) || m.alertHistoricalClaims[r.CandidateDeploymentID] != "" {
		return r, ErrAlertRollbackChanged
	}
	// ADR-127 request telemetry is PostgreSQL-only. An in-memory store must
	// never treat an app-wide alert value as evidence for this deployment.
	r, _ = qualifyHistoricalAlertEvidence(r, 0, 0, nil, false, time.Now().UTC())
	m.alertRollbacks[r.ID] = r
	return cloneAlertRollback(r), nil
}
func (m *MemStore) RefreshHistoricalAlertRollback(ctx context.Context, snapshot api.AlertRollback) (api.AlertRollback, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.alertRollbacks[snapshot.ID]
	if !ok {
		return r, ErrNotFound
	}
	if alertRollbackTerminal(r) {
		return cloneAlertRollback(r), nil
	}
	if !r.Historical || r.RollbackOperationID == "" || r.AppID != snapshot.AppID || r.AccountID != snapshot.AccountID {
		return r, ErrAlertRollbackChanged
	}
	r = projectHistoricalAlertRollback(r, m.checkedRollbacks[r.RollbackOperationID], m.deployments[r.CandidateDeploymentID], m.deployments[r.PredecessorDeploymentID])
	if r.Status == "complete" {
		id, err := m.appendDeploymentAuditLocked(historicalAlertAudit(ctx, r, "complete"))
		if err != nil {
			return r, err
		}
		r = completeAlertRollback(r, id)
	}
	m.alertRollbacks[r.ID] = r
	return cloneAlertRollback(r), nil
}
