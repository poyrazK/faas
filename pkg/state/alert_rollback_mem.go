package state

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ AlertRollbackStore = (*MemStore)(nil)

func cloneAlertRollback(r api.AlertRollback) api.AlertRollback {
	raw, _ := json.Marshal(r)
	var out api.AlertRollback
	_ = json.Unmarshal(raw, &out)
	return out
}
func (m *MemStore) alertRollbackFactsLocked(rule AlertRule) alertRollbackFacts {
	app := m.apps[rule.AppID]
	f := alertRollbackFacts{RuleID: rule.ID, AccountID: rule.AccountID, AppID: rule.AppID, AppAccountID: app.AccountID, Enabled: rule.Enabled, Action: rule.Action, Metric: rule.Metric, Name: rule.Name, Service: app.Manifest.ExecutionMode == api.ExecutionModeService, WindowSeconds: rule.PostDeployRollbackWindowSeconds}
	f.Comparison, f.Threshold, f.WindowSpec = rule.Comparison, rule.Threshold, rule.WindowSpec
	if app.Status == AppDeleted {
		f.AppAccountID = ""
	}
	for _, d := range m.deployments {
		if d.AppID == rule.AppID && (d.Status == DeployLive || d.Status == DeploySuperseded) {
			f.Deployments = append(f.Deployments, alertRollbackDeployment{ID: d.ID, AppID: d.AppID, Scope: d.Scope, Status: d.Status, TrafficPercent: d.TrafficPercent, CanaryTotalSteps: d.CanaryTotalSteps, CanaryStep: d.CanaryStep, RolloutState: d.RolloutState, CreatedAt: d.CreatedAt, PredecessorID: d.ServiceRolloutHandoff.PredecessorDeploymentID, RecoveryPredecessorID: m.recoveryPredecessors[d.ID], CompletedAt: d.RolloutCompletedAt, Recovered: m.recoveredDeploymentLocked(d.ID)})
		}
	}
	return f
}
func (m *MemStore) captureAlertRollbackLocked(id string, rule AlertRule, observed float64, at time.Time) {
	if rule.Action != AlertActionRollback || !alertRollbackMetricAllowed(rule.Metric) {
		return
	}
	if m.alertRollbacks == nil {
		m.alertRollbacks = map[string]api.AlertRollback{}
	}
	r := captureAlertRollback(id, m.alertRollbackFactsLocked(rule), observed, at)
	m.alertRollbacks[r.ID] = r
}
func (m *MemStore) ReadAlertRollback(_ context.Context, id string) (api.AlertRollback, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.alertRollbacks[alertRollbackFireID(id)]
	if !ok {
		return r, ErrNotFound
	}
	return cloneAlertRollback(r), nil
}
func (m *MemStore) GetAlertRollback(_ context.Context, acct, app, id string) (api.AlertRollback, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.alertRollbacks[alertRollbackFireID(id)]
	a := m.apps[app]
	if !ok || r.AppID != app || r.AccountID != acct || a.AccountID != acct || a.Status == AppDeleted {
		return api.AlertRollback{}, ErrNotFound
	}
	return cloneAlertRollback(r), nil
}
func (m *MemStore) listAlertRollbacksLocked(acct, app string, pending bool) []api.AlertRollback {
	out := []api.AlertRollback{}
	for _, r := range m.alertRollbacks {
		if pending && alertRollbackTerminal(r) || !pending && (r.AppID != app || r.AccountID != acct) {
			continue
		}
		out = append(out, cloneAlertRollback(r))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].ID < out[j].ID
		}
		if pending {
			return out[i].UpdatedAt.Before(out[j].UpdatedAt)
		}
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	if len(out) > api.AlertRollbackBatchSize {
		out = out[:api.AlertRollbackBatchSize]
	}
	return out
}
func (m *MemStore) ListAlertRollbacks(_ context.Context, acct, app string) ([]api.AlertRollback, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.apps[app]
	if !ok || a.AccountID != acct || a.Status == AppDeleted {
		return []api.AlertRollback{}, nil
	}
	return m.listAlertRollbacksLocked(acct, app, false), nil
}
func (m *MemStore) ListPendingAlertRollbacks(context.Context) ([]api.AlertRollback, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.listAlertRollbacksLocked("", "", true), nil
}
func (m *MemStore) UpdateAlertRollback(_ context.Context, r api.AlertRollback, status, code string, blockers []api.BindingCheckFinding) error {
	if status != "blocked" && status != "failed" {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current, ok := m.alertRollbacks[r.ID]
	if !ok {
		return ErrNotFound
	}
	if alertRollbackTerminal(current) || current.ServiceRequestID != r.ServiceRequestID || current.RollbackOperationID != r.RollbackOperationID {
		return nil
	}
	m.alertRollbacks[r.ID] = alertRollbackProgress(current, status, code, blockers)
	return nil
}
func (m *MemStore) CommitAlertRollback(ctx context.Context, r api.AlertRollback) (api.AlertRollback, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	current, ok := m.alertRollbacks[r.ID]
	if !ok {
		return r, ErrNotFound
	}
	if alertRollbackTerminal(current) {
		return cloneAlertRollback(current), nil
	}
	if current.ServiceRequestID != "" || current.RollbackOperationID != "" {
		return cloneAlertRollback(current), nil
	}
	rule, exists := m.alertRules[current.RuleID]
	if !exists {
		return r, ErrAlertRollbackChanged
	}
	fresh := captureAlertRollback(current.ID, m.alertRollbackFactsLocked(rule), current.ObservedValue, current.FiredAt)
	if !alertRollbackPairMatches(fresh, current) {
		return r, ErrAlertRollbackChanged
	}
	if current.Historical {
		return m.requestHistoricalAlertRollbackLocked(ctx, current)
	}
	if current.Service {
		return m.requestServiceAlertRollbackLocked(ctx, current)
	}
	audit := alertRollbackAudit(ctx, current)
	_, auditID, err := m.recoverRolloutLocked(ctx, current.AppID, current.CandidateDeploymentID, current.PredecessorDeploymentID, "abort", current.Reason, &audit)
	if err != nil {
		return r, err
	}
	current = completeAlertRollback(current, auditID)
	m.alertRollbacks[r.ID] = current
	return cloneAlertRollback(current), nil
}
