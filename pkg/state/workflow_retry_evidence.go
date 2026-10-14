package state

import (
	"context"
	"errors"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"time"
)

var ErrWorkflowRecoveryEvidenceBlocked = errors.New("workflow recovery evidence cannot be safely repaired")

type WorkflowRetryEvidenceStore interface {
	DiagnoseWorkflowRetryEvidence(context.Context, string, string, string, string) (api.WorkflowRetryEvidenceDiagnosis, error)
	RepairWorkflowRetryEvidence(context.Context, string, string, string, string, api.WorkflowInitialEvidenceRepairRequest) (api.WorkflowRetryEvidenceDiagnosis, error)
}

// Reuse exact-generation evidence checks with the real frozen retry policy.
// This adapter grants no admission authority and is never returned as a review.
func retryEvidenceFrame(r api.WorkflowRecoveryPolicyReview, e api.WorkflowRecoveryAutomaticAction, now time.Time) (api.WorkflowInitialRecoveryReview, error) {
	a, err := recoveryAutomaticAction(r, e, api.WorkflowRecoveryPolicyResponse{AppID: r.AppID, Version: r.PolicyVersion, Policy: r.Policy})
	if err != nil {
		return api.WorkflowInitialRecoveryReview{}, fmt.Errorf("%w: %w", ErrEventRecoveryChanged, err)
	}
	if !r.ObservedAt.After(now.Add(-api.WorkflowRecoveryHistoryRetention)) && (e.Status != "applied" || e.Result != nil) {
		return api.WorkflowInitialRecoveryReview{}, ErrNotFound
	}
	frame := api.WorkflowInitialRecoveryReview{ID: r.ID, AppID: r.AppID, JobID: e.ChildJobID, Target: a.Retry.WorkflowRuns.Runs[0], Policy: r.Policy, PolicyVersion: r.PolicyVersion, AutomaticResult: e.Result}
	if e.Status == "applied" && e.ChildJobID != "" {
		frame.Status = "submitted"
	}
	if receipt, ok := r.EvidenceRepairs[e.ActionID]; ok {
		frame.EvidenceRepair = &receipt
	}
	return frame, nil
}
func retryEvidenceDiagnosis(d api.WorkflowInitialEvidenceDiagnosis, action string) api.WorkflowRetryEvidenceDiagnosis {
	return api.WorkflowRetryEvidenceDiagnosis{ReviewID: d.ReviewID, ActionID: action, JobID: d.JobID, RunID: d.RunID, ResumeCount: d.ResumeCount, ObservedAt: d.ObservedAt, Reason: d.Reason, Repairable: d.Repairable, Execution: d.Execution, Repair: d.Repair}
}
func retryEvidenceChildMatches(job api.EventRecoveryJob, item api.EventRecoveryItem, r api.WorkflowRecoveryPolicyReview, e api.WorkflowRecoveryAutomaticAction) bool {
	a, err := recoveryAutomaticAction(r, e, api.WorkflowRecoveryPolicyResponse{AppID: r.AppID, Version: r.PolicyVersion, Policy: r.Policy})
	if err != nil || job.Selection.WorkflowRuns == nil || len(job.Selection.WorkflowRuns.Runs) != 1 {
		return false
	}
	actual := job.Selection.WorkflowRuns.Runs[0]
	expected := a.Retry.WorkflowRuns.Runs[0]
	return job.Selection.Mode == "workflow" && job.AppID == r.AppID && job.Selection.ParentJobID == r.JobID && job.Selection.RequestID == e.ActionID && job.Selection.WorkflowRuns.Name == a.Retry.WorkflowRuns.Name && actual.RunID == expected.RunID && actual.ExpectedResumeCount == expected.ExpectedResumeCount && actual.ParentPosition == expected.ParentPosition && item.ParentJobID == r.JobID && item.ParentPosition != nil && *item.ParentPosition == e.Position
}
func validateRetryEvidenceIDs(account, app, review, action string) error {
	if err := validateInitialReviewIDs(account, app, review); err != nil {
		return err
	}
	return eventRecoveryIDs(account, action)
}
