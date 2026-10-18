package state

import (
	"context"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"time"
)

type WorkflowInitialEvidenceStore interface {
	DiagnoseWorkflowInitialEvidence(context.Context, string, string, string) (api.WorkflowInitialEvidenceDiagnosis, error)
	RepairWorkflowInitialEvidence(context.Context, string, string, string, api.WorkflowInitialEvidenceRepairRequest) (api.WorkflowInitialEvidenceDiagnosis, error)
}

func initialEvidenceDiagnosis(r api.WorkflowInitialRecoveryReview, e *api.EventRecoveryExecution, reason string, now time.Time) api.WorkflowInitialEvidenceDiagnosis {
	out := api.WorkflowInitialEvidenceDiagnosis{ReviewID: r.ID, JobID: r.JobID, RunID: r.Target.RunID, ResumeCount: r.Target.ExpectedResumeCount + 1, ObservedAt: now, Reason: reason, Execution: e, Repair: r.EvidenceRepair}
	if !recoveryEvidenceSupported(r) {
		out.Reason = "not_submitted_automatic_initial"
		if r.Policy.Mode == "preview" {
			out.Reason = "not_submitted_manual_initial"
		}
		if r.Policy.Mode == "automatic_retry" {
			out.Reason = "retry_not_applied"
		}
		return out
	}
	if r.AutomaticResult != nil || r.Result != nil {
		out.Reason = "already_confirmed"
		return out
	}
	return recoveryEvidenceExecutionDiagnosis(out, e)
}
func recoveryEvidenceExecutionDiagnosis(out api.WorkflowInitialEvidenceDiagnosis, e *api.EventRecoveryExecution) api.WorkflowInitialEvidenceDiagnosis {
	if e == nil {
		return out
	}
	switch e.Source {
	case "recovery_result":
		out.Reason = "existing_result_conflict"
		switch e.State {
		case "succeeded", "failed", "cancelled":
			out.Reason = "already_recorded"
		}
	case "workflow_run":
		switch e.State {
		case "succeeded", "failed", "cancelled":
			if e.CompletedAt != nil {
				out.Reason = "terminal_outcome_not_saved"
				out.Repairable = true
			} else {
				out.Reason = "completion_time_unverified"
			}
		case "superseded":
			out.Reason = "generation_superseded"
		default:
			out.Reason = "run_not_terminal"
		}
	}
	return out
}
func initialEvidenceReceipt(ctx context.Context, req api.WorkflowInitialEvidenceRepairRequest, outcome string, now time.Time) *api.WorkflowInitialEvidenceRepairReceipt {
	actor := recoveryAuditActor(ctx, "evidence_repair")
	return &api.WorkflowInitialEvidenceRepairReceipt{RequestID: req.RequestID, Reason: req.Reason, ActorKind: actor.Kind, ActorID: actor.ID, RepairedAt: now, Outcome: outcome}
}
func initialEvidenceReplay(r api.WorkflowInitialRecoveryReview, req api.WorkflowInitialEvidenceRepairRequest) (bool, error) {
	if r.EvidenceRepair == nil {
		return false, nil
	}
	if r.EvidenceRepair.RequestID == req.RequestID && r.EvidenceRepair.Reason == req.Reason {
		return true, nil
	}
	return false, fmt.Errorf("evidence repair already recorded: %w", ErrEventRecoveryChanged)
}
func validateInitialReviewIDs(account, app, id string) error {
	if err := eventRecoveryIDs(account, app); err != nil {
		return err
	}
	return eventRecoveryIDs(account, id)
}

func recoveryEvidenceSupported(r api.WorkflowInitialRecoveryReview) bool {
	return r.Status == "submitted" && ((r.Policy.Mode == "automatic_initial" || r.Policy.Mode == "preview") && r.Target.ExpectedResumeCount == 0 || r.Policy.Mode == "automatic_retry" && r.Target.ExpectedResumeCount > 0 && r.Target.ExpectedResumeCount < api.WorkflowRunMaxResumes)
}
