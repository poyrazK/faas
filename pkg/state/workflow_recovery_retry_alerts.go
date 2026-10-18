package state

import (
	"fmt"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// Receipt state tracks evidence attention, never execution authority or success.
func automaticEvidenceTransition(app, slug, review string, version int64, e api.WorkflowRecoveryAutomaticAction, d api.WorkflowRetryEvidenceDiagnosis, now time.Time) *api.WorkflowRecoveryQueueBlockedWebhookPayload {
	previous := e.BlockedNotification
	cleared := d.Reason == "already_recorded" || d.Reason == "already_confirmed"
	if d.EvidenceKind == "child" && d.Reason == "run_not_terminal" && d.Execution != nil && d.Execution.Source == "workflow_run" && d.Execution.State != "unknown" && d.Execution.State != "superseded" {
		cleared = true
	}
	// A pending parent running is no longer the saved failure supporting this plan.
	if d.EvidenceKind == "parent" && d.Reason == "run_not_terminal" && d.Execution != nil && d.Execution.Source == "workflow_run" && d.Execution.State != "unknown" {
		return nil
	}
	state, reason := "blocked", "evidence_unknown"
	if d.Repairable {
		reason = "terminal_outcome_not_saved"
	}
	if cleared {
		if previous == nil || previous.State == "recovered" {
			return nil
		}
		state, reason = "recovered", "blocker_cleared"
	} else if d.Reason == "retry_not_pending" || d.Reason == "retry_not_applied" {
		return nil
	}
	if previous != nil && previous.State == state && previous.EvidenceKind == d.EvidenceKind && previous.DiagnosisReason == d.Reason {
		return nil
	}
	payload := automaticBlockedPayload(app, slug, review, version, e, now)
	sequence := int64(1)
	if previous != nil {
		sequence = previous.Sequence + 1
		if sequence < 2 {
			sequence = 2
		}
		payload.PreviousReason = previous.Reason
	}
	if sequence > 1 {
		payload.EventID = uuid.NewSHA1(uuid.NameSpaceURL, []byte(fmt.Sprintf("gregale:recovery-action:%s:blocked:%d", e.ActionID, sequence))).String()
	}
	payload.State, payload.Reason = state, reason
	payload.EvidenceKind, payload.DiagnosisReason = d.EvidenceKind, d.Reason
	count := int64(d.ResumeCount)
	payload.RunID, payload.ResumeCount, payload.Repairable = d.RunID, &count, &d.Repairable
	base := "/v1/apps/" + url.PathEscape(slug) + "/recovery-policy/reviews/" + review + "/actions/" + e.ActionID + "/evidence"
	payload.CLICommand = fmt.Sprintf("gregale automations recovery-retry-evidence --app %s --review %s --action %s", slug, review, e.ActionID)
	if d.EvidenceKind == "parent" {
		base += "/parent"
		payload.CLICommand += " --parent"
	}
	payload.EvidenceURL = base
	if d.JobID != "" {
		payload.JobURL = "/v1/event-recoveries/" + d.JobID
	}
	payload.OperatorAction = "Inspect exact generation evidence using the diagnosis URL or CLI command; missing or conflicting proof cannot be forced."
	if d.Repairable {
		payload.OperatorAction = "Verified terminal proof is repairable. Recheck diagnosis, then use the CLI command with --repair --request-id REQUEST_UUID --reason 'Restore verified outcome'. Normal worker checks still govern execution."
	}
	if cleared {
		payload.OperatorAction = "Evidence blocker cleared; inspect current progress. This does not prove success or authorize another retry."
	}
	return &payload
}

func automaticEvidenceReceipt(payload api.WorkflowRecoveryQueueBlockedWebhookPayload, previous *api.WorkflowRecoveryAutomaticBlockedNotification, receivers []string) api.WorkflowRecoveryAutomaticBlockedNotification {
	r := automaticBlockedReceipt(payload, receivers)
	r.Sequence = 1
	if previous != nil {
		r.Sequence = previous.Sequence + 1
		if r.Sequence < 2 {
			r.Sequence = 2
		}
	}
	return r
}
