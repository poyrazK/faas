package state

import (
	"fmt"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// Normal budget/cooldown waits and running jobs are quiet. Attention is tied to
// committed worker evidence, never a read or a hypothetical policy preview.
func initialAutomaticBlockTransition(r *api.WorkflowInitialRecoveryReview, reason string, now time.Time, diagnoses ...api.WorkflowInitialEvidenceDiagnosis) *api.WorkflowRecoveryQueueBlockedWebhookPayload {
	if r.Policy.Mode != "automatic_initial" {
		return nil
	}
	return initialRecoveryBlockTransition(r, reason, now, diagnoses...)
}

// This pure transition helper grants no execution authority.
func initialRecoveryBlockTransition(r *api.WorkflowInitialRecoveryReview, reason string, now time.Time, diagnoses ...api.WorkflowInitialEvidenceDiagnosis) *api.WorkflowRecoveryQueueBlockedWebhookPayload {
	action := ""
	switch reason {
	case "evidence_unknown":
		action = "Inspect retained run and recovery job evidence; restore missing evidence before another recovery."
	case "terminal_outcome_not_saved":
		action = "Inspect the result recorder and retain the exact resumed generation outcome."
	case "execution_quota":
		action = "Inspect active recovery jobs across the account and free capacity."
	case "job_paused":
		action = "Inspect the recovery job and resume it when safe."
	}
	previous := r.AutomaticBlockReason
	state := "blocked"
	if action == "" {
		switch reason {
		case "child_created", "result_recorded", "admission_wait", "execution_wait", "recovery_running":
			if previous == "" {
				return nil
			}
			state, reason = "recovered", "blocker_cleared"
			action = "Inspect the review and job for current progress; clearing a blocker does not prove success."
		default:
			return nil
		}
	} else if previous == reason {
		if (reason != "evidence_unknown" && reason != "terminal_outcome_not_saved") || len(diagnoses) == 0 || r.AutomaticBlockNotification == nil || r.AutomaticBlockNotification.DiagnosisReason == diagnoses[0].Reason {
			return nil
		}
	}
	r.AutomaticBlockSequence++
	r.AutomaticBlockReason = reason
	if state == "recovered" {
		r.AutomaticBlockReason = ""
	}
	id := uuid.NewSHA1(uuid.NameSpaceURL, []byte(fmt.Sprintf("gregale:initial-recovery:%s:block:%d", r.ID, r.AutomaticBlockSequence))).String()
	base := "/v1/apps/" + url.PathEscape(r.App)
	status := "pending"
	if r.SubmittedAt != nil {
		status = "applied"
	}
	payload := &api.WorkflowRecoveryQueueBlockedWebhookPayload{EventID: id, AppID: r.AppID, App: r.App, ReviewID: r.ID, ActionID: r.ID, PolicyVersion: r.PolicyVersion, Position: 1, ChildJobID: r.JobID, Status: status, Reason: reason, ObservedAt: now, QueueURL: base + "/recovery-policy/queue", ReviewStatusURL: base + "/initial-recovery-reviews/" + r.ID, InitialFailureID: r.Failure.ID, State: state, PreviousReason: previous, OperatorAction: action}
	if r.JobID != "" {
		payload.JobURL = "/v1/event-recoveries/" + r.JobID
	}
	if len(diagnoses) > 0 {
		d := diagnoses[0]
		count := int64(d.ResumeCount)
		payload.EvidenceKind, payload.RunID, payload.ResumeCount, payload.Repairable, payload.DiagnosisReason = "initial", d.RunID, &count, &d.Repairable, d.Reason
		payload.EvidenceURL = base + "/initial-recovery-reviews/" + r.ID + "/evidence"
		payload.CLICommand = fmt.Sprintf("gregale automations failure-recovery-evidence --app %s --review %s", r.App, r.ID)
		if state == "blocked" && (reason == "evidence_unknown" || reason == "terminal_outcome_not_saved") {
			payload.OperatorAction = "Inspect the exact initial recovery generation using the diagnosis link or CLI command; missing or conflicting proof cannot be forced."
			if d.Repairable {
				payload.OperatorAction = "Verified terminal proof is repairable. Recheck diagnosis, then append --repair --request-id REQUEST_UUID --reason 'Restore verified outcome'. Normal worker checks still govern execution."
			}
		}
	}
	return payload
}

func initialAttentionNeedsDiagnosis(r api.WorkflowInitialRecoveryReview, reason string) bool {
	if r.Policy.Mode != "automatic_initial" {
		return false
	}
	switch reason {
	case "evidence_unknown", "terminal_outcome_not_saved", "execution_quota", "job_paused":
		return true
	case "child_created", "result_recorded", "admission_wait", "execution_wait", "recovery_running":
		return r.AutomaticBlockReason != ""
	}
	return false
}
