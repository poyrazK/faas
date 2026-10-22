package state

import (
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func workflowActionObligation(row api.OperationWorkflowReadinessFollowUp, kind string) *api.OperationWorkflowActionObligation {
	epoch := row.CreatedAt
	response := row.ReviewResponse
	if response != nil {
		review := response.DecisionReview
		switch kind {
		case "response_check_in":
			if response.CheckInAt != nil {
				epoch = *response.CheckInAt
			}
		case "decision_revalidation":
			if review != nil {
				epoch = review.RequiredAt
			}
		case "handoff_acceptance":
			if review != nil {
				epoch = review.RequiredAt
				if review.HandedOffAt != nil {
					epoch = *review.HandedOffAt
				}
			}
		case "handoff_reassignment":
			if review != nil && review.Decline != nil {
				epoch = review.Decline.At
			}
		case "escalation_acknowledgment":
			if review != nil && review.AcceptanceEscalatedAt != nil {
				epoch = *review.AcceptanceEscalatedAt
			}
		case "coordinator_completion":
			if review != nil && review.EscalationAcknowledgment != nil {
				epoch = review.EscalationAcknowledgment.At
			}
		case "scheduled_evidence_check", "coordinator_evidence_review", "coordinator_revision":
			if review != nil && review.EscalationAcknowledgment != nil && review.EscalationAcknowledgment.Completion != nil {
				completion := review.EscalationAcknowledgment.Completion
				epoch = completion.At
				if kind == "coordinator_revision" && completion.Review != nil {
					epoch = completion.Review.At
				}
			}
		}
	}
	if epoch.IsZero() {
		return nil
	}
	epoch = epoch.UTC()
	revision := int64(0)
	if kind == "dependency_review" && row.PlaybookStep != nil && row.PlaybookStep.DependencyReview != nil {
		revision = row.PlaybookStep.DependencyReview.DependencyReviewRevision
	}
	id := readinessDigest([]any{row.SubscriptionID, row.Sequence, kind, epoch, revision})
	return &api.OperationWorkflowActionObligation{ID: id, StartedAt: epoch}
}

// Historical receipts are included only when they carry the saved epoch themselves.
func workflowObligationReceipts(n api.OperationWorkflowReadinessNotification, action api.OperationWorkflowCaseNextAction, observedAt time.Time) ([]api.OperationWorkflowObligationEvent, string) {
	events := []api.OperationWorkflowObligationEvent{}
	if action.Obligation == nil {
		return events, ""
	}
	epoch := action.Obligation.StartedAt
	terminal := ""
	add := func(kind string, at time.Time, actor, explanation string) {
		if at.IsZero() {
			return
		}
		event := api.OperationWorkflowObligationEvent{Kind: kind, At: at, Actor: actor, Explanation: explanation}
		if !hasObligationEvent(events, event) {
			events = append(events, event)
		}
	}
	add("observed_assignment", observedAt, "", "Saved assignment to "+action.Owner+" for this obligation.")
	applyReceipts := func() {
		for _, event := range n.ObligationReceipts {
			if event.ObligationID != action.Obligation.ID {
				continue
			}
			if !hasObligationEvent(events, event) {
				events = append(events, event)
			}
			switch event.Kind {
			case "assigned":
				terminal = ""
			case "completed":
				terminal = "completed"
			case "canceled":
				terminal = "canceled"
			case "superseded":
				terminal = "replaced"
			}
		}
	}
	response := n.ReviewResponse
	if response == nil {
		applyReceipts()
		slices.SortStableFunc(events, func(a, b api.OperationWorkflowObligationEvent) int { return a.At.Compare(b.At) })
		if len(events) > 40 {
			events = events[len(events)-40:]
		}
		return events, terminal
	}
	completion := func(c *api.OperationWorkflowReviewResponseEscalationCompletion) {
		if action.Kind == "scheduled_evidence_check" && c != nil && c.At.Equal(epoch) && c.ScheduledReviewAt != nil && c.Review != nil && c.Review.At.Equal(*c.ScheduledReviewAt) {
			add("completed", *c.ScheduledReviewAt, c.Review.Actor, "Scheduled worker evidence observation recorded; application success is not inferred.")
			terminal = "completed"
		}
		if action.Kind == "coordinator_completion" && c != nil && c.AcknowledgedAt.Equal(epoch) {
			add("completed", c.At, c.Actor, c.Summary)
			terminal = "completed"
		}
	}
	cancellation := func(c *api.OperationWorkflowReviewResponseScheduledReviewCancellation) {
		if action.Kind == "scheduled_evidence_check" && c != nil && c.CompletedAt.Equal(epoch) {
			add("canceled", c.At, c.Actor, c.Reason)
			terminal = "canceled"
		}
	}
	reschedule := func(c *api.OperationWorkflowReviewResponseScheduledReviewReschedule) {
		if action.Kind == "scheduled_evidence_check" && c != nil && c.CompletedAt.Equal(epoch) {
			add("rescheduled", c.At, c.Actor, c.Reason)
		}
	}
	reassignment := func(c *api.OperationWorkflowReviewResponseHandoffReassignment) {
		if action.Kind == "handoff_reassignment" && c != nil && c.Decline.At.Equal(epoch) {
			add("reassigned", c.At, c.Actor, c.Reason)
			terminal = "completed"
		}
	}
	for _, h := range response.History {
		completion(h.EscalationCompletion)
		if h.EscalationAcknowledgment != nil {
			completion(h.EscalationAcknowledgment.Completion)
		}
		cancellation(h.ScheduledReviewCancellation)
		reschedule(h.ScheduledReviewReschedule)
		reassignment(h.HandoffReassignment)
	}
	if review := response.DecisionReview; review != nil {
		reassignment(review.Reassignment)
		if action.Kind == "handoff_acceptance" && review.HandedOffAt != nil && review.HandedOffAt.Equal(epoch) && review.Decline != nil {
			d := review.Decline
			add("declined", d.At, d.Actor, d.Reason)
			terminal = "canceled"
		}
		if action.Kind == "handoff_acceptance" && review.HandedOffAt != nil && review.HandedOffAt.Equal(epoch) && review.Acceptance != nil {
			a := review.Acceptance
			add("accepted", a.At, a.Actor, a.Note)
			terminal = "completed"
		}
		if ack := review.EscalationAcknowledgment; ack != nil {
			completion(ack.Completion)
			if c := ack.Completion; c != nil {
				cancellation(c.ReviewCancellation)
				reschedule(c.ReviewReschedule)
			}
		}
	}
	applyReceipts()
	slices.SortStableFunc(events, func(a, b api.OperationWorkflowObligationEvent) int { return a.At.Compare(b.At) })
	if len(events) > 40 {
		events = events[len(events)-40:]
	}
	return events, terminal
}

func hasObligationEvent(events []api.OperationWorkflowObligationEvent, event api.OperationWorkflowObligationEvent) bool {
	fingerprint := readinessDigest(event)
	return slices.ContainsFunc(events, func(existing api.OperationWorkflowObligationEvent) bool {
		return readinessDigest(existing) == fingerprint
	})
}
