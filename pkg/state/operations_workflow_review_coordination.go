package state

import (
	"context"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
)

func fulfillmentReviewFingerprint(r api.OperationWorkflowObligationFulfillmentReview) string {
	fields := []any{r.Binding, r.FulfilledAt, r.BasisAt, r.Owner, r.Reasons, r.CurrentReasons, r.CurrentContractVersion}
	if r.ManualOwner || r.DueAt != nil {
		fields = append(fields, r.ManualOwner, r.DueAt)
	}
	if r.EscalationOwner != "" || r.EscalateAfterSeconds != 0 {
		fields = append(fields, r.EscalationOwner, r.EscalateAfterSeconds)
	}
	return readinessDigest(fields)
}
func pendingReviewByID(n api.OperationWorkflowReadinessNotification, id, reviewID string) (api.OperationWorkflowObligationFulfillmentReview, int, error) {
	if n.Coordination != nil {
		for i, r := range n.Coordination.FulfillmentReviews {
			obligation := fulfillmentReviewActionObligation(id, n.Sequence, r)
			if r.ReviewRequired && obligation != nil && obligation.ID == reviewID && activeFulfillment(n.Coordination, r.Binding, r.FulfilledAt) != nil {
				return r, i, nil
			}
		}
	}
	return api.OperationWorkflowObligationFulfillmentReview{}, 0, ErrConflict
}
func prepareFulfillmentReviewCoordination(ctx context.Context, store readinessSubscriptionBackend, account, tenant, app, scope, id string, sequence int64, req api.OperationWorkflowReadinessCoordinationRequest, operator bool) (*api.OperationWorkflowFulfillmentReviewCoordinationChange, error) {
	input := req.ReviewCoordination
	if input == nil {
		return nil, nil
	}
	if req.Reaffirmation != nil || req.Reopening != nil || req.Fulfillment != nil || req.TargetBinding != nil || req.ReviewResponse != nil || req.Completion != nil || input.ReviewID == "" || len(input.ReviewID) > 128 || input.ReviewFingerprint == "" || len(input.ReviewFingerprint) > 128 || len(input.Owner) > 256 || strings.TrimSpace(input.Owner) != input.Owner || !utf8.ValidString(input.Owner) || strings.IndexFunc(input.Owner, unicode.IsControl) >= 0 || input.Reason == "" || len(input.Reason) > 2048 || strings.TrimSpace(input.Reason) != input.Reason || !utf8.ValidString(input.Reason) || strings.IndexFunc(input.Reason, unicode.IsControl) >= 0 {
		return nil, ErrInvalidArgument
	}
	if input.DueAt != nil {
		at := input.DueAt.UTC().Truncate(time.Microsecond)
		if at.Year() < 1 || at.Year() > 9999 {
			return nil, ErrInvalidArgument
		}
		input.DueAt = &at
	}
	page, err := store.readReadinessInbox(ctx, account, tenant, app, scope, id, sequence-1, 1, operator)
	if err != nil {
		return nil, err
	}
	if len(page.Items) != 1 || page.Items[0].Sequence != sequence {
		return nil, ErrNotFound
	}
	n := page.Items[0]
	if n.Coordination == nil || n.Coordination.Revision != req.Revision || len(n.Coordination.ReviewCoordinationChanges) >= 128 {
		return nil, ErrConflict
	}
	if req.Status != n.Coordination.Status || req.Owner != n.Coordination.Owner || req.Note != n.Coordination.Note || readinessDigest(req.FollowUpAt) != readinessDigest(n.Coordination.FollowUpAt) {
		return nil, ErrInvalidArgument
	}
	r, _, err := pendingReviewByID(n, id, input.ReviewID)
	if err != nil {
		return nil, err
	}
	if r.Fingerprint != input.ReviewFingerprint {
		return nil, ErrConflict
	}

	escalationOwner, grace := r.EscalationOwner, r.EscalateAfterSeconds
	if input.Owner != r.Owner && input.EscalationOwner == nil && input.EscalateAfterSeconds == nil {
		escalationOwner, grace = "", 0
	}
	if input.DueAt == nil && input.EscalationOwner == nil && input.EscalateAfterSeconds == nil {
		escalationOwner, grace = "", 0
	}
	if input.EscalationOwner != nil {
		escalationOwner = *input.EscalationOwner
		if escalationOwner == "" && input.EscalateAfterSeconds == nil {
			grace = 0
		}
	}
	if input.EscalateAfterSeconds != nil {
		grace = *input.EscalateAfterSeconds
		if grace == 0 && input.EscalationOwner == nil {
			escalationOwner = ""
		}
	}
	if len(escalationOwner) > 256 || strings.TrimSpace(escalationOwner) != escalationOwner || !utf8.ValidString(escalationOwner) || strings.IndexFunc(escalationOwner, unicode.IsControl) >= 0 || grace < 0 || grace > 604800 || (escalationOwner == "") != (grace == 0) || escalationOwner != "" && (escalationOwner == input.Owner || input.DueAt == nil) {
		return nil, ErrInvalidArgument
	}
	return &api.OperationWorkflowFulfillmentReviewCoordinationChange{FromEscalationOwner: r.EscalationOwner, EscalationOwner: escalationOwner, PreviousEscalateAfterSeconds: r.EscalateAfterSeconds, EscalateAfterSeconds: grace, ReviewID: input.ReviewID, Binding: r.Binding, FulfilledAt: r.FulfilledAt, FromOwner: r.Owner, Owner: input.Owner, PreviousDueAt: r.DueAt, DueAt: input.DueAt, Reason: input.Reason}, nil
}
func applyFulfillmentReviewCoordination(n api.OperationWorkflowReadinessNotification, req readinessCoordinationUpdate, actor string, now time.Time) (api.OperationWorkflowReadinessNotification, error) {
	if req.ReviewCoordination == nil {
		return n, nil
	}
	prepared := req.PreparedReviewCoordination
	if prepared == nil || n.Coordination == nil || len(n.Coordination.ReviewCoordinationChanges) >= 128 {
		return n, ErrConflict
	}
	r, index, err := pendingReviewByID(n, req.TargetSubscriptionID, req.ReviewCoordination.ReviewID)
	if err != nil {
		return n, err
	}
	if r.Fingerprint != req.ReviewCoordination.ReviewFingerprint || readinessDigest(r.Binding) != readinessDigest(prepared.Binding) || !r.FulfilledAt.Equal(prepared.FulfilledAt) {
		return n, ErrConflict
	}
	r.Owner, r.DueAt, r.ManualOwner = prepared.Owner, prepared.DueAt, true
	r.EscalationOwner, r.EscalateAfterSeconds = prepared.EscalationOwner, prepared.EscalateAfterSeconds
	r.Fingerprint = fulfillmentReviewFingerprint(r)
	change := *prepared
	change.At, change.Actor = now, actor
	coord := *n.Coordination
	coord.FulfillmentReviews = slices.Clone(coord.FulfillmentReviews)
	coord.FulfillmentReviews[index] = r
	coord.ReviewCoordinationChanges = append(slices.Clone(coord.ReviewCoordinationChanges), change)
	n.Coordination = &coord
	return n, nil
}
func reviewCoordinationEvent(c api.OperationWorkflowFulfillmentReviewCoordinationChange) api.OperationWorkflowObligationEvent {
	value := c
	return api.OperationWorkflowObligationEvent{ReviewCoordination: &value, ObligationID: c.ReviewID, ActionKind: "fulfillment_review", Cases: []api.OperationWorkflowNextActionCase{c.Binding.Case}, Kind: "fulfillment_review_coordinated", At: c.At, Actor: c.Actor, Explanation: "Fulfillment review assignment, deadline or escalation policy explicitly updated: " + c.Reason}
}
