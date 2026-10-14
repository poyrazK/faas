package state

import (
	"context"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"time"
)

func (m *MemStore) initialEvidenceLocked(r api.WorkflowInitialRecoveryReview, now time.Time) api.WorkflowInitialEvidenceDiagnosis {
	if !recoveryEvidenceSupported(r) || r.AutomaticResult != nil || r.Result != nil {
		return initialEvidenceDiagnosis(r, nil, "not_submitted_automatic_initial", now)
	}
	job := m.eventRecoveryJobs[r.JobID]
	if job == nil {
		return initialEvidenceDiagnosis(r, nil, "job_not_retained", now)
	}
	if !sameMemUUID(job.Job.AppID, r.AppID) || len(job.Items) != 1 || job.Items[0].WorkflowRunID != r.Target.RunID || job.Items[0].ExpectedResumeCount == nil || *job.Items[0].ExpectedResumeCount != r.Target.ExpectedResumeCount {
		return initialEvidenceDiagnosis(r, nil, "job_target_changed", now)
	}
	if job.Items[0].State != "queued" {
		return initialEvidenceDiagnosis(r, nil, "admission_not_verified", now)
	}
	e := m.workflowRecoveryExecutionLocked(job, job.Items[0], now)
	out := initialEvidenceDiagnosis(r, &e, "exact_generation_unverified", now)
	if _, exists := job.WorkflowResults[1]; exists && out.Repairable {
		out.Repairable = false
		out.Reason = "existing_result_conflict"
	}
	return out
}
func (m *MemStore) DiagnoseWorkflowInitialEvidence(ctx context.Context, account, app, id string) (api.WorkflowInitialEvidenceDiagnosis, error) {
	var out api.WorkflowInitialEvidenceDiagnosis
	if err := validateInitialReviewIDs(account, app, id); err != nil {
		return out, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if !m.eventRecoveryAppLocked(account, app) {
		return out, ErrNotFound
	}
	r, ok := m.initialRecoveryReviews[canonicalMemUUID(id)]
	now := time.Now().UTC().Truncate(time.Microsecond)
	if !ok || !sameMemUUID(r.AppID, app) || !r.PreparedAt.After(now.Add(-api.WorkflowRecoveryHistoryRetention)) && !initialReviewUnresolved(r) {
		return out, ErrNotFound
	}
	return recoveryPolicyClone(m.initialEvidenceLocked(r, now)), nil
}
func (m *MemStore) RepairWorkflowInitialEvidence(ctx context.Context, account, app, id string, req api.WorkflowInitialEvidenceRepairRequest) (api.WorkflowInitialEvidenceDiagnosis, error) {
	var out api.WorkflowInitialEvidenceDiagnosis
	if err := validateInitialReviewIDs(account, app, id); err != nil {
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
	if !m.eventRecoveryAppLocked(account, app) {
		return out, ErrNotFound
	}
	r, ok := m.initialRecoveryReviews[canonicalMemUUID(id)]
	now := time.Now().UTC().Truncate(time.Microsecond)
	if !ok || !sameMemUUID(r.AppID, app) || !r.PreparedAt.After(now.Add(-api.WorkflowRecoveryHistoryRetention)) && !initialReviewUnresolved(r) {
		return out, ErrNotFound
	}
	replay, err := initialEvidenceReplay(r, req)
	if err != nil {
		return out, err
	}
	if replay {
		return recoveryPolicyClone(m.initialEvidenceLocked(r, now)), nil
	}
	out = m.initialEvidenceLocked(r, now)
	job := m.eventRecoveryJobs[r.JobID]
	if !out.Repairable || job == nil {
		return out, ErrWorkflowInitialReviewBlocked
	}
	// An incompatible existing result is never overwritten, even if it is not usable.
	if _, exists := job.WorkflowResults[1]; exists {
		return out, ErrWorkflowInitialReviewBlocked
	}
	e := *out.Execution
	e.Source = "recovery_result"
	e.EvidenceSource = "workflow_run"
	e.RecordedAt = &now
	if job.WorkflowResults == nil {
		job.WorkflowResults = map[int64]memWorkflowRecoveryResult{}
	}
	job.WorkflowResults[1] = memWorkflowRecoveryResult{RunID: r.Target.RunID, ResumeNumber: 1, Execution: e}
	r.EvidenceRepair = initialEvidenceReceipt(ctx, req, e.State, now)
	initialReviewTransition(ctx, &r, r.Status, "evidence_repaired", now)
	m.initialRecoveryReviews[r.ID] = recoveryPolicyClone(r)
	if p := m.recoveryPolicies[canonicalMemUUID(app)]; p != nil {
		p.appendHistoryEvent(initialRepairHistory(r), now)
		if r.Policy.Mode != "preview" && p.NextExecute.After(now) {
			p.NextExecute = now
		}
	}
	if r.Policy.Mode == "preview" {
		delete(m.initialCompletionNext, r.ID)
	}
	return recoveryPolicyClone(m.initialEvidenceLocked(r, now)), nil
}
