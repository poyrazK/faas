package state

import (
	"context"
	"github.com/onebox-faas/faas/pkg/api"
	"time"
)

type WorkflowRetryParentEvidenceStore interface {
	DiagnoseWorkflowRetryParentEvidence(context.Context, string, string, string, string) (api.WorkflowRetryEvidenceDiagnosis, error)
	RepairWorkflowRetryParentEvidence(context.Context, string, string, string, string, api.WorkflowInitialEvidenceRepairRequest) (api.WorkflowRetryEvidenceDiagnosis, error)
}

func retryParentEvidenceBase(r api.WorkflowRecoveryPolicyReview, e api.WorkflowRecoveryAutomaticAction, frame api.WorkflowInitialRecoveryReview, now time.Time) api.WorkflowRetryEvidenceDiagnosis {
	out := api.WorkflowRetryEvidenceDiagnosis{ReviewID: r.ID, ActionID: e.ActionID, JobID: r.JobID, RunID: frame.Target.RunID, ResumeCount: frame.Target.ExpectedResumeCount, ObservedAt: now, Reason: "exact_generation_unverified", EvidenceKind: "parent", ItemPosition: e.Position}
	if receipt, ok := r.ParentEvidenceRepairs[e.ActionID]; ok {
		out.Repair = &receipt
	}
	if e.Status != "pending" || e.ChildJobID != "" || frame.Target.ExpectedResumeCount < 1 {
		out.Reason = "retry_not_pending"
	}
	return out
}
func retryParentItemMatches(job api.EventRecoveryJob, item api.EventRecoveryItem, r api.WorkflowRecoveryPolicyReview, e api.WorkflowRecoveryAutomaticAction, frame api.WorkflowInitialRecoveryReview) bool {
	a, err := recoveryAutomaticAction(r, e, api.WorkflowRecoveryPolicyResponse{AppID: r.AppID, Version: r.PolicyVersion, Policy: r.Policy})
	if err != nil || job.Selection.WorkflowRuns == nil {
		return false
	}
	if job.Selection.Mode != "workflow" || job.AppID != r.AppID || job.Selection.WorkflowRuns.Name != a.Retry.WorkflowRuns.Name || item.Position != e.Position || item.WorkflowRunID != frame.Target.RunID || item.ExpectedResumeCount == nil || *item.ExpectedResumeCount != frame.Target.ExpectedResumeCount-1 {
		return false
	}
	for _, target := range job.Selection.WorkflowRuns.Runs {
		if target.RunID == frame.Target.RunID && target.ExpectedResumeCount == frame.Target.ExpectedResumeCount-1 {
			return true
		}
	}
	return false
}
func retryParentExecutionDiagnosis(out api.WorkflowRetryEvidenceDiagnosis, execution api.EventRecoveryExecution, existing bool) api.WorkflowRetryEvidenceDiagnosis {
	observed := recoveryEvidenceExecutionDiagnosis(api.WorkflowInitialEvidenceDiagnosis{Reason: out.Reason, Execution: &execution}, &execution)
	out.Reason, out.Execution, out.Repairable = observed.Reason, observed.Execution, observed.Repairable
	if existing && out.Repairable {
		out.Reason = "existing_result_conflict"
		out.Repairable = false
	}
	return out
}
func retryParentEvidenceReplay(out api.WorkflowRetryEvidenceDiagnosis, req api.WorkflowInitialEvidenceRepairRequest) (bool, error) {
	return initialEvidenceReplay(api.WorkflowInitialRecoveryReview{EvidenceRepair: out.Repair}, req)
}
