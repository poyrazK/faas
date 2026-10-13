package state

import (
	"context"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Capture only transitions in the locked source, never reconstruct an audit from old snapshots.
func captureObligationReceipts(ctx context.Context, id, scope string, before, after api.OperationWorkflowReadinessNotification, actor string) api.OperationWorkflowReadinessNotification {
	if after.NextActionDigest != nil || after.TargetAssessment != nil || after.ObligationReopening != nil || after.FulfillmentReview != nil {
		return after
	}
	// Complete the locked-source audit even if the caller was canceled.
	ctx = context.WithoutCancel(ctx)
	now := time.Now().UTC().Truncate(time.Microsecond)
	if actor == "" {
		actor = "system:obligation-receipts"
	}
	cases := map[string][]api.OperationWorkflowNextActionCase{}
	kinds := map[string]string{}
	actions := func(n api.OperationWorkflowReadinessNotification) map[string]api.OperationWorkflowCaseNextAction {
		result := map[string]api.OperationWorkflowCaseNextAction{}
		if n.Coordination == nil {
			return result
		}
		row := readinessFollowUpProjection(api.OperationWorkflowReadinessSubscription{ID: id, Scope: scope}, "", n)
		page, err := nextActionWorkQueue(ctx, []api.OperationWorkflowReadinessFollowUp{row}, "", api.OperationWorkflowReadinessFollowUpOptions{Scope: scope}, true, readinessQueueOffsetMax("next_actions"), api.OperationWorkflowReadinessFollowUpsResponse{EvaluatedAt: now})
		if err != nil {
			return result
		}
		for _, item := range page.NextActions {
			if item.Action.Obligation != nil {
				id := item.Action.Obligation.ID
				result[id] = item.Action
				kinds[id] = item.Action.Kind
				for _, c := range item.Cases {
					if !slices.Contains(cases[id], c) {
						cases[id] = append(cases[id], c)
					}
				}
			}
		}
		return result
	}
	old, current := actions(before), actions(after)
	reopenings := newObligationReopenings(before.Coordination, after.Coordination)
	receipts := slices.Clone(before.ObligationReceipts)
	add := func(id, kind, explanation string) {
		receipts = append(receipts, api.OperationWorkflowObligationEvent{Cases: slices.Clone(cases[id]), ActionKind: kinds[id], SharedPlan: len(cases[id]) > 1, ObligationID: id, Kind: kind, At: now, Actor: actor, Explanation: explanation})
	}
	// Stable ordering makes same-transaction publication deterministic.
	keys := []string{}
	for id := range old {
		keys = append(keys, id)
	}
	slices.Sort(keys)
	for _, id := range keys {
		previous := old[id]
		if next, exists := current[id]; exists {
			if previous.Owner != next.Owner {
				add(id, "reassigned", "Owner changed from "+previous.Owner+" to "+next.Owner+".")
			}
			if readinessDigest(previous.DueAt) != readinessDigest(next.DueAt) {
				add(id, "rescheduled", "The deadline for this obligation changed.")
			}
			if fulfillmentActionFingerprint(previous) != fulfillmentActionFingerprint(next) || previous.Summary != next.Summary || readinessDigest(previous.Evidence) != readinessDigest(next.Evidence) {
				add(id, "changed", "The obligation plan or linked evidence changed.")
			}
			continue
		}
		if previous.Kind == "fulfillment_review" {
			terminal := fulfillmentReviewActionTerminal(after, previous)
			if terminal == "" {
				terminal = "superseded"
			}
			add(id, terminal, "The pending fulfillment review was "+terminal+"; original fulfillment remains separately recorded.")
			continue
		}
		fulfilled := false
		if after.Coordination != nil {
			for _, receipt := range after.Coordination.Fulfillments {
				if receipt.Binding.ObligationID == id && (before.Coordination == nil || !slices.ContainsFunc(before.Coordination.Fulfillments, func(previous api.OperationWorkflowObligationFulfillment) bool {
					return readinessDigest(previous) == readinessDigest(receipt)
				})) {
					fulfilled = true
				}
			}
		}
		if fulfilled {
			continue
		}
		canonical := after
		canonical.ObligationReceipts = nil
		events, terminal := workflowObligationReceipts(canonical, previous, now)
		if terminal != "" {
			for _, event := range events {
				if event.Kind == "observed_assignment" {
					continue
				}
				event.ObligationID = id
				event.Cases = slices.Clone(cases[id])
				event.ActionKind = kinds[id]
				event.SharedPlan = len(cases[id]) > 1
				if !hasObligationEvent(receipts, event) {
					receipts = append(receipts, event)
				}
			}
			switch terminal {
			case "completed":
				add(id, "completed", "This change fulfilled the coordination obligation.")
			case "canceled":
				add(id, "canceled", "This obligation was canceled or declined.")
			default:
				add(id, "superseded", "This obligation was superseded.")
			}
		} else if before.ReviewResponse != nil && before.ReviewResponse.DecisionReview != nil && previous.Kind == "decision_revalidation" && after.ReviewResponse != nil && after.ReviewResponse.Decision != nil && after.ReviewResponse.Decision.At.After(before.ReviewResponse.DecisionReview.RequiredAt) && (before.ReviewResponse.Decision == nil || !after.ReviewResponse.Decision.At.Equal(before.ReviewResponse.Decision.At)) {
			add(id, "completed", "A new explicit response decision satisfied this latched review requirement.")
		} else if after.Coordination != nil && after.Coordination.Completion != nil {
			if previous.Kind == "follow_up" && after.Coordination.Completion.Outcome == "resolved" {
				add(id, "completed", "The generic source follow-up was explicitly resolved.")
			} else {
				add(id, "canceled", "The source follow-up was closed; fulfillment of this obligation is not asserted.")
			}
		} else {
			add(id, "superseded", "This obligation stopped projecting during the source mutation; fulfillment is not asserted.")
		}
	}
	keys = keys[:0]
	for id := range current {
		keys = append(keys, id)
	}
	slices.Sort(keys)
	for _, id := range keys {
		if _, exists := old[id]; !exists && !slices.ContainsFunc(reopenings, func(reopening api.OperationWorkflowObligationReopening) bool {
			return reopening.Binding.ObligationID == id
		}) {
			add(id, "assigned", "This obligation became active for "+current[id].Owner+".")
		}
	}
	if after.Coordination != nil {
		for _, receipt := range after.Coordination.Fulfillments {
			event := fulfillmentEvent(receipt)
			if before.Coordination != nil && slices.ContainsFunc(before.Coordination.Fulfillments, func(previous api.OperationWorkflowObligationFulfillment) bool {
				return readinessDigest(previous) == readinessDigest(receipt)
			}) {
				continue
			}
			event.SharedPlan = len(cases[receipt.Binding.ObligationID]) > 1
			if !hasObligationEvent(receipts, event) {
				receipts = append(receipts, event)
			}
		}
	}
	for _, reopening := range reopenings {
		event := reopeningEvent(reopening)
		event.SharedPlan = len(cases[reopening.Binding.ObligationID]) > 1
		receipts = append(receipts, event)
	}
	if after.Coordination != nil {
		for _, review := range after.Coordination.FulfillmentReviews {
			previous := fulfillmentReviewFor(before.Coordination, review.Binding, review.FulfilledAt)
			// Explicit coordination has its own receipt, rather than a new evidence flag.
			coordinated := slices.ContainsFunc(after.Coordination.ReviewCoordinationChanges, func(change api.OperationWorkflowFulfillmentReviewCoordinationChange) bool {
				return change.FulfilledAt.Equal(review.FulfilledAt) && readinessDigest(change.Binding) == readinessDigest(review.Binding) && (before.Coordination == nil || !slices.ContainsFunc(before.Coordination.ReviewCoordinationChanges, func(previous api.OperationWorkflowFulfillmentReviewCoordinationChange) bool {
					return readinessDigest(previous) == readinessDigest(change)
				}))
			})
			if !coordinated && review.ReviewRequired && (previous == nil || previous.Fingerprint != review.Fingerprint) {
				receipts = append(receipts, fulfillmentReviewEvent(review, "fulfillment_review_required", review.CheckedAt, actor))
			}
		}
		for _, r := range after.Coordination.Reaffirmations {
			if before.Coordination != nil && slices.ContainsFunc(before.Coordination.Reaffirmations, func(previous api.OperationWorkflowObligationReaffirmation) bool {
				return readinessDigest(previous) == readinessDigest(r)
			}) {
				continue
			}
			receipts = append(receipts, reaffirmationEvent(r))
		}
	}
	if after.Coordination != nil {
		for _, change := range after.Coordination.ReviewCoordinationChanges {
			if before.Coordination != nil && slices.ContainsFunc(before.Coordination.ReviewCoordinationChanges, func(previous api.OperationWorkflowFulfillmentReviewCoordinationChange) bool {
				return readinessDigest(previous) == readinessDigest(change)
			}) {
				continue
			}
			receipts = append(receipts, reviewCoordinationEvent(change))
		}
	}

	if after.Coordination != nil {
		for _, reminder := range after.Coordination.FulfillmentReviewReminders {
			if before.Coordination != nil && slices.ContainsFunc(before.Coordination.FulfillmentReviewReminders, func(previous api.OperationWorkflowFulfillmentReviewReminder) bool {
				return previous.NotificationID == reminder.NotificationID
			}) {
				continue
			}
			receipts = append(receipts, fulfillmentReviewReminderEvent(reminder))
		}
	}

	if after.Coordination != nil {
		for _, escalation := range after.Coordination.FulfillmentReviewEscalations {
			if before.Coordination != nil && slices.ContainsFunc(before.Coordination.FulfillmentReviewEscalations, func(previous api.OperationWorkflowFulfillmentReviewEscalation) bool {
				return previous.NotificationID == escalation.NotificationID
			}) {
				continue
			}
			receipts = append(receipts, fulfillmentReviewEscalationEvent(escalation))
		}
	}
	receipts = captureObligationTargetReceipts(before, after, old, current, cases, actor, now, receipts)
	receipts = linkObligationSuccessors(before, after, old, current, cases, actor, now, receipts)
	after.ObligationReceiptsTruncated = before.ObligationReceiptsTruncated
	if len(receipts) > 128 {
		receipts = receipts[len(receipts)-128:]
		after.ObligationReceiptsTruncated = true
	}
	after.ObligationReceipts = receipts
	return after
}
