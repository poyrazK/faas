package state

import (
	"fmt"
	"strings"
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
		if event.OutcomeChange != nil {
			outcome := event.OutcomeChange.After.Evidence
			e.ExpectedOutcomeCode = outcome.ExpectedOutcomeCode
			e.ReportedOutcomeCode = outcome.ReportedOutcomeCode
			e.StateRevision = outcome.StateRevision
			links(e, outcome.ReportingOperationID)
		}
		for _, change := range event.Changes {
			links(e, change.After.ReportingOperationID)
			if change.After.Evidence != nil {
				links(e, change.After.Evidence.OperationID)
			}
		}
	}
	if regression := n.DependencyRegression; regression != nil {
		labels := []string{}
		for _, step := range regression.AffectedSteps {
			labels = append(labels, step.OutcomeCode+": "+step.Label)
		}
		e.Summary = "Follow-up dependency regression: " + strings.Join(labels, ", ")
	}
	if recovery := n.DependencyRecovery; recovery != nil {
		labels := []string{}
		for _, step := range recovery.ReadySteps {
			labels = append(labels, step.OutcomeCode+": "+step.Label)
		}
		e.Summary = "Follow-up dependency recovery: " + strings.Join(labels, ", ") + "; regression notice " + fmt.Sprint(recovery.Regression.Sequence)
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
			if result := completion.PlaybookOutcome; result != nil {
				e.Summary = "Playbook completion: " + result.Resolution + " · " + result.Alignment + ". " + result.Explanation
				e.ExpectedOutcomeCode = result.Observation.Evidence.ExpectedOutcomeCode
				e.ReportedOutcomeCode = result.Observation.Evidence.ReportedOutcomeCode
				e.StateRevision = result.Observation.Evidence.StateRevision
				if n.PlaybookEscalation != nil {
					e.PlaybookProgressID = n.PlaybookEscalation.ProgressID
				}
				links(e, result.Observation.Evidence.ReportingOperationID)
				observation("playbook_completion_outcome", result.Observation.Evidence)
			}
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
