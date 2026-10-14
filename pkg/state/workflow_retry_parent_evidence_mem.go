package state

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"time"
)

func (m *MemStore) retryParentEvidenceLocked(r api.WorkflowRecoveryPolicyReview, e api.WorkflowRecoveryAutomaticAction, frame api.WorkflowInitialRecoveryReview, now time.Time) api.WorkflowRetryEvidenceDiagnosis {
	out := retryParentEvidenceBase(r, e, frame, now)
	if out.Reason == "retry_not_pending" {
		return out
	}
	job := m.eventRecoveryJobs[r.JobID]
	if job == nil {
		out.Reason = "parent_job_not_retained"
		return out
	}
	for _, item := range job.Items {
		if item.Position != e.Position {
			continue
		}
		if !retryParentItemMatches(job.Job, item.EventRecoveryItem, r, e, frame) {
			out.Reason = "parent_lineage_changed"
			return out
		}
		if item.State != "queued" {
			out.Reason = "parent_admission_not_verified"
			return out
		}
		execution := m.workflowRecoveryExecutionLocked(job, item, now)
		_, existing := job.WorkflowResults[item.Position]
		return retryParentExecutionDiagnosis(out, execution, existing)
	}
	out.Reason = "parent_item_not_retained"
	return out
}
func (m *MemStore) DiagnoseWorkflowRetryParentEvidence(ctx context.Context, account, app, review, action string) (api.WorkflowRetryEvidenceDiagnosis, error) {
	var out api.WorkflowRetryEvidenceDiagnosis
	if err := validateRetryEvidenceIDs(account, app, review, action); err != nil {
		return out, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return out, err
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	_, r, e, frame, err := m.retryEvidenceScopeLocked(account, app, review, action, now)
	if err != nil {
		return out, err
	}
	return recoveryPolicyClone(m.retryParentEvidenceLocked(r, *e, frame, now)), nil
}
func (m *MemStore) RepairWorkflowRetryParentEvidence(ctx context.Context, account, app, review, action string, req api.WorkflowInitialEvidenceRepairRequest) (api.WorkflowRetryEvidenceDiagnosis, error) {
	var out api.WorkflowRetryEvidenceDiagnosis
	if err := validateRetryEvidenceIDs(account, app, review, action); err != nil {
		return out, err
	}
	if err := req.Validate(); err != nil {
		return out, fmt.Errorf("%w: %w", ErrEventRecoveryQuery, err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return out, err
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	p, r, e, frame, err := m.retryEvidenceScopeLocked(account, app, review, action, now)
	if err != nil {
		return out, err
	}
	out = m.retryParentEvidenceLocked(r, *e, frame, now)
	replay, err := retryParentEvidenceReplay(out, req)
	if err != nil {
		return out, err
	}
	if replay {
		return recoveryPolicyClone(out), nil
	}
	if !out.Repairable {
		return out, ErrWorkflowRecoveryEvidenceBlocked
	}
	job := m.eventRecoveryJobs[r.JobID]
	execution := *out.Execution
	execution.Source = "recovery_result"
	execution.EvidenceSource = "workflow_run"
	execution.RecordedAt = &now
	receipt := initialEvidenceReceipt(ctx, req, execution.State, now)
	if r.ParentEvidenceRepairs == nil {
		r.ParentEvidenceRepairs = map[string]api.WorkflowInitialEvidenceRepairReceipt{}
	}
	r.ParentEvidenceRepairs[e.ActionID] = *receipt
	raw, err := json.Marshal(r)
	if err != nil {
		return out, fmt.Errorf("encode parent evidence receipt: %w", err)
	}
	if job.WorkflowResults == nil {
		job.WorkflowResults = map[int64]memWorkflowRecoveryResult{}
	}
	job.WorkflowResults[e.Position] = memWorkflowRecoveryResult{RunID: frame.Target.RunID, ResumeNumber: frame.Target.ExpectedResumeCount, Execution: execution}
	p.Reviews[r.ID] = raw
	p.appendHistoryEvent(retryRepairHistory(r, *e, "parent", *receipt), now)
	if p.NextExecute.After(now) {
		p.NextExecute = now
	}
	return recoveryPolicyClone(m.retryParentEvidenceLocked(r, *e, frame, now)), nil
}
