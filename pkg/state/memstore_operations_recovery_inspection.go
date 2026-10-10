// adr: 660
package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) operationRecoverySnapshotLocked(accountID, id string) (operationRecoverySnapshot, error) {
	if m.operationData == nil {
		return operationRecoverySnapshot{}, ErrNotFound
	}
	op, exists := m.operationData.operations[id]
	if !exists || op.AccountID != accountID {
		return operationRecoverySnapshot{}, ErrNotFound
	}
	if !operationRetained(op, time.Now()) {
		return operationRecoverySnapshot{}, ErrOperationExpired
	}
	account, app := m.accounts[accountID], m.apps[op.AppID]
	s := operationRecoverySnapshot{op: op, def: m.operationData.definitions[op.DefinitionID], plan: account.Plan,
		codeAvailable: m.operationCodeAvailableLocked(op), tenantActive: m.platformTenants[op.PlatformTenantID].Status == PlatformTenantActive,
		blobs: map[string]OperationResultBlob{}}
	bindings := make(map[string]bool, len(op.ArtifactStorageKeys))
	for _, key := range op.ArtifactStorageKeys {
		bindings[key] = true
	}
	for _, blob := range m.operationData.blobs {
		if bindings[blob.StorageKey] && blob.OperationID == op.ID && blob.AccountID == accountID {
			s.blobs[blob.StorageKey] = blob
		}
	}
	if op.JobRunID != "" {
		task, ok := m.jobTasks[op.JobRunID][0]
		if !ok {
			return s, ErrNotFound
		}
		s.jobTask = &task
		job := m.jobs[op.JobSnapshot.JobID]
		s.targetAvailable = account.Active() && app.Status != AppDeleted && !app.MaintenanceMode && job.Status == "active"
		return s, nil
	}
	if op.WorkflowRunID == "" {
		var ok bool
		s.inv, ok = m.invocations[op.CurrentInvocationID]
		if !ok {
			return operationRecoverySnapshot{}, ErrNotFound
		}
		return s, nil
	}
	run, ok := m.workflowRuns[op.WorkflowRunID]
	if !ok || run.AppID != op.AppID || run.PlatformTenantID != op.PlatformTenantID {
		return operationRecoverySnapshot{}, ErrNotFound
	}
	s.run, s.steps = &run, m.workflowSteps[run.ID]
	s.targetAvailable = account.Active() && account.Plan.WorkflowsAllowed() && app.Status != AppDeleted && !app.MaintenanceMode && (!app.PlatformTenantRequired || run.PlatformTenantID != "")
	for key, attempt := range m.workflowStepAttempts {
		if key.runID == run.ID && attempt.Status == WorkflowAttemptStatusRunning {
			s.runningAttempt = true
		}
	}
	for _, other := range m.workflowRuns {
		if other.AppID == op.AppID && workflowGuardRunActive(other.Status) {
			s.activeRuns++
		}
	}
	return s, nil
}

func (m *MemStore) InspectOperationRecovery(_ context.Context, accountID, id string) (api.OperationRecoveryInspection, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, err := m.operationRecoverySnapshotLocked(accountID, id)
	if err != nil {
		return api.OperationRecoveryInspection{}, err
	}
	return operationRecoveryInspection(s, time.Now().UTC()), nil
}

func (m *MemStore) PreviewOperationRecovery(_ context.Context, accountID, id string, req api.OperationRecoveryPreviewRequest) (api.OperationRecoveryPreview, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, err := m.operationRecoverySnapshotLocked(accountID, id)
	if err != nil {
		return api.OperationRecoveryPreview{}, err
	}
	return operationRecoveryPreview(s, req, time.Now().UTC())
}

func (m *MemStore) checkOperationInspectionRevisionLocked(op Operation, revision string) error {
	if revision == "" {
		return nil
	}
	s, err := m.operationRecoverySnapshotLocked(op.AccountID, op.ID)
	if err != nil {
		return err
	}
	if operationRecoveryInspection(s, time.Now().UTC()).InspectionRevision != revision {
		return ErrConflict
	}
	return nil
}
