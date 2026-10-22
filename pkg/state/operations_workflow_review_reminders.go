package state

import (
	"context"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type fulfillmentReviewReminderUpdate struct {
	ReviewID    string
	Fingerprint string
	DueAt       time.Time
}

func fulfillmentReviewDeadlineDelivered(coord *api.OperationWorkflowReadinessCoordination, id string, due time.Time) bool {
	return slices.ContainsFunc(coord.FulfillmentReviewReminders, func(r api.OperationWorkflowFulfillmentReviewReminder) bool {
		return r.ReviewID == id && r.DueAt.Equal(due)
	})
}

// Rotate due reviews independently of evidence observation failures and discovery filters.
func evaluateFulfillmentReviewReminders(ctx context.Context, store readinessSubscriptionBackend, r *readinessSubscriptionRecord) ([]readinessWatchUpdate, error) {
	page, err := store.readReadinessInbox(ctx, r.Account, r.Tenant, r.Subscription.AppID, r.Subscription.Scope, r.Subscription.ID, 0, api.OperationWorkflowReadinessInboxMax, r.Operator)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	type candidate struct {
		key    string
		update readinessWatchUpdate
	}
	candidates := []candidate{}
	for _, n := range page.Items {
		if n.Coordination == nil || len(n.Coordination.FulfillmentReviewReminders) >= 128 {
			continue
		}
		for _, review := range n.Coordination.FulfillmentReviews {
			obligation := fulfillmentReviewActionObligation(r.Subscription.ID, n.Sequence, review)
			if !review.ReviewRequired || obligation == nil || review.DueAt == nil || review.DueAt.After(now) || activeFulfillment(n.Coordination, review.Binding, review.FulfilledAt) == nil || fulfillmentReviewDeadlineDelivered(n.Coordination, obligation.ID, *review.DueAt) {
				continue
			}
			candidates = append(candidates, candidate{n.ID + "/" + obligation.ID, readinessWatchUpdate{SourceID: n.ID, Sequence: n.Sequence, Revision: n.Coordination.Revision, FulfillmentReviewReminder: &fulfillmentReviewReminderUpdate{ReviewID: obligation.ID, Fingerprint: review.Fingerprint, DueAt: *review.DueAt}}})
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i].key > r.FulfillmentReviewReminderCursor, candidates[j].key > r.FulfillmentReviewReminderCursor
		if a != b {
			return a
		}
		return candidates[i].key < candidates[j].key
	})
	if len(candidates) > 5 {
		candidates = candidates[:5]
	}
	updates := []readinessWatchUpdate{}
	for _, c := range candidates {
		r.FulfillmentReviewReminderCursor = c.key
		updates = append(updates, c.update)
	}
	return updates, nil
}

// The source and subscription are locked; receipt and notice are committed together.
func applyFulfillmentReviewReminder(record *readinessSubscriptionRecord, n api.OperationWorkflowReadinessNotification, u readinessWatchUpdate) (api.OperationWorkflowReadinessNotification, []api.OperationWorkflowReadinessNotification, bool) {
	input := u.FulfillmentReviewReminder
	if input == nil || n.ID != u.SourceID || n.Coordination == nil || n.Coordination.Revision != u.Revision || len(n.Coordination.FulfillmentReviewReminders) >= 128 {
		return n, nil, false
	}
	review, _, err := pendingReviewByID(n, record.Subscription.ID, input.ReviewID)
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err != nil || review.Fingerprint != input.Fingerprint || review.DueAt == nil || !review.DueAt.Equal(input.DueAt) || review.DueAt.After(now) || fulfillmentReviewDeadlineDelivered(n.Coordination, input.ReviewID, input.DueAt) {
		return n, nil, false
	}
	notice := readinessSubscriptionNotify(record, "fulfillment_review_overdue", "Fulfillment review deadline missed. Review the retained evidence issues ("+strings.Join(review.Reasons, ", ")+") and explicitly reaffirm fulfillment or reopen the case. Original fulfillment remains recorded.", nil)
	notice.ID = uuid.NewSHA1(uuid.NameSpaceOID, []byte(n.ID+"\x1f"+input.ReviewID+"\x1f"+input.DueAt.UTC().Format(time.RFC3339Nano))).String()
	receipt := api.OperationWorkflowFulfillmentReviewReminder{ReviewID: input.ReviewID, NotificationID: notice.ID, DueAt: input.DueAt, NotifiedAt: notice.CreatedAt, Review: review}
	notice.FulfillmentReview = &review
	notice.FulfillmentReviewReminder = &receipt
	notice.Coordination = &api.OperationWorkflowReadinessCoordination{Status: "unread", Owner: review.Owner, Revision: 1, UpdatedAt: notice.CreatedAt, UpdatedBy: "system:fulfillment-review-reminder"}
	coord := *n.Coordination
	coord.FulfillmentReviewReminders = append(slices.Clone(coord.FulfillmentReviewReminders), receipt)
	// Delivery does not change semantic review fingerprints or invalidate other prepared review checks.
	n.Coordination = &coord
	return n, []api.OperationWorkflowReadinessNotification{notice}, true
}

func fulfillmentReviewReminderEvent(reminder api.OperationWorkflowFulfillmentReviewReminder) api.OperationWorkflowObligationEvent {
	value := reminder
	return api.OperationWorkflowObligationEvent{FulfillmentReviewReminder: &value, ObligationID: reminder.ReviewID, ActionKind: "fulfillment_review", Cases: []api.OperationWorkflowNextActionCase{reminder.Review.Binding.Case}, Kind: "fulfillment_review_overdue", At: reminder.NotifiedAt, Actor: "system:fulfillment-review-reminder", Explanation: "Overdue review reminder delivered to " + reminder.Review.Owner + " for deadline " + reminder.DueAt.UTC().Format(time.RFC3339Nano) + "."}
}
