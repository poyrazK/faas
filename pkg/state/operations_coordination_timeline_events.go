package state

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func readinessCoordinationTimelineEvents(s api.OperationWorkflowReadinessSubscription, n api.OperationWorkflowReadinessNotification) ([]api.OperationWorkflowCoordinationTimelineEvent, bool) {
	events := []api.OperationWorkflowCoordinationTimelineEvent{}
	truncated := false
	add := func(key, kind, summary string, at time.Time) *api.OperationWorkflowCoordinationTimelineEvent {
		events = append(events, api.OperationWorkflowCoordinationTimelineEvent{ID: s.ID + "/" + n.ID + "/" + key, At: at, Kind: kind, Summary: summary, SubscriptionID: s.ID, SubscriptionName: s.Name, SubscriptionTenantID: s.TenantID, NotificationID: n.ID, Sequence: n.Sequence, ReportingOperationIDs: []string{}})
		return &events[len(events)-1]
	}
	links := func(e *api.OperationWorkflowCoordinationTimelineEvent, ids ...string) {
		for _, id := range ids {
			if id == "" || len(e.ReportingOperationIDs) >= api.OperationWorkflowCoordinationTimelineOperationLinksMax {
				continue
			}
			exists := false
			for _, prior := range e.ReportingOperationIDs {
				if prior == id {
					exists = true
					break
				}
			}
			if !exists {
				e.ReportingOperationIDs = append(e.ReportingOperationIDs, id)
			}
		}
	}
	observation := func(key string, current api.OperationWorkflowReadinessBusinessOutcome) {
		e := add(key, "outcome_observation", current.Explanation, current.CheckedAt)
		if key == "watch_outcome" || key == "escalation_outcome" {
			e.Summary = "Latest retained outcome observation: " + current.Explanation
		}
		e.Status = current.Status
		e.ExpectedOutcomeCode = current.ExpectedOutcomeCode
		e.ReportedOutcomeCode = current.ReportedOutcomeCode
		e.StateRevision = current.StateRevision
		links(e, current.ReportingOperationID)
	}
	e := add("notification", "notification", n.Explanation, n.CreatedAt)
	e.Status = n.Kind
	if event := n.PlaybookEvidence; event != nil {
		e.PlaybookProgressID = event.ProgressID
		for _, change := range event.Changes {
			links(e, change.After.ReportingOperationID)
			if change.After.Evidence != nil {
				links(e, change.After.Evidence.OperationID)
			}
		}
	}
	if n.Change != nil {
		links(e, n.Change.CurrentOperationID)
	}
	if n.BusinessOutcome != nil {
		links(e, n.BusinessOutcome.Current.ReportingOperationID)
		e.ExpectedOutcomeCode = n.BusinessOutcome.Current.ExpectedOutcomeCode
		e.ReportedOutcomeCode = n.BusinessOutcome.Current.ReportedOutcomeCode
	}
	if n.Verification != nil {
		for _, change := range n.Verification.Changes {
			links(e, change.After.ReportingOperationID, change.After.Proof.ResolutionOperationID, change.After.Proof.Resolution.VerificationOperationID)
		}
	}
	if c := n.Coordination; c != nil {
		e = add("coordination", "coordination_snapshot", "Latest retained coordination edit; earlier edits are not reconstructed.", c.UpdatedAt)
		e.Actor = c.UpdatedBy
		e.Owner = c.Owner
		e.Status = c.Status
		e.Note = c.Note
		e.FollowUpAt = c.FollowUpAt
		if c.AcknowledgedAt != nil {
			e = add("acknowledgment", "acknowledgment", "Retained acknowledgment.", *c.AcknowledgedAt)
			e.Actor = c.AcknowledgedBy
		}
		if completion := c.Completion; completion != nil {
			e = add("completion", "completion", "Recorded follow-up completion; this does not establish business success.", completion.CompletedAt)
			e.Actor = completion.CompletedBy
			e.Status = completion.Outcome
			e.Note = completion.Note
			e.ExpectedOutcomeCode = completion.ExpectedOutcomeCode
			links(e, completion.EvidenceOperationIDs...)
			links(e, completion.Evidence.ReportingOperationID)
			if completion.BusinessOutcome != nil {
				observation("completion_outcome", *completion.BusinessOutcome)
			}
		}
	}
	if n.Reminder != nil {
		e = add("reminder", "reminder", "Retained follow-up reminder delivery.", n.Reminder.CreatedAt)
		e.FollowUpAt = &n.Reminder.DueAt
	}
	if n.BusinessOutcomeWatch != nil {
		observation("watch_outcome", n.BusinessOutcomeWatch.Current)
	}
	if escalation := n.OutcomeEscalation; escalation != nil {
		observation("escalation_outcome", escalation.Current)
		if escalation.SuccessObservedAt != nil {
			add("success_flag", "application_success_observed", "First application success flag retained on this escalation; its original proof identity may no longer be available.", *escalation.SuccessObservedAt)
		}
		if response := escalation.Response; response != nil {
			if response.FirstAcknowledgedAt != nil && (n.Coordination == nil || n.Coordination.AcknowledgedAt == nil || !n.Coordination.AcknowledgedAt.Equal(*response.FirstAcknowledgedAt)) {
				add("first_acknowledgment", "acknowledgment", "First response-target acknowledgment flag; original actor is not retained.", *response.FirstAcknowledgedAt)
			}
			if response.DeadlineNotifiedAt != nil {
				add("response_deadline", "response_deadline_notice", "Missed response-target deadline notice was published; this is coordination, not business failure.", *response.DeadlineNotifiedAt)
			}
			history := response.History
			truncated = response.HistoryTruncated || len(history) > api.OperationWorkflowReadinessAssignmentHistoryMax
			if len(history) > api.OperationWorkflowReadinessAssignmentHistoryMax {
				history = history[len(history)-api.OperationWorkflowReadinessAssignmentHistoryMax:]
			}
			for _, change := range history {
				e = add("assignment/"+readinessDigest(change), "assignment", change.FromOwner+" → "+change.ToOwner+" · "+change.Reason, change.At)
				e.Actor = change.Actor
				e.Owner = change.ToOwner
			}
		}
	}
	return events, truncated
}
