package state

import (
	"github.com/onebox-faas/faas/pkg/api"
	"net/url"
)

func retryRepairHistory(r api.WorkflowRecoveryPolicyReview, e api.WorkflowRecoveryAutomaticAction, kind string, receipt api.WorkflowInitialEvidenceRepairReceipt) api.WorkflowRecoveryHistoryEntry {
	out := api.WorkflowRecoveryHistoryEntry{ID: "retry_" + kind + "_evidence_repaired:" + e.ActionID + ":" + receipt.RequestID, Kind: "retry_" + kind + "_evidence_repaired", OccurredAt: receipt.RepairedAt, Outcome: receipt.Outcome, Reason: receipt.Reason, PolicyVersion: r.PolicyVersion, ReviewID: r.ID, ActionID: e.ActionID, JobID: r.JobID, EvidenceKind: kind, RequestID: receipt.RequestID, ActorKind: receipt.ActorKind, ActorID: receipt.ActorID}
	if r.Plan == nil {
		return out
	}
	out.ReviewURL = "/v1/apps/" + url.PathEscape(r.Plan.App) + "/recovery-policy/reviews/" + r.ID
	out.EvidenceURL = out.ReviewURL + "/actions/" + e.ActionID + "/evidence"
	affected := e.ChildJobID
	if kind == "parent" {
		affected = r.JobID
		out.EvidenceURL += "/parent"
	} else {
		out.ChildJobID = e.ChildJobID
	}
	if affected != "" {
		out.JobURL = "/v1/event-recoveries/" + affected
	}
	for _, action := range r.Plan.Actions {
		if action.Position != e.Position || action.Retry == nil || action.Retry.WorkflowRuns == nil || len(action.Retry.WorkflowRuns.Runs) != 1 {
			continue
		}
		target := action.Retry.WorkflowRuns.Runs[0]
		count := target.ExpectedResumeCount
		if kind == "child" {
			count++
		}
		out.Workflow, out.RunID, out.ResumeCount = action.Retry.WorkflowRuns.Name, target.RunID, &count
		break
	}
	return out
}

func retryAlertHistory(r api.WorkflowRecoveryPolicyReview, payload api.WorkflowRecoveryQueueBlockedWebhookPayload) api.WorkflowRecoveryHistoryEntry {
	kind := "retry_evidence_blocked"
	if payload.State == "recovered" {
		kind = "retry_evidence_cleared"
	}
	out := api.WorkflowRecoveryHistoryEntry{Kind: kind, OccurredAt: payload.ObservedAt, Outcome: payload.State, Reason: payload.Reason, PolicyVersion: payload.PolicyVersion, RunID: payload.RunID, ReviewID: payload.ReviewID, ActionID: payload.ActionID, JobID: r.JobID, ChildJobID: payload.ChildJobID, EvidenceKind: payload.EvidenceKind, NotificationEventID: payload.EventID, DiagnosisReason: payload.DiagnosisReason, EvidenceURL: payload.EvidenceURL, JobURL: payload.JobURL, ActorKind: "system", ActorID: "recovery_evidence_monitor"}
	if payload.ResumeCount != nil {
		count := int(*payload.ResumeCount)
		out.ResumeCount = &count
	}
	if r.Plan != nil {
		out.ReviewURL = "/v1/apps/" + url.PathEscape(r.Plan.App) + "/recovery-policy/reviews/" + r.ID
		for _, action := range r.Plan.Actions {
			if action.Position == payload.Position && action.Retry != nil && action.Retry.WorkflowRuns != nil {
				out.Workflow = action.Retry.WorkflowRuns.Name
				break
			}
		}
	}
	return out
}

func recoveryHistoryHasRepair(p *memWorkflowRecoveryPolicy, action, kind, request string) bool {
	for _, event := range p.History {
		if event.Entry.ActionID == action && event.Entry.Kind == "retry_"+kind+"_evidence_repaired" && event.Entry.RequestID == request {
			return true
		}
	}
	return false
}
