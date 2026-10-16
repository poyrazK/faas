package state

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"time"
)

func (m *MemStore) retryEvidenceLocked(r api.WorkflowRecoveryPolicyReview, e api.WorkflowRecoveryAutomaticAction, frame api.WorkflowInitialRecoveryReview, now time.Time) api.WorkflowInitialEvidenceDiagnosis {
	if !recoveryEvidenceSupported(frame) || frame.AutomaticResult != nil {
		return initialEvidenceDiagnosis(frame, nil, "retry_not_applied", now)
	}
	if job := m.eventRecoveryJobs[e.ChildJobID]; job != nil && (len(job.Items) != 1 || !retryEvidenceChildMatches(job.Job, job.Items[0].EventRecoveryItem, r, e)) {
		return initialEvidenceDiagnosis(frame, nil, "child_lineage_changed", now)
	}
	return m.initialEvidenceLocked(frame, now)
}
func (m *MemStore) retryEvidenceScopeLocked(account, app, review, action string, now time.Time) (*memWorkflowRecoveryPolicy, api.WorkflowRecoveryPolicyReview, *api.WorkflowRecoveryAutomaticAction, api.WorkflowInitialRecoveryReview, error) {
	var r api.WorkflowRecoveryPolicyReview
	var frame api.WorkflowInitialRecoveryReview
	if !m.eventRecoveryAppLocked(account, app) {
		return nil, r, nil, frame, ErrNotFound
	}
	p := m.recoveryPolicies[canonicalMemUUID(app)]
	if p == nil {
		return nil, r, nil, frame, ErrNotFound
	}
	e := p.Executions[canonicalMemUUID(action)]
	if e == nil {
		return nil, r, nil, frame, ErrNotFound
	}
	r, ok := recoveryAutomaticMemReview(p, e.ActionID)
	if !ok || !sameMemUUID(r.ID, review) {
		return nil, r, nil, frame, ErrNotFound
	}
	frame, err := retryEvidenceFrame(r, *e, now)
	return p, r, e, frame, err
}
func (m *MemStore) DiagnoseWorkflowRetryEvidence(ctx context.Context, account, app, review, action string) (api.WorkflowRetryEvidenceDiagnosis, error) {
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
	return recoveryPolicyClone(retryEvidenceDiagnosis(m.retryEvidenceLocked(r, *e, frame, now), e.ActionID)), nil
}
func (m *MemStore) RepairWorkflowRetryEvidence(ctx context.Context, account, app, review, action string, req api.WorkflowInitialEvidenceRepairRequest) (api.WorkflowRetryEvidenceDiagnosis, error) {
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
	replay, err := initialEvidenceReplay(frame, req)
	if err != nil {
		return out, err
	}
	diagnosed := m.retryEvidenceLocked(r, *e, frame, now)
	out = retryEvidenceDiagnosis(diagnosed, e.ActionID)
	if replay {
		return recoveryPolicyClone(out), nil
	}
	if !out.Repairable {
		return out, ErrWorkflowRecoveryEvidenceBlocked
	}
	job := m.eventRecoveryJobs[e.ChildJobID]
	execution := *out.Execution
	execution.Source = "recovery_result"
	execution.EvidenceSource = "workflow_run"
	execution.RecordedAt = &now
	receipt := initialEvidenceReceipt(ctx, req, execution.State, now)
	if r.EvidenceRepairs == nil {
		r.EvidenceRepairs = map[string]api.WorkflowInitialEvidenceRepairReceipt{}
	}
	r.EvidenceRepairs[e.ActionID] = *receipt
	raw, err := json.Marshal(r)
	if err != nil {
		return out, fmt.Errorf("encode retry evidence audit: %w", err)
	}
	if job.WorkflowResults == nil {
		job.WorkflowResults = map[int64]memWorkflowRecoveryResult{}
	}
	job.WorkflowResults[1] = memWorkflowRecoveryResult{RunID: frame.Target.RunID, ResumeNumber: frame.Target.ExpectedResumeCount + 1, Execution: execution}
	p.Reviews[r.ID] = raw
	p.appendHistoryEvent(retryRepairHistory(r, *e, "child", *receipt), now)
	if p.NextExecute.After(now) {
		p.NextExecute = now
	}
	frame.EvidenceRepair = receipt
	return recoveryPolicyClone(retryEvidenceDiagnosis(m.retryEvidenceLocked(r, *e, frame, now), e.ActionID)), nil
}
