package state

import (
	"bytes"
	"context"
	"encoding/json"
	"sort"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/automationlive"
)

func (m *MemStore) liveViewLocked(live automationLiveRecord) (api.AutomationLiveVerification, error) {
	run, exists := m.workflowRuns[live.View.RunID]
	if !exists {
		return api.AutomationLiveVerification{}, ErrNotFound
	}
	current := m.automations[live.AppID+"/"+live.Name]
	return liveView(live, run.Status, run.UpdatedAt, run.ResumeCount, current, m.automationDeploymentLocked(live.AppID).ID), nil
}

func (m *MemStore) StartAutomationLiveVerification(ctx context.Context, appID, name, key string, req api.StartAutomationLiveVerificationRequest, runtimeEnabled bool) (api.AutomationLiveVerification, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.startAutomationLiveVerificationLocked(ctx, appID, name, key, req, runtimeEnabled)
}

func (m *MemStore) startAutomationLiveVerificationLocked(ctx context.Context, appID, name, key string, req api.StartAutomationLiveVerificationRequest, runtimeEnabled bool) (api.AutomationLiveVerification, bool, error) {
	fingerprint, err := validateLiveStart(&req, key)
	if err != nil {
		return api.AutomationLiveVerification{}, false, err
	}
	if err := ctx.Err(); err != nil {
		return api.AutomationLiveVerification{}, false, err
	}
	for _, live := range m.automationLiveVerifications {
		if live.AppID == appID && live.Name == name && live.Key == key {
			if !bytes.Equal(live.Fingerprint, fingerprint) {
				return api.AutomationLiveVerification{}, false, ErrWorkflowRunIdempotencyConflict
			}
			view, err := m.liveViewLocked(live)
			return view, true, err
		}
	}
	app, exists := m.apps[appID]
	if !exists || app.Status == AppDeleted {
		return api.AutomationLiveVerification{}, false, ErrNotFound
	}
	account := m.accounts[app.AccountID]
	if err := automationAccountGate(account.Plan, account.Active(), "tests"); err != nil {
		return api.AutomationLiveVerification{}, false, err
	}
	if app.PlatformTenantRequired {
		return api.AutomationLiveVerification{}, false, api.ErrWorkflowTenantIdentityUnavailable()
	}
	if !runtimeEnabled || app.MaintenanceMode {
		return api.AutomationLiveVerification{}, false, ErrWorkflowEventTargetUnavailable
	}
	record, exists := m.automations[appID+"/"+name]
	if !exists {
		return api.AutomationLiveVerification{}, false, ErrNotFound
	}
	deployment := m.automationDeploymentLocked(appID)
	if deployment.ID == "" {
		return api.AutomationLiveVerification{}, false, ErrWorkflowEventTargetUnavailable
	}
	run, live, err := prepareLiveVerification(appID, name, key, deployment.ID, record, req, fingerprint)
	if err != nil {
		return api.AutomationLiveVerification{}, false, err
	}
	var def api.WorkflowSpec
	if err := json.Unmarshal(run.DefinitionSnapshot, &def); err != nil {
		return api.AutomationLiveVerification{}, false, err
	}
	if err := m.validateWorkflowOutboundLocked(appID, app.AccountID, def); err != nil {
		return api.AutomationLiveVerification{}, false, err
	}
	active := 0
	for _, r := range m.workflowRuns {
		if r.AppID == appID && !liveRunTerminal(r.Status) {
			active++
		}
	}
	if active >= account.Plan.WorkflowMaxConcurrentRuns() {
		return api.AutomationLiveVerification{}, false, api.ErrPlanWorkflowsQuota(account.Plan, account.Plan.WorkflowMaxConcurrentRuns(), active)
	}
	if err := ctx.Err(); err != nil {
		return api.AutomationLiveVerification{}, false, err
	}
	if err := m.insertWorkflowRunLocked(run); err != nil {
		return api.AutomationLiveVerification{}, false, err
	}
	if m.automationLiveVerifications == nil {
		m.automationLiveVerifications = map[string]automationLiveRecord{}
	}
	live.View.CreatedAt = run.CreatedAt
	m.automationLiveVerifications[run.ID] = live
	view, err := m.liveViewLocked(live)
	return view, false, err
}

func (m *MemStore) ListAutomationLiveVerifications(ctx context.Context, appID, name string) (api.ListAutomationLiveVerificationsResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return api.ListAutomationLiveVerificationsResponse{}, err
	}
	record, exists := m.automations[appID+"/"+name]
	if !exists {
		return api.ListAutomationLiveVerificationsResponse{}, ErrNotFound
	}
	result := api.ListAutomationLiveVerificationsResponse{PublishedVersion: record.PublishedVersion, DeploymentID: m.automationDeploymentLocked(appID).ID, Verifications: []api.AutomationLiveVerification{}}
	for _, live := range m.automationLiveVerifications {
		if live.AppID != appID || live.Name != name {
			continue
		}
		view, err := m.liveViewLocked(live)
		if err == nil {
			result.Verifications = append(result.Verifications, view)
		}
	}
	sort.Slice(result.Verifications, func(i, j int) bool {
		a, b := result.Verifications[i], result.Verifications[j]
		if a.CreatedAt.Equal(b.CreatedAt) {
			return a.RunID > b.RunID
		}
		return a.CreatedAt.After(b.CreatedAt)
	})
	if len(result.Verifications) > api.AutomationLiveHistoryLimit {
		result.Verifications = result.Verifications[:api.AutomationLiveHistoryLimit]
	}
	return result, nil
}

func (m *MemStore) GetAutomationLiveVerification(ctx context.Context, appID, name, runID string) (api.AutomationLiveVerification, error) {
	id, err := uuid.Parse(runID)
	if err != nil {
		return api.AutomationLiveVerification{}, ErrNotFound
	}
	runID = id.String()
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return api.AutomationLiveVerification{}, err
	}
	live, exists := m.automationLiveVerifications[runID]
	if !exists || live.AppID != appID || live.Name != name {
		return api.AutomationLiveVerification{}, ErrNotFound
	}
	return m.liveViewLocked(live)
}

func (m *MemStore) CheckAutomationLiveVerification(ctx context.Context, appID, name, runID string) (api.AutomationLiveVerification, error) {
	id, err := uuid.Parse(runID)
	if err != nil {
		return api.AutomationLiveVerification{}, ErrNotFound
	}
	runID = id.String()
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.checkAutomationLiveVerificationLocked(ctx, appID, name, runID)
}

func (m *MemStore) checkAutomationLiveVerificationLocked(ctx context.Context, appID, name, runID string) (api.AutomationLiveVerification, error) {
	live, exists := m.automationLiveVerifications[runID]
	run, hasRun := m.workflowRuns[runID]
	if !exists || !hasRun || live.AppID != appID || live.Name != name {
		return api.AutomationLiveVerification{}, ErrNotFound
	}
	report, err := evaluateLiveReport(ctx, live.Test, run.Status, run.CancelledAt != nil, run.ResumeCount, run.UpdatedAt, func(e api.AutomationLiveExpectation) ([]automationlive.ObservedStep, error) {
		rows := []automationlive.ObservedStep{}
		for _, step := range m.workflowSteps[runID] {
			root := e.Loop == "" && step.StepName == e.Step && step.ForEachParent == nil && step.ForEachIndex == nil
			item := e.Loop != "" && step.ForEachParent != nil && *step.ForEachParent == e.Loop && step.ForEachIndex != nil && e.ItemIndex != nil && *step.ForEachIndex == *e.ItemIndex
			if root || item {
				rows = append(rows, automationlive.ObservedStep{State: step.Status, Output: step.Output, AttemptCount: step.Attempt})
				if len(rows) > 1 {
					break
				}
			}
		}
		return rows, nil
	})
	if err != nil {
		return api.AutomationLiveVerification{}, err
	}
	if err := ctx.Err(); err != nil {
		return api.AutomationLiveVerification{}, err
	}
	if report != nil {
		live.View.Report = report
		m.automationLiveVerifications[runID] = live
	}
	return m.liveViewLocked(live)
}

func (m *MemStore) clearAutomationLiveLocked(appID, name string) {
	for id, suite := range m.automationLiveSuites {
		if suite.AppID == appID && suite.Name == name {
			delete(m.automationLiveSuites, id)
		}
	}
	for id, live := range m.automationLiveVerifications {
		if live.AppID == appID && live.Name == name {
			delete(m.automationLiveVerifications, id)
		}
	}
}
