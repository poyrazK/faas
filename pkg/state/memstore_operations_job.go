// adr: 645
package state

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/onebox-faas/faas/pkg/operations"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) operationForJobLocked(runID string) (Operation, bool) {
	if m.operationData == nil {
		return Operation{}, false
	}
	id, ok := m.operationData.jobOwners[runID]
	if !ok {
		return Operation{}, false
	}
	return cloneOperation(m.operationData.operations[id]), true
}
func (m *MemStore) OperationForJobRun(_ context.Context, runID string) (Operation, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	op, ok := m.operationForJobLocked(runID)
	return op, ok, nil
}
func (m *MemStore) prepareOperationJobLocked(op *Operation, inv Invocation, def OperationDefinition, plan api.Plan) (JobRun, error) {
	if !m.accounts[op.AccountID].Active() || m.apps[op.AppID].MaintenanceMode || m.apps[op.AppID].Status == AppDeleted {
		return JobRun{}, ErrConflict
	}
	for _, job := range m.jobs {
		if job.AccountID == op.AccountID && job.Name == def.Spec.Job && job.Status != "deleted" {
			return prepareOperationJob(op, inv, job, plan)
		}
	}
	return JobRun{}, ErrNotFound
}
func (m *MemStore) insertOperationJobLocked(op Operation, run JobRun) {
	if m.jobRuns == nil {
		m.jobRuns = map[string]JobRun{}
	}
	if m.jobTasks == nil {
		m.jobTasks = map[string]map[int]JobTask{}
	}
	m.jobRuns[run.ID] = run
	m.jobTasks[run.ID] = map[int]JobTask{0: {RunID: run.ID, TaskIndex: 0, Attempt: 1, Status: "queued", CreatedAt: run.CreatedAt}}
	d := m.operationMemoryLocked()
	if d.jobOwners == nil {
		d.jobOwners = map[string]string{}
	}
	if d.jobExecutions == nil {
		d.jobExecutions = map[string]map[int]api.OperationExecution{}
	}
	if d.jobExecutions[op.ID] == nil {
		d.jobExecutions[op.ID] = map[int]api.OperationExecution{}
	}
	d.jobOwners[run.ID] = op.ID
	d.jobExecutions[op.ID][op.Generation] = operationJobExecution(op, m.jobTasks[run.ID][0])
}
func (m *MemStore) syncOperationJobLocked(runID string) error {
	op, ok := m.operationForJobLocked(runID)
	if !ok {
		return nil
	}
	task := m.jobTasks[runID][0]
	if op.JobRunID != runID {
		return nil
	}
	m.jobRuns[runID] = recomputeJobRun(m.jobRuns[runID], m.jobTasks[runID], time.Now().UTC())
	events := operationJobProjection(&op, task, time.Now().UTC())
	m.operationData.jobExecutions[op.ID][op.Generation] = operationJobExecution(op, task)
	if len(events) > 0 && op.State.Terminal() {
		if err := m.operationCompletionLocked(&op, m.operationData.definitions[op.DefinitionID]); err != nil {
			return err
		}
	}
	for _, event := range events {
		m.operationSaveLocked(op, event)
	}
	return nil
}
func (m *MemStore) OperationJobDispatchEnv(_ context.Context, runID, instanceID, lease string) (map[string]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	op, ok := m.operationForJobLocked(runID)
	if !ok {
		return nil, nil
	}
	return operationJobDispatchEnv(op, m.jobTasks[runID][0], instanceID, lease, time.Now().UTC())
}
func (m *MemStore) OperationJobControl(_ context.Context, id string, a JobOperationAuthority) (api.OperationJobControlResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	op, task, err := m.jobOperationAuthorityLocked(id, a)
	if err != nil {
		return api.OperationJobControlResponse{}, err
	}
	now := time.Now().UTC()
	return operationJobControlResponse(op, task, now), nil
}
func operationJobControlResponse(op Operation, task JobTask, now time.Time) api.OperationJobControlResponse {
	return api.OperationJobControlResponse{AccountID: op.AccountID, AppID: op.AppID, PlatformTenantID: op.PlatformTenantID, Scope: op.Scope, OperationID: op.ID, JobRunID: task.RunID, Generation: op.Generation, Attempt: task.Attempt, CancellationRequested: op.CancellationRequested, DeadlineAt: operationJobDeadline(op, task), LeaseExpiresAt: *task.LeaseExpiresAt, ObservedAt: now, PollAfterMS: api.OperationControlPollIntervalMS}
}
func (m *MemStore) ReportOperationJob(_ context.Context, id string, a JobOperationAuthority, kind string, report api.OperationJobReportRequest) (Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	op, task, err := m.jobOperationAuthorityLocked(id, a)
	if err != nil {
		return Operation{}, err
	}
	now := time.Now().UTC()
	event, changed, err := operationJobReport(&op, m.operationData.definitions[op.DefinitionID], task, kind, report, now)
	if err != nil {
		return Operation{}, err
	}
	if changed {
		m.operationSaveLocked(op, event)
	}
	return cloneOperation(op), nil
}
func (m *MemStore) cancelOperationJobLocked(op Operation) (Operation, error) {
	task := m.jobTasks[op.JobRunID][0]
	now := time.Now().UTC()
	op.CancellationRequested = true
	event := operationJobEvent(&op, task, "cancellation_requested", map[string]any{"cancellation_requested": true}, now)
	m.operationSaveLocked(op, event)
	if task.Status == "queued" {
		task.Status, task.FinishedAt = "cancelled", &now
		m.jobTasks[task.RunID][0] = task
		m.recordJobTaskAttemptLocked(task)
	}
	if err := m.syncOperationJobLocked(task.RunID); err != nil {
		return Operation{}, err
	}
	return cloneOperation(m.operationData.operations[op.ID]), nil
}
func (m *MemStore) recoverOperationJobLocked(op Operation, req api.OperationRecoveryRequest, fingerprint string) (Operation, error) {
	now := time.Now().UTC()
	expiry := op.ExpiresAt
	def := m.operationData.definitions[op.DefinitionID]
	task := m.jobTasks[op.JobRunID][0]
	if task.Status == "queued" || task.Status == "claimed" {
		return Operation{}, ErrConflict
	}
	if req.Resolution == "safe_to_retry" {
		s, err := m.operationRecoverySnapshotLocked(op.AccountID, op.ID)
		if err != nil {
			return Operation{}, err
		}
		blockers, _ := operationRecoveryRetryPlan(s, now)
		if len(blockers) > 0 {
			return Operation{}, fmt.Errorf("%w: job recovery blocked", ErrConflict)
		}
	}
	event, err := operationJobRecovery(&op, def, req, now)
	if err != nil {
		return Operation{}, err
	}
	if req.Resolution == "safe_to_retry" {
		run := *op.JobSnapshot
		run.ID, run.CreatedAt = newOperationID(), now
		run.AggregateStatus = "queued"
		op.JobRunID = run.ID
		m.insertOperationJobLocked(op, run)
	} else if err := m.operationCompletionLocked(&op, def); err != nil {
		return Operation{}, err
	}
	m.saveOperationRecoveryDecisionLocked(op, req, fingerprint, now, expiry)
	m.operationSaveLocked(op, event)
	return cloneOperation(op), nil
}
func operationJobRecovery(op *Operation, def OperationDefinition, req api.OperationRecoveryRequest, now time.Time) (api.OperationEvent, error) {
	if op.State != api.OperationRequiresReconciliation || op.Generation != req.ExpectedGeneration {
		return api.OperationEvent{}, ErrConflict
	}
	if op.RecoveryCount >= op.PlanLimits.RecoveriesPerOperation {
		return api.OperationEvent{}, NewOperationLimitError("recoveries_per_operation", int64(op.PlanLimits.RecoveriesPerOperation), int64(op.RecoveryCount+1))
	}
	switch req.Resolution {
	case "safe_to_retry":
		op.Generation++
		op.State, op.Progress, op.Result, op.CancellationRequested = api.OperationAccepted, nil, nil, false
		op.JobResultReceipt = nil
		op.JobReports = map[string]string{}
		op.JobArtifactReceipts = nil
		op.Artifacts = nil
		op.ArtifactStorageKeys = nil
		op.CompletionDelivery = api.OperationDeliveryResponse{State: "not_requested"}
		if def.Spec.CompletionWebhookID != "" {
			op.CompletionDelivery.State = "awaiting_outcome"
		}
	case "succeeded":
		contract, err := operations.Compile(def.Spec, op.PlanLimits)
		if err != nil {
			return api.OperationEvent{}, err
		}
		if err := contract.ValidateOutput(req.Result, op.ValueMaxBytes); err != nil {
			return api.OperationEvent{}, fmt.Errorf("%w: %w", ErrInvalidArgument, err)
		}
		op.State, op.Result = api.OperationSucceeded, append(json.RawMessage(nil), req.Result...)
	case "failed":
		op.State = api.OperationFailed
	case "cancelled":
		op.State = api.OperationCancelled
	default:
		return api.OperationEvent{}, ErrInvalidArgument
	}
	op.RecoveryCount++
	op.FailureCode = ""
	if req.Resolution == "failed" {
		op.FailureCode = "reconciled_failure"
	}
	op.ExpiresAt = now.Add(time.Duration(op.PlanLimits.ResultRetentionSeconds) * time.Second)
	op.EventExpiresAt = now.Add(time.Duration(op.PlanLimits.EventRetentionSeconds) * time.Second)
	if req.Resolution == "succeeded" {
		publishJobArtifacts(op, JobTask{RunID: op.JobRunID, Attempt: 1}, true, now)
	}
	refreshOperationArtifactExpiry(op)
	kind := req.Resolution
	if kind == "safe_to_retry" {
		kind = "recovery_requested"
	}
	digest, _ := operations.InputFingerprint(mustOperationJSON(req.Evidence))
	return operationJobEvent(op, JobTask{RunID: op.JobRunID, Attempt: 1}, kind, map[string]any{"state": op.State, "resolution": req.Resolution, "evidence_digest": digest}, now), nil
}

func (m *MemStore) OperationJobImageRetained(_ context.Context, key string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.operationData != nil {
		for _, op := range m.operationData.operations {
			if op.JobSnapshot != nil && op.JobSnapshot.ImageStorageKeySnapshot == key {
				return true, nil
			}
		}
	}
	return false, nil
}

// Parent purge closes work that never started and waits for leased execution
// to settle. Never turn a queued Operation into an ordinary Job by unlinking it.
func (m *MemStore) prepareOperationJobOwnerPurgeLocked(accountID, appID string) error {
	if m.operationData == nil {
		return nil
	}
	owned := func(op Operation) bool {
		return op.JobRunID != "" && (accountID != "" && op.AccountID == accountID || appID != "" && op.AppID == appID)
	}
	for _, op := range m.operationData.operations {
		if owned(op) && m.jobTasks[op.JobRunID][0].Status == "claimed" {
			return ErrConflict
		}
	}
	for _, op := range m.operationData.operations {
		if !owned(op) {
			continue
		}
		task := m.jobTasks[op.JobRunID][0]
		if task.Status == "queued" {
			now := time.Now().UTC()
			task.Status, task.FinishedAt = "cancelled", &now
			m.jobTasks[op.JobRunID][0] = task
			m.recordJobTaskAttemptLocked(task)
			m.jobRuns[op.JobRunID] = recomputeJobRun(m.jobRuns[op.JobRunID], m.jobTasks[op.JobRunID], now)
		}
	}
	return nil
}
