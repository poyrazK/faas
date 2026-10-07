// adr: 646
package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) jobOperationAuthorityLocked(id string, a JobOperationAuthority) (Operation, JobTask, error) {
	op, exists := m.operationForJobLocked(a.RunID)
	if !exists || op.ID != id {
		return Operation{}, JobTask{}, ErrNotFound
	}
	task := m.jobTasks[a.RunID][0]
	app, account, tenant, instance := m.apps[op.AppID], m.accounts[op.AccountID], m.platformTenants[op.PlatformTenantID], m.instances[a.InstanceID]
	if !account.Active() || app.AccountID != op.AccountID || app.Status == AppDeleted || app.MaintenanceMode || tenant.AccountID != op.AccountID || tenant.Status != PlatformTenantActive || instance.Kind != "job_task" || instance.JobRunID != a.RunID || instance.JobTaskIndex != 0 || (instance.State != string(StateRunning) && instance.State != string(StateColdBooting)) {
		return Operation{}, JobTask{}, ErrNotFound
	}
	if err := validateOperationJobAuthority(op, task, a, time.Now().UTC()); err != nil {
		return Operation{}, JobTask{}, err
	}
	return op, task, nil
}

func (m *MemStore) ReuseJobOperationArtifact(_ context.Context, id string, a JobOperationAuthority, req api.OperationArtifactRequest) (api.OperationJobArtifactResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	op, _, err := m.jobOperationAuthorityLocked(id, a)
	if err != nil {
		return api.OperationJobArtifactResponse{}, err
	}
	receipt, exists, err := jobArtifactReceipt(op, a, req)
	if err != nil {
		return api.OperationJobArtifactResponse{}, err
	}
	if exists {
		return reuseJobArtifact(op, a, receipt, m.operationData.blobs[receipt.BlobID])
	}
	_, err = prepareJobArtifact(&op, a, req, OperationResultBlob{})
	return api.OperationJobArtifactResponse{Available: false}, err
}

func (m *MemStore) ReserveJobOperationArtifact(_ context.Context, id string, a JobOperationAuthority, req api.OperationArtifactRequest) (OperationResultBlob, Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	op, _, err := m.jobOperationAuthorityLocked(id, a)
	if err != nil {
		return OperationResultBlob{}, Operation{}, err
	}
	receipt, exists, err := jobArtifactReceipt(op, a, req)
	if err != nil {
		return OperationResultBlob{}, Operation{}, err
	}
	if exists {
		blob := m.operationData.blobs[receipt.BlobID]
		_, err := reuseJobArtifact(op, a, receipt, blob)
		return blob, op, err
	}
	blob := newJobArtifactBlob(op, a, req, time.Now().UTC())
	copy := cloneOperation(op)
	if _, err := prepareJobArtifact(&copy, a, req, blob); err != nil {
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

func (m *MemStore) PrepareVerifiedJobOperationArtifact(_ context.Context, id string, a JobOperationAuthority, req api.OperationArtifactRequest, blobID string) (api.OperationJobArtifactResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	op, task, err := m.jobOperationAuthorityLocked(id, a)
	if err != nil {
		return api.OperationJobArtifactResponse{}, err
	}
	receipt, exists, err := jobArtifactReceipt(op, a, req)
	if err != nil {
		return api.OperationJobArtifactResponse{}, err
	}
	if exists {
		return reuseJobArtifact(op, a, receipt, m.operationData.blobs[receipt.BlobID])
	}
	blob, exists := m.operationData.blobs[blobID]
	if !exists {
		return api.OperationJobArtifactResponse{}, ErrNotFound
	}
	now := time.Now().UTC()
	if err := validateJobArtifactBlob(blob, op, a, req, now); err != nil {
		return api.OperationJobArtifactResponse{}, err
	}
	artifact, err := prepareJobArtifact(&op, a, req, blob)
	if err != nil {
		return api.OperationJobArtifactResponse{}, err
	}
	blob.State = "retained"
	m.operationData.blobs[blobID] = blob
	event := operationJobEvent(&op, task, "artifact_prepared", map[string]any{"artifact_id": artifact.ID}, now)
	m.operationSaveLocked(op, event)
	return api.OperationJobArtifactResponse{Available: true, Artifact: &artifact}, nil
}

var _ OperationJobArtifactStore = (*MemStore)(nil)
