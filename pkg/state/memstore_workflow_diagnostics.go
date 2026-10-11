package state

import (
	"context"
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) workflowRecoveryTargetLocked(run WorkflowRun) workflowRecoveryTarget {
	app := m.apps[run.AppID]
	account := m.accounts[app.AccountID]
	target := workflowRecoveryTarget{plan: account.Plan, accountActive: account.Active(), appDeleted: app.Status == AppDeleted,
		maintenance: app.MaintenanceMode, tenantRequired: app.PlatformTenantRequired,
		tenantActive:     run.PlatformTenantID == "" || m.workflowOutboundTenantLinkActiveLocked(app.AccountID, run.PlatformTenantID, app.ID),
		pinnedDeployment: run.DeploymentID == "",
	}
	for _, dep := range m.deployments {
		if dep.AppID != app.ID || dep.Status != "live" {
			continue
		}
		target.liveDeployment = target.liveDeployment || dep.Scope == "default"
		target.pinnedDeployment = target.pinnedDeployment || dep.ID == run.DeploymentID && dep.DeletedAt == nil
	}
	for _, other := range m.workflowRuns {
		if other.AppID == run.AppID && workflowGuardRunActive(other.Status) {
			target.activeRuns++
		}
	}
	return target
}

func (m *MemStore) GetWorkflowRunDiagnostics(ctx context.Context, opts WorkflowDiagnosticsOptions) (api.WorkflowRunDiagnosticsResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return api.WorkflowRunDiagnosticsResponse{}, err
	}
	run, ok := m.workflowRuns[opts.RunID]
	app, appOK := m.apps[run.AppID]
	if !ok || !appOK || app.AccountID != opts.AccountID || opts.PlatformTenantID != "" && run.PlatformTenantID != opts.PlatformTenantID {
		return api.WorkflowRunDiagnosticsResponse{}, ErrWorkflowRunNotFound
	}
	now := time.Now().UTC()
	target := m.workflowRecoveryTargetLocked(run)
	running := false
	for key, attempt := range m.workflowStepAttempts {
		if key.runID == run.ID && attempt.Status == WorkflowAttemptStatusRunning {
			running = true
		}
	}
	var spec api.WorkflowSpec
	var outboundErr error
	if json.Unmarshal(run.DefinitionSnapshot, &spec) == nil {
		outboundErr = m.validateWorkflowOutboundLocked(run.AppID, opts.AccountID, spec)
	}
	return buildWorkflowRunDiagnostics(run, m.workflowSteps[run.ID], m.workflowDispatchDeadlineLocked(run), now,
		m.workflowDispatchOccupancyLocked(now), target, running, outboundErr), nil
}
