package state

import (
	"github.com/onebox-faas/faas/pkg/api"
	"net/url"
	"time"
)

func initialEvidenceHistoryBase(r api.WorkflowInitialRecoveryReview) api.WorkflowRecoveryHistoryEntry {
	count := r.Target.ExpectedResumeCount + 1
	base := "/v1/apps/" + url.PathEscape(r.App) + "/initial-recovery-reviews/" + r.ID
	e := api.WorkflowRecoveryHistoryEntry{InitialFailureID: r.Failure.ID, EvidenceKind: "initial", ResumeCount: &count, PolicyVersion: r.PolicyVersion, Workflow: r.Failure.Workflow, RunID: r.Target.RunID, ReviewID: r.ID, ActionID: r.ID, JobID: r.JobID, ReviewURL: base, EvidenceURL: base + "/evidence"}
	if r.JobID != "" {
		e.JobURL = "/v1/event-recoveries/" + r.JobID
	}
	return e
}
func initialRepairHistory(r api.WorkflowInitialRecoveryReview) api.WorkflowRecoveryHistoryEntry {
	e := initialEvidenceHistoryBase(r)
	receipt := r.EvidenceRepair
	e.ID = "initial_evidence_repaired:" + r.ID + ":" + receipt.RequestID
	e.Kind, e.OccurredAt, e.Outcome, e.Reason, e.ActorKind, e.ActorID, e.RequestID = "initial_evidence_repaired", receipt.RepairedAt, receipt.Outcome, receipt.Reason, receipt.ActorKind, receipt.ActorID, receipt.RequestID
	return e
}
func initialAlertHistory(r api.WorkflowInitialRecoveryReview, p api.WorkflowRecoveryQueueBlockedWebhookPayload) api.WorkflowRecoveryHistoryEntry {
	e := initialEvidenceHistoryBase(r)
	e.Kind = "initial_recovery_blocked"
	if p.State == "recovered" {
		e.Kind = "initial_recovery_cleared"
	}
	e.OccurredAt, e.Outcome, e.Reason, e.NotificationEventID, e.DiagnosisReason = p.ObservedAt, p.State, p.Reason, p.EventID, p.DiagnosisReason
	e.ActorKind, e.ActorID = "system", "recovery_initial_worker"
	if r.Policy.Mode == "preview" {
		e.ActorID = "initial_completion_observer"
	}
	return e
}

// Only mutation paths call this after a new review event has been committed.
func initialLifecycleHistory(r api.WorkflowInitialRecoveryReview) api.WorkflowRecoveryHistoryEntry {
	event := r.Events[len(r.Events)-1]
	e := initialEvidenceHistoryBase(r)
	e.Kind = "initial_recovery_" + event.Status
	e.OccurredAt, e.Outcome, e.Reason, e.ActorKind, e.ActorID = event.OccurredAt, event.Status, event.Reason, event.ActorKind, event.ActorID
	result := r.Result
	if result == nil {
		result = r.AutomaticResult
	}
	if event.Reason == "result_recorded" && result != nil {
		e.Kind, e.Outcome, e.Reason = "initial_recovery_result", result.Outcome, result.OutcomeReason
	}
	return e
}

func (m *MemStore) recordInitialLifecycleLocked(r api.WorkflowInitialRecoveryReview) {
	if p := m.recoveryPolicies[canonicalMemUUID(r.AppID)]; p != nil {
		p.appendHistoryEvent(initialLifecycleHistory(r), time.Now().UTC())
	}
}
