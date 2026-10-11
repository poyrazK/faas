package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) workflowArtifactAuthorityLocked(id string, a OperationWorkflowAuthority) (Operation, WorkflowRun, error) {
	op, run, _, err := m.workflowOperationAuthorityLocked(id, a, true)
	return op, run, err
}

func (m *MemStore) workflowOperationAuthorityLocked(id string, a OperationWorkflowAuthority, artifact bool) (Operation, WorkflowRun, workflowOperationWindow, error) {
	op, exists := m.operationMemoryLocked().operations[id]
	if !exists {
		return Operation{}, WorkflowRun{}, workflowOperationWindow{}, ErrNotFound
	}
	run, exists := m.workflowRuns[a.RunID]
	if !exists {
		return Operation{}, WorkflowRun{}, workflowOperationWindow{}, ErrNotFound
	}
	step := m.workflowSteps[a.RunID][a.StepName]
	app := m.apps[op.AppID]
	account := m.accounts[op.AccountID]
	instance := m.instances[a.InstanceID]
	if instance.AppID != op.AppID || instance.DeploymentID != op.DeploymentID || instance.State != string(StateRunning) || !account.Active() || !account.Plan.WorkflowsAllowed() || app.Status == AppDeleted || app.MaintenanceMode || !m.workflowOutboundTenantLinkActiveLocked(op.AccountID, op.PlatformTenantID, op.AppID) {
		return Operation{}, WorkflowRun{}, workflowOperationWindow{}, ErrNotFound
	}
	if step.Status != WorkflowStepStatusRunning || step.Attempt != a.Attempt || !m.workflowRunLeases[a.RunID].After(time.Now()) {
		return Operation{}, WorkflowRun{}, workflowOperationWindow{}, ErrOperationStaleAttempt
	}
	if err := validateOperationWorkflowExecutionAuthority(op, run, a, step.outboundAttemptToken, !artifact); err != nil {
		return Operation{}, WorkflowRun{}, workflowOperationWindow{}, err
	}
	if artifact {
		if err := validateOperationWorkflowAuthority(op, run, a, step.outboundAttemptToken); err != nil {
			return Operation{}, WorkflowRun{}, workflowOperationWindow{}, err
		}
	}
	window, err := m.workflowOperationWindowLocked(run, a.StepName, a.Attempt)
	return cloneOperation(op), run, window, err
}

func (m *MemStore) ReuseWorkflowOperationArtifact(_ context.Context, id string, a OperationWorkflowAuthority, req api.OperationArtifactRequest) (api.OperationWorkflowArtifactResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	op, _, err := m.workflowArtifactAuthorityLocked(id, a)
	if err != nil {
		return api.OperationWorkflowArtifactResponse{}, err
	}
	receipt, exists, err := workflowArtifactReceipt(op, a, req)
	if err != nil {
		return api.OperationWorkflowArtifactResponse{}, err
	}
	if !exists {
		copy := cloneOperation(op)
		_, err := prepareWorkflowArtifact(&copy, a, req, OperationResultBlob{}, time.Now().UTC())
		return api.OperationWorkflowArtifactResponse{Available: false}, err
	}
	response, err := rebindWorkflowArtifact(&op, a, req.ReportID, receipt, m.operationData.blobs[receipt.BlobID])
	if err != nil {
		return api.OperationWorkflowArtifactResponse{}, err
	}
	m.operationData.operations[id] = cloneOperation(op)
	return response, nil
}

func (m *MemStore) ReserveWorkflowOperationArtifact(_ context.Context, id string, a OperationWorkflowAuthority, req api.OperationArtifactRequest) (OperationResultBlob, Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	op, _, err := m.workflowArtifactAuthorityLocked(id, a)
	if err != nil {
		return OperationResultBlob{}, Operation{}, err
	}
	receipt, exists, err := workflowArtifactReceipt(op, a, req)
	if err != nil {
		return OperationResultBlob{}, Operation{}, err
	}
	if exists {
		blob := m.operationData.blobs[receipt.BlobID]
		if _, err := rebindWorkflowArtifact(&op, a, req.ReportID, receipt, blob); err != nil {
			return OperationResultBlob{}, Operation{}, err
		}
		m.operationData.operations[id] = cloneOperation(op)
		return blob, op, nil
	}
	blob := newOperationResultBlob(op, Invocation{Attempts: a.Attempt}, req, time.Now().UTC())
	blob.ExecutionID, blob.WorkflowRunID, blob.WorkflowStep = "", a.RunID, a.StepName
	copy := cloneOperation(op)
	if _, err := prepareWorkflowArtifact(&copy, a, req, blob, time.Now().UTC()); err != nil {
		return OperationResultBlob{}, Operation{}, err
	}
	var count, bytes int64
	for _, b := range m.operationData.blobs {
		if b.AccountID == op.AccountID {
			count++
			bytes += b.SizeBytes
		}
	}
	if err := checkOperationBlobQuota(api.MustLimitsFor(m.accounts[op.AccountID].Plan).Operations, count, bytes, req.SizeBytes); err != nil {
		return OperationResultBlob{}, Operation{}, err
	}
	m.operationData.blobs[blob.ID] = blob
	return blob, op, nil
}

func (m *MemStore) PrepareVerifiedWorkflowOperationArtifact(_ context.Context, id string, a OperationWorkflowAuthority, req api.OperationArtifactRequest, blobID string) (api.OperationWorkflowArtifactResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	op, run, err := m.workflowArtifactAuthorityLocked(id, a)
	if err != nil {
		return api.OperationWorkflowArtifactResponse{}, err
	}
	receipt, exists, err := workflowArtifactReceipt(op, a, req)
	if err != nil {
		return api.OperationWorkflowArtifactResponse{}, err
	}
	if exists {
		response, err := rebindWorkflowArtifact(&op, a, req.ReportID, receipt, m.operationData.blobs[receipt.BlobID])
		if err == nil {
			m.operationData.operations[id] = cloneOperation(op)
		}
		return response, err
	}
	blob, exists := m.operationData.blobs[blobID]
	if !exists {
		return api.OperationWorkflowArtifactResponse{}, ErrNotFound
	}
	now := time.Now().UTC()
	if err := validateWorkflowArtifactBlob(blob, op, a, req, now); err != nil {
		return api.OperationWorkflowArtifactResponse{}, err
	}
	artifact, err := prepareWorkflowArtifact(&op, a, req, blob, now)
	if err != nil {
		return api.OperationWorkflowArtifactResponse{}, err
	}
	blob.State = "retained"
	m.operationData.blobs[blobID] = blob
	event := operationWorkflowEvent(&op, run, "artifact_prepared", map[string]any{"artifact_id": artifact.ID, "workflow_step": a.StepName}, now)
	m.operationSaveLocked(op, event)
	return api.OperationWorkflowArtifactResponse{Available: true, Artifact: &artifact}, nil
}

var _ OperationWorkflowArtifactStore = (*MemStore)(nil)
