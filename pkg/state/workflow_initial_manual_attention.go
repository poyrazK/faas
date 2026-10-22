package state

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func initialManualAttentionTransition(r *api.WorkflowInitialRecoveryReview, reason string, now time.Time, d api.WorkflowInitialEvidenceDiagnosis) *api.WorkflowRecoveryQueueBlockedWebhookPayload {
	if r.Policy.Mode != "preview" || r.Status != "submitted" || r.SubmittedAt == nil || r.Target.ExpectedResumeCount != 0 {
		return nil
	}
	if r.Attention == nil {
		r.Attention = &api.WorkflowInitialRecoveryAttention{}
	}
	a := r.Attention
	a.Reason, a.ObservedAt, a.Diagnosis = reason, now, &d
	base := "/v1/apps/" + url.PathEscape(r.App) + "/initial-recovery-reviews/" + r.ID
	a.EvidenceURL = base + "/evidence"
	a.CLICommand = fmt.Sprintf("gregale automations failure-recovery-evidence --app %s --review %s", r.App, r.ID)
	a.OperatorAction = initialManualOperatorAction(reason, d)
	// A retained pending item or a policy change cannot prove an evidence blocker
	// cleared. Only confirmed results or exact-generation execution progress can.
	switch reason {
	case "child_created", "admission_wait":
		return nil
	case "result_recorded":
		if r.Result == nil {
			return nil
		}
	case "execution_wait", "recovery_running":
		if d.Execution == nil || d.Execution.Source != "workflow_run" || d.RunID != r.Target.RunID || d.ResumeCount != 1 || (d.Execution.State != "queued" && d.Execution.State != "running") {
			return nil
		}
	}
	// Adapt only the pure transition state, preserving the frozen manual policy.
	frame := *r
	frame.AutomaticBlockReason, frame.AutomaticBlockSequence, frame.AutomaticBlockNotification = a.BlockReason, a.Sequence, a.Notification
	payload := initialRecoveryBlockTransition(&frame, reason, now, d)
	if payload == nil {
		return nil
	}
	a.BlockReason, a.Sequence = frame.AutomaticBlockReason, frame.AutomaticBlockSequence
	payload.QueueURL = "/v1/apps/" + url.PathEscape(r.App) + "/initial-recovery-reviews"
	if payload.State == "blocked" {
		payload.OperatorAction = a.OperatorAction
	}
	return payload
}

func initialManualOperatorAction(reason string, d api.WorkflowInitialEvidenceDiagnosis) string {
	switch reason {
	case "job_paused":
		return "Inspect the approved recovery job and resume it when safe."
	case "evidence_unknown", "terminal_outcome_not_saved":
		if d.Repairable {
			return "Verified terminal proof is repairable. Recheck diagnosis, then append --repair --request-id REQUEST_UUID --reason 'Restore verified outcome'. Completion tracking will verify the saved proof."
		}
		return "Inspect the exact initial recovery generation using the diagnosis link or CLI command; missing or conflicting proof cannot be forced."
	case "result_recorded":
		return "Inspect the verified result on the review and the recovery job."
	case "execution_wait", "recovery_running":
		return "Inspect current execution progress; progress does not prove successful completion."
	default:
		return "Inspect native recovery admission and the retained generation-one evidence."
	}
}

func (m *MemStore) captureInitialManualAttentionLocked(account string, r *api.WorkflowInitialRecoveryReview, reason string, now time.Time) error {
	d := m.initialEvidenceLocked(*r, now)
	payload := initialManualAttentionTransition(r, reason, now, d)
	if payload == nil {
		return nil
	}
	receipt, err := m.captureInitialRecoveryAttentionLocked(account, r, payload, r.Attention.Sequence, now)
	if err != nil {
		return err
	}
	r.Attention.Notification = receipt
	return nil
}

func captureInitialManualAttentionTx(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, account string, r *api.WorkflowInitialRecoveryReview, reason string, now time.Time) error {
	d, err := initialEvidenceTx(ctx, q, tx, account, *r, now)
	if err != nil {
		return err
	}
	payload := initialManualAttentionTransition(r, reason, now, d)
	if payload == nil {
		return nil
	}
	receipt, err := captureInitialRecoveryAttentionTx(ctx, q, tx, account, r, payload, r.Attention.Sequence, now)
	if err != nil {
		return err
	}
	r.Attention.Notification = receipt
	return nil
}
