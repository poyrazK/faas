package state

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/financial"
)

var _ FinancialPreviewCommandStore = (*MemStore)(nil)

func (m *MemStore) financialPreviewCommandHeldLocked(task AppTask, now time.Time) bool {
	app := m.apps[task.AppID]
	for _, h := range m.financialComputeHolds[app.AccountID] {
		p := m.financialBudgets[h.PolicyID]
		if p.DeletedAt == nil && p.Revision == h.Revision && p.Spec.Enabled && (p.Spec.Scope.Kind == "account" || p.Spec.Scope.Kind == "app" && p.Spec.Scope.ID == app.ID) && (p.Spec.ResumeRule == "manual" || h.PeriodStart.Equal(financial.ComputeMonth(now))) {
			return true
		}
	}
	if app.PreviewOfSlug == "" {
		return false
	}
	for _, stop := range m.financialBudgetStops {
		if !financialBudgetStopActive(stop, m.financialBudgets[stop.PolicyID], now) {
			continue
		}
		if stop.Matches(app, "") {
			return true
		}
		if stop.Spec.Scope.Kind == "environment" {
			owner, err := m.runtimeAppValueOwnerLocked(app.AccountID, app.ID, task.DeploymentID)
			if err == nil && stop.Matches(app, owner.EnvironmentID) {
				return true
			}
			dep := m.deployments[task.DeploymentID]
			env := m.projectEnvironments[stop.Spec.Scope.ID]
			if m.deploymentRuntimeEnvironmentOwners[dep.ID] == "" && env.AccountID == app.AccountID && env.ProjectID == app.ProjectID && env.Slug == workloadEnvironmentSlug(normalizedDeploymentScope(dep.Scope)) {
				return true
			}
		}
	}
	return false
}

func (m *MemStore) RegisterFinancialPreviewCommand(_ context.Context, r FinancialPreviewCommandRuntime) error {
	if !validFinancialPreviewCommand(r) {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.appTasks[r.TaskID]
	if t.AccountID != r.AccountID || t.AppID != r.AppID || t.DeploymentID != r.DeploymentID || t.Status != AppTaskRestoring || t.LeaseToken == nil || *t.LeaseToken != r.LeaseToken || t.LeaseExpiresAt == nil || !t.LeaseExpiresAt.After(r.RecordedAt) || m.apps[t.AppID].PreviewOfSlug == "" {
		return ErrConflict
	}
	if m.financialPreviewCommandHeldLocked(t, r.RecordedAt) {
		return ErrFinancialBudgetHeld
	}
	for _, old := range m.financialPreviewCommands {
		if old.TaskID == r.TaskID && old.Phase != "retired" {
			return ErrConflict
		}
	}
	if m.financialPreviewCommands == nil {
		m.financialPreviewCommands = map[string]FinancialPreviewCommandRuntime{}
	}
	r.Phase = "preparing"
	m.financialPreviewCommands[r.TaskID+":"+r.LeaseToken] = r
	return nil
}

func (m *MemStore) ActivateFinancialPreviewCommand(_ context.Context, id, token string) error {
	if uuid.Validate(id) != nil || uuid.Validate(token) != nil {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := id + ":" + token
	r, ok := m.financialPreviewCommands[key]
	if !ok || r.Phase != "preparing" {
		return ErrConflict
	}
	r.Phase = "active"
	m.financialPreviewCommands[key] = r
	return nil
}

func (m *MemStore) RetireFinancialPreviewCommand(_ context.Context, id, token string, now time.Time) error {
	if uuid.Validate(id) != nil || uuid.Validate(token) != nil || now.IsZero() {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := id + ":" + token
	r, ok := m.financialPreviewCommands[key]
	if !ok {
		return ErrConflict
	}
	r.Phase = "retired"
	m.financialPreviewCommands[key] = r
	return nil
}

func (m *MemStore) FinancialPreviewCommands(_ context.Context, account, app string) ([]FinancialPreviewCommandRuntime, error) {
	if uuid.Validate(account) != nil || uuid.Validate(app) != nil {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []FinancialPreviewCommandRuntime{}
	known := map[string]bool{}
	for _, r := range m.financialPreviewCommands {
		known[r.TaskID] = true
		if r.AccountID == account && r.AppID == app && r.Phase != "retired" {
			out = append(out, r)
		}
	}
	for _, t := range m.appTasks {
		if t.AccountID == account && t.AppID == app && !known[t.ID] && (t.Status == AppTaskRestoring || t.Status == AppTaskRunning) {
			out = append(out, FinancialPreviewCommandRuntime{AccountID: account, AppID: app, TaskID: t.ID, DeploymentID: t.DeploymentID, Phase: "unknown"})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TaskID < out[j].TaskID })
	return out, nil
}
