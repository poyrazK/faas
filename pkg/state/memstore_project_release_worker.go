package state

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) releaseWorkerCaptureLocked(accountID, projectID, baselineID string, appIDs ...string) (*api.ProjectReleaseWorker, error) {
	baseline, ok := m.projectReleaseSets[baselineID]
	if !ok || baseline.AccountID != accountID || baseline.ProjectID != projectID || m.activeProjectReleaseSets[releaseKey(projectID, "production")] != baselineID && !m.canaryGraphCapturedLocked(accountID, projectID, baselineID) {
		return nil, ErrConflict
	}
	var capture projectWorkerCapture
	for _, member := range baseline.Members {
		app := m.apps[member.AppID]
		if app.Manifest.ExecutionMode != api.ExecutionModeWorker || len(appIDs) > 0 && app.ID != appIDs[0] {
			continue
		}
		if capture.Worker != nil {
			return nil, ErrConflict
		}
		capture.Worker = &api.ProjectReleaseWorker{AppID: app.ID, Slug: app.Slug, PreviousDeploymentID: member.DeploymentID, DrainSeconds: app.Manifest.StopGracePeriodS}
		bindings := []QueueBinding{}
		for _, b := range m.queueBindings {
			if b.AppID == app.ID {
				bindings = append(bindings, b)
			}
		}
		sort.Slice(bindings, func(i, j int) bool { return bindings[i].ID < bindings[j].ID })
		for _, b := range bindings {
			raw, _ := json.Marshal(map[string]any{"id": b.ID, "name": b.Name, "queue_name": b.QueueName, "mode": b.Mode, "enabled": b.Enabled, "deployment_scope": b.DeploymentScope, "workload_class": b.WorkloadClass, "max_concurrency": b.MaxConcurrency, "retry_policy": b.RetryPolicyJSON, "updated_at": b.UpdatedAt, "retired_at": b.RetiredAt})
			capture.Bindings = append(capture.Bindings, raw)
		}
		for _, trigger := range m.triggers {
			if trigger.AppID.String() != canonicalMemUUID(app.ID) {
				continue
			}
			raw, _ := json.Marshal(map[string]any{"id": trigger.ID.String(), "kind": trigger.Kind, "enabled": trigger.Enabled, "source": trigger.Source.String, "queue_binding_id": trigger.QueueBindingID.String(), "queue_binding_scope": trigger.QueueBindingScope, "config": json.RawMessage(trigger.Config), "updated_at": trigger.UpdatedAt.Time})
			capture.Consumers = append(capture.Consumers, raw)
		}
	}
	return capture.policy()
}
func (m *MemStore) PreviewProjectReleaseWorker(ctx context.Context, accountID, projectID, baselineID string) (*api.ProjectReleaseWorker, error) {
	m.mu.Lock()
	worker, err := m.releaseWorkerCaptureLocked(accountID, projectID, baselineID)
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return validatePreviousProjectWorker(ctx, m, worker)
}
func (m *MemStore) validateReleaseWorkerCandidateLocked(d Deployment) error {
	app := m.apps[d.AppID]
	entry, ok := m.projectSubmissions[app.AccountID+"\x00"+d.releaseWorkerAttempt]
	var receipt api.ProjectSubmissionReceipt
	if !ok || json.Unmarshal(entry.Payload, &receipt) != nil {
		return ErrConflict
	}
	receipt.Status = entry.Status
	if !workerReceiptCandidate(receipt, d) {
		return ErrConflict
	}
	current, err := m.releaseWorkerCaptureLocked(app.AccountID, app.ProjectID, receipt.Apply.SafeRelease.PreviousReleaseID)
	if err != nil || api.ProjectReleaseWorkerPolicyID(current) != api.ProjectReleaseWorkerPolicyID(receipt.Apply.SafeRelease.Worker) {
		return ErrConflict
	}
	return nil
}
func (m *MemStore) HeldProjectReleaseWorkers(_ context.Context, appID string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[appID]
	if !ok {
		return nil, ErrNotFound
	}
	var held []string
	for deploymentID, attempt := range m.releaseWorkerCandidates {
		d, exists := m.deployments[deploymentID]
		if !exists || d.AppID != appID || d.Status == DeployFailed || d.Status == DeployCancelled || d.Status == DeploySuperseded {
			continue
		}
		entry, exists := m.projectSubmissions[app.AccountID+"\x00"+attempt]
		var receipt api.ProjectSubmissionReceipt
		if exists && json.Unmarshal(entry.Payload, &receipt) == nil && receipt.Apply.SafeRelease != nil && receipt.Apply.SafeRelease.Status == "building" && time.Now().Before(receipt.Apply.SafeRelease.Deadline) {
			held = append(held, deploymentID)
		}
	}
	held = append(held, m.promotionWorkerHoldsLocked(appID)...)
	return held, nil
}
func (m *MemStore) releaseWorkerReadyLocked(accountID, appID, deploymentID string) (string, error) {
	if m.deployments[deploymentID].Status != DeployLive {
		return "", nil
	}
	snapshot, err := m.runtimeAppValuesLocked(accountID, appID, deploymentID)
	if err != nil {
		return "", err
	}
	expected, err := NewRuntimeAppConfigFence(snapshot)
	if err != nil {
		return "", err
	}
	for _, instance := range m.instances {
		if m.releaseWorkerDrains[instance.ID] || instance.AppID != appID || instance.DeploymentID != deploymentID || instance.Mode != string(InstanceModeWorker) || instance.State != string(StateRunning) {
			continue
		}
		proof, exists := m.runtimeInstanceConfigProofs[instance.ID]
		inputs, ack := m.instanceRuntimeConfigInputsLocked(instance.ID)
		if exists && ack && inputs.Scope == expected.SecretFence.Scope && proof.WakeID == instance.WakeID && proof.NodeID == instance.NodeID && proof.Fence == expected {
			return instance.ID, nil
		}
	}
	return "", nil
}
func (m *MemStore) guardReleaseWorkerLocked(accountID string, r api.ProjectSubmissionReceipt, afterCutover bool) (bool, error) {
	worker := r.Apply.SafeRelease.Worker
	if worker == nil {
		return true, nil
	}
	baselineID := r.Apply.SafeRelease.PreviousReleaseID
	if afterCutover {
		baselineID = r.Apply.SafeRelease.ReleaseID
	}
	current, err := m.releaseWorkerCaptureLocked(accountID, r.Apply.ProjectID, baselineID)
	if err != nil {
		return false, err
	}
	current.PreviousDeploymentID = worker.PreviousDeploymentID
	if api.ProjectReleaseWorkerPolicyID(current) != api.ProjectReleaseWorkerPolicyID(worker) {
		return false, ErrConflict
	}
	depID := selectedProjectWorkerDeployment(r)
	if depID == "" {
		return false, nil
	}
	if m.releaseWorkerCandidates[depID] != r.AttemptID {
		return false, ErrConflict
	}
	instanceID, err := m.releaseWorkerReadyLocked(accountID, worker.AppID, depID)
	if err != nil {
		return false, err
	}
	worker.DeploymentID, worker.InstanceID = depID, instanceID
	if instanceID == "" {
		worker.Status = "building"
		return false, nil
	}
	worker.Status = "ready"
	if afterCutover {
		worker.Status = "active"
	}
	return true, nil
}
