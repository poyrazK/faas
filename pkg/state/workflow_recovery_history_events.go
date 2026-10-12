package state

import (
	"github.com/onebox-faas/faas/pkg/api"
	"time"
)

type memRecoveryHistoryEvent struct {
	Sequence   int64
	Entry      api.WorkflowRecoveryHistoryEntry
	RecordedAt time.Time
}

func recoveryPolicyHistoryEvent(p api.WorkflowRecoveryPolicyResponse) api.WorkflowRecoveryHistoryEntry {
	policy := recoveryPolicyClone(p.Policy)
	return api.WorkflowRecoveryHistoryEntry{OccurredAt: p.UpdatedAt, Kind: "policy_changed", Outcome: p.Status, Reason: p.Policy.Reason, PolicyVersion: p.Version, ActorKind: p.ActorKind, ActorID: p.ActorID, Policy: &policy}
}

// Only real transitions append events; repeated observations remain on the live row.
func recoveryHealthHistoryEvents(previous, next *api.WorkflowRecoveryHealthIncident, version int64) []api.WorkflowRecoveryHistoryEntry {
	events := []api.WorkflowRecoveryHistoryEntry{}
	if next == nil {
		return events
	}
	base := api.WorkflowRecoveryHistoryEntry{OccurredAt: next.ObservedAt, Outcome: next.Status, Reason: next.Reason, PolicyVersion: version, EpisodeID: next.EpisodeID, ActorKind: "system", ActorID: "recovery_health_monitor"}
	same := previous != nil && previous.EpisodeID == next.EpisodeID
	if !same {
		e := base
		e.Kind = "health_opened"
		e.OccurredAt = next.OpenedAt
		events = append(events, e)
	} else if previous.ClosedAt == nil && next.ClosedAt != nil {
		e := base
		e.Kind = "health_closed"
		e.OccurredAt = *next.ClosedAt
		events = append(events, e)
	} else if previous.Status != next.Status || previous.Reason != next.Reason {
		e := base
		e.Kind = "health_changed"
		events = append(events, e)
	}
	for _, capture := range []struct {
		kind          string
		before, after *api.WorkflowRecoveryHealthNotification
	}{
		{"health_alert_captured", nil, next.AlertNotification}, {"health_recovered_captured", nil, next.RecoveryNotification},
	} {
		if same {
			if capture.kind == "health_alert_captured" {
				capture.before = previous.AlertNotification
			} else {
				capture.before = previous.RecoveryNotification
			}
		}
		if capture.after != nil && capture.before == nil {
			e := base
			e.Kind = capture.kind
			e.OccurredAt = capture.after.CapturedAt
			e.Outcome = capture.after.Status
			events = append(events, e)
		}
	}
	return events
}

func (p *memWorkflowRecoveryPolicy) appendHistoryEvent(e api.WorkflowRecoveryHistoryEntry, recordedAt time.Time) {
	// PostgreSQL timestamps have microsecond precision; normalize before JSON capture.
	e.OccurredAt = e.OccurredAt.UTC().Truncate(time.Microsecond)
	e.ID = "audit:" + canonicalMemUUID(newID())
	e.Durable = true
	cutoff := recordedAt.Add(-api.WorkflowRecoveryHistoryRetention)
	retained := p.History[:0]
	for _, existing := range p.History {
		if existing.RecordedAt.After(cutoff) {
			retained = append(retained, existing)
		}
	}
	clear(p.History[len(retained):])
	p.HistorySequence++
	p.History = append(retained, memRecoveryHistoryEvent{Sequence: p.HistorySequence, Entry: recoveryPolicyClone(e), RecordedAt: recordedAt})
	if len(p.History) > api.WorkflowRecoveryHistoryEventsMax {
		p.History = append([]memRecoveryHistoryEvent(nil), p.History[len(p.History)-api.WorkflowRecoveryHistoryEventsMax:]...)
	}
}

func (m *MemStore) recordRecoveryEvaluationLocked(p *memWorkflowRecoveryPolicy, a *api.WorkflowRecoveryAutomaticAction, reason string, now time.Time, result *api.WorkflowRecoveryReviewActionStatus) {
	previous := a.LastEvaluation
	a.LastEvaluation = recoveryAutomaticEvaluation(previous, reason, now)
	if previous != nil && previous.Reason == reason {
		return
	}
	review, ok := recoveryAutomaticMemReview(p, a.ActionID)
	if !ok {
		return
	}
	e := api.WorkflowRecoveryHistoryEntry{OccurredAt: now, Kind: "retry_transition", Outcome: a.Status, Reason: reason, EvaluationReason: reason, ActorKind: "system", ActorID: "recovery_automatic_worker", PolicyVersion: review.PolicyVersion, ReviewID: review.ID, JobID: review.JobID, ActionID: a.ActionID, ChildJobID: a.ChildJobID}
	if reason == "suppressed" {
		e.Reason = a.Reason
	}
	if reason == "result_recorded" && result != nil {
		e.Outcome = result.Outcome
		e.Reason = result.OutcomeReason
	}
	for _, decision := range review.Decisions {
		if decision.Position == a.Position {
			e.RunID = canonicalMemUUID(decision.RunID)
			break
		}
	}
	if job := m.eventRecoveryJobs[canonicalMemUUID(review.JobID)]; job != nil && job.Job.Selection.WorkflowRuns != nil {
		e.Workflow = job.Job.Selection.WorkflowRuns.Name
	}
	p.appendHistoryEvent(e, time.Now().UTC())
}

func recoveryAutomaticPauseHistoryEvent(p api.WorkflowRecoveryPolicyResponse) api.WorkflowRecoveryHistoryEntry {
	return api.WorkflowRecoveryHistoryEntry{OccurredAt: *p.AutomaticExecution.PausedAt, Kind: "policy_paused", Outcome: "paused", Reason: p.AutomaticExecution.PauseReason, PolicyVersion: p.Version, ActorKind: "system", ActorID: "recovery_policy:" + p.AppID}
}

func recoveryCandidateHistoryEvent(r api.WorkflowRecoveryPolicyReview, workflow string) api.WorkflowRecoveryHistoryEntry {
	return api.WorkflowRecoveryHistoryEntry{OccurredAt: r.ObservedAt, Kind: "candidates_observed", Outcome: "recorded", Reason: "policy_review", PolicyVersion: r.PolicyVersion, ReviewID: r.ID, JobID: r.JobID, Workflow: workflow, ActorKind: "system", ActorID: "recovery_policy", CandidateEvidence: &api.WorkflowRecoveryCandidateEvidence{ReviewID: r.ID, AppID: r.AppID, JobID: r.JobID, PolicyVersion: r.PolicyVersion, Policy: recoveryEvaluationPolicyCopy(r.Policy), ObservedAt: r.ObservedAt.UTC().Truncate(time.Microsecond), Decisions: recoveryPolicyClone(r.Decisions)}}
}
func recoveryCandidateEvidenceReview(e api.WorkflowRecoveryHistoryEntry) recoveryEvaluationReview {
	c := e.CandidateEvidence
	return recoveryEvaluationReview{evidenceID: e.ID, workflow: e.Workflow, review: api.WorkflowRecoveryPolicyReview{ID: c.ReviewID, AppID: c.AppID, JobID: c.JobID, PolicyVersion: c.PolicyVersion, Policy: c.Policy, ObservedAt: c.ObservedAt, Decisions: recoveryPolicyClone(c.Decisions)}}
}
