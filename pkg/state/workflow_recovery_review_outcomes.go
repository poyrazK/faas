package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

func finishRecoveryReviewOutcome(out *api.WorkflowRecoveryReviewStatus) {
	out.Complete = len(out.Actions) > 0
	unknown, running, failed, cancelled := false, false, false, 0
	for _, a := range out.Actions {
		out.Complete = out.Complete && a.Complete
		unknown = unknown || a.Outcome == "unknown"
		running = running || a.Outcome == "running"
		failed = failed || a.Outcome == "failed"
		if a.Outcome == "cancelled" {
			cancelled++
		}
	}
	switch {
	case !out.Complete && unknown:
		out.Outcome = "unknown"
	case !out.Complete && running:
		out.Outcome = "running"
	case !out.Complete:
		out.Outcome = "pending"
	case failed:
		out.Outcome = "failed"
	case cancelled == len(out.Actions):
		out.Outcome = "cancelled"
	case cancelled > 0:
		out.Outcome = "completed"
	default:
		out.Outcome = "succeeded"
	}
}
func inspectRecoveryReviewRetryOutcome(ctx context.Context, s WorkflowRecoveryReviewInspector, account string, p api.WorkflowRecoveryResponsePlan, a api.WorkflowRecoveryResponseAction, child api.EventRecoveryJob, out *api.WorkflowRecoveryReviewActionStatus) error {
	out.ChildJobID = child.ID
	out.ItemsURL = "/v1/event-recoveries/" + child.ID + "/items"
	if a.Retry.WorkflowRuns == nil || len(a.Retry.WorkflowRuns.Runs) != 1 {
		return fmt.Errorf("invalid retry review target")
	}
	target := a.Retry.WorkflowRuns.Runs[0]
	out.ResumeCount = target.ExpectedResumeCount + 1
	rows, err := s.ListEventRecoveryItems(ctx, account, child.ID, 0, 1)
	if errors.Is(err, ErrNotFound) {
		out.OutcomeReason = "child_evidence_not_retained"
		return nil
	}
	if err != nil {
		return fmt.Errorf("read recovery review child execution: %w", err)
	}
	if rows.JobID != child.ID || len(rows.Items) != 1 {
		out.OutcomeReason = "child_evidence_not_retained"
		return nil
	}
	item := rows.Items[0]
	if item.WorkflowRunID != a.RunID || item.ExpectedResumeCount == nil || *item.ExpectedResumeCount != target.ExpectedResumeCount || item.ParentJobID != p.JobID || item.ParentPosition == nil || *item.ParentPosition != a.Position {
		return fmt.Errorf("recovery review child target does not match frozen plan")
	}
	switch item.State {
	case "pending":
		out.Outcome = "pending"
		out.OutcomeReason = "admission_pending"
	case "skipped", "cancelled":
		out.OutcomeReason = "admission_" + item.State
		// Terminal admission is evidence that this exact child action will not run.
		if child.PendingCount == 0 && child.CompletedAt != nil && !child.CompletedAt.IsZero() && (child.State == "completed" || child.State == "cancelled" || child.State == "expired") {
			out.Complete = true
			out.EvidenceSource = "recovery_admission"
			out.RecordedAt = cloneEventReceiptTime(child.CompletedAt)
			out.Outcome = "failed"
			if item.State == "cancelled" {
				out.Outcome = "cancelled"
			}
		}
	case "queued":
		e := item.Execution
		if e == nil {
			out.OutcomeReason = "execution_evidence_unavailable"
			return nil
		}
		out.EvidenceSource = e.EvidenceSource
		switch e.State {
		case "queued":
			out.Outcome = "pending"
		case "running", "retrying":
			out.Outcome = "running"
		case "succeeded", "failed", "cancelled":
			if e.Source == "recovery_result" && e.EvidenceSource == "workflow_run" && e.RecordedAt != nil && !e.RecordedAt.IsZero() {
				out.Outcome = e.State
				out.Complete = true
				out.RecordedAt = cloneEventReceiptTime(e.RecordedAt)
			} else {
				out.OutcomeReason = "terminal_outcome_not_saved"
			}
		default:
			out.OutcomeReason = "execution_evidence_unavailable"
		}
	}
	return nil
}
