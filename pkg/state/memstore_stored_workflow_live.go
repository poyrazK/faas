package state

import (
	"bytes"
	"context"
	"encoding/json"
	"sort"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/storedworkflowlive"
)

func (m *MemStore) liveViewLocked(live storedWorkflowLiveRecord) (api.StoredWorkflowLiveVerification, error) {
	run, exists := m.workflowRuns[live.View.RunID]
	if !exists {
		return api.StoredWorkflowLiveVerification{}, ErrNotFound
	}
	current := m.storedWorkflows[live.AppID+"/"+live.Name]
	return liveView(live, run.Status, run.UpdatedAt, run.ResumeCount, current, m.storedWorkflowDeploymentLocked(live.AppID).ID), nil
}

func (m *MemStore) StartStoredWorkflowLiveVerification(ctx context.Context, appID, name, key string, req api.StartStoredWorkflowLiveVerificationRequest, runtimeEnabled bool) (api.StoredWorkflowLiveVerification, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.startStoredWorkflowLiveVerificationLocked(ctx, appID, name, key, req, runtimeEnabled)
}

func (m *MemStore) startStoredWorkflowLiveVerificationLocked(ctx context.Context, appID, name, key string, req api.StartStoredWorkflowLiveVerificationRequest, runtimeEnabled bool) (api.StoredWorkflowLiveVerification, bool, error) {
	fingerprint, err := validateLiveStart(&req, key)
	if err != nil {
		return api.StoredWorkflowLiveVerification{}, false, err
	}
	if err := ctx.Err(); err != nil {
		return api.StoredWorkflowLiveVerification{}, false, err
	}
	for _, live := range m.storedWorkflowLiveVerifications {
		if live.AppID == appID && live.Name == name && live.Key == key {
			if !bytes.Equal(live.Fingerprint, fingerprint) {
				return api.StoredWorkflowLiveVerification{}, false, ErrWorkflowRunIdempotencyConflict
			}
			view, err := m.liveViewLocked(live)
			return view, true, err
		}
	}
	app, exists := m.apps[appID]
	if !exists || app.Status == AppDeleted {
		return api.StoredWorkflowLiveVerification{}, false, ErrNotFound
	}
	account := m.accounts[app.AccountID]
	if err := storedWorkflowAccountGate(account.Plan, account.Active(), "tests"); err != nil {
		return api.StoredWorkflowLiveVerification{}, false, err
	}
	if app.PlatformTenantRequired {
		return api.StoredWorkflowLiveVerification{}, false, api.ErrWorkflowTenantIdentityUnavailable()
	}
	if !runtimeEnabled || app.MaintenanceMode {
		return api.StoredWorkflowLiveVerification{}, false, ErrWorkflowEventTargetUnavailable
	}
	record, exists := m.storedWorkflows[appID+"/"+name]
	if !exists {
		return api.StoredWorkflowLiveVerification{}, false, ErrNotFound
	}
	deployment := m.storedWorkflowDeploymentLocked(appID)
	if deployment.ID == "" {
		return api.StoredWorkflowLiveVerification{}, false, ErrWorkflowEventTargetUnavailable
	}
	run, live, err := prepareLiveVerification(appID, name, key, deployment.ID, record, req, fingerprint)
	if err != nil {
		return api.StoredWorkflowLiveVerification{}, false, err
	}
	var def api.WorkflowSpec
	if err := json.Unmarshal(run.DefinitionSnapshot, &def); err != nil {
		return api.StoredWorkflowLiveVerification{}, false, err
	}
	if err := m.validateWorkflowOutboundLocked(appID, app.AccountID, def); err != nil {
		return api.StoredWorkflowLiveVerification{}, false, err
	}
	active := m.workflowAdmissionLocked(appID, account.Plan.WorkflowMaxParkedRuns())
	if active >= account.Plan.WorkflowMaxConcurrentRuns() {
		return api.StoredWorkflowLiveVerification{}, false, api.ErrPlanWorkflowsQuota(account.Plan, account.Plan.WorkflowMaxConcurrentRuns(), active)
	}
	if err := ctx.Err(); err != nil {
		return api.StoredWorkflowLiveVerification{}, false, err
	}
	if err := m.insertWorkflowRunLocked(run); err != nil {
		return api.StoredWorkflowLiveVerification{}, false, err
	}
	if m.storedWorkflowLiveVerifications == nil {
		m.storedWorkflowLiveVerifications = map[string]storedWorkflowLiveRecord{}
	}
	live.View.CreatedAt = run.CreatedAt
	m.storedWorkflowLiveVerifications[run.ID] = live
	view, err := m.liveViewLocked(live)
	return view, false, err
}

func (m *MemStore) ListStoredWorkflowLiveVerifications(ctx context.Context, appID, name string) (api.ListStoredWorkflowLiveVerificationsResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return api.ListStoredWorkflowLiveVerificationsResponse{}, err
	}
	record, exists := m.storedWorkflows[appID+"/"+name]
	if !exists {
		return api.ListStoredWorkflowLiveVerificationsResponse{}, ErrNotFound
	}
	result := api.ListStoredWorkflowLiveVerificationsResponse{PublishedVersion: record.PublishedVersion, DeploymentID: m.storedWorkflowDeploymentLocked(appID).ID, Verifications: []api.StoredWorkflowLiveVerification{}}
	for _, live := range m.storedWorkflowLiveVerifications {
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
	if len(result.Verifications) > api.StoredWorkflowLiveHistoryLimit {
		result.Verifications = result.Verifications[:api.StoredWorkflowLiveHistoryLimit]
	}
	return result, nil
}

func (m *MemStore) GetStoredWorkflowLiveVerification(ctx context.Context, appID, name, runID string) (api.StoredWorkflowLiveVerification, error) {
	id, err := uuid.Parse(runID)
	if err != nil {
		return api.StoredWorkflowLiveVerification{}, ErrNotFound
	}
	runID = id.String()
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return api.StoredWorkflowLiveVerification{}, err
	}
	live, exists := m.storedWorkflowLiveVerifications[runID]
	if !exists || live.AppID != appID || live.Name != name {
		return api.StoredWorkflowLiveVerification{}, ErrNotFound
	}
	return m.liveViewLocked(live)
}

func (m *MemStore) CheckStoredWorkflowLiveVerification(ctx context.Context, appID, name, runID string) (api.StoredWorkflowLiveVerification, error) {
	id, err := uuid.Parse(runID)
	if err != nil {
		return api.StoredWorkflowLiveVerification{}, ErrNotFound
	}
	runID = id.String()
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.checkStoredWorkflowLiveVerificationLocked(ctx, appID, name, runID)
}

func (m *MemStore) checkStoredWorkflowLiveVerificationLocked(ctx context.Context, appID, name, runID string) (api.StoredWorkflowLiveVerification, error) {
	live, exists := m.storedWorkflowLiveVerifications[runID]
	run, hasRun := m.workflowRuns[runID]
	if !exists || !hasRun || live.AppID != appID || live.Name != name {
		return api.StoredWorkflowLiveVerification{}, ErrNotFound
	}
	report, err := evaluateLiveReport(ctx, live.Test, run.Status, run.CancelledAt != nil, run.ResumeCount, run.UpdatedAt, func(e api.StoredWorkflowLiveExpectation) ([]storedworkflowlive.ObservedStep, error) {
		rows := []storedworkflowlive.ObservedStep{}
		for _, step := range m.workflowSteps[runID] {
			root := e.Loop == "" && step.StepName == e.Step && step.ForEachParent == nil && step.ForEachIndex == nil
			item := e.Loop != "" && step.ForEachParent != nil && *step.ForEachParent == e.Loop && step.ForEachIndex != nil && e.ItemIndex != nil && *step.ForEachIndex == *e.ItemIndex
			if root || item {
				rows = append(rows, storedworkflowlive.ObservedStep{State: step.Status, Output: step.Output, AttemptCount: step.Attempt})
				if len(rows) > 1 {
					break
				}
			}
		}
		return rows, nil
	})
	if err != nil {
		return api.StoredWorkflowLiveVerification{}, err
	}
	if err := ctx.Err(); err != nil {
		return api.StoredWorkflowLiveVerification{}, err
	}
	if report != nil {
		live.View.Report = report
		m.storedWorkflowLiveVerifications[runID] = live
	}
	return m.liveViewLocked(live)
}

func (m *MemStore) clearStoredWorkflowLiveLocked(appID, name string) {
	for id, suite := range m.storedWorkflowLiveSuites {
		if suite.AppID == appID && suite.Name == name {
			delete(m.storedWorkflowLiveSuites, id)
		}
	}
	for id, live := range m.storedWorkflowLiveVerifications {
		if live.AppID == appID && live.Name == name {
			delete(m.storedWorkflowLiveVerifications, id)
		}
	}
}
