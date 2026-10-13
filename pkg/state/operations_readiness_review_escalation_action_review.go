package state

import (
	"context"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

func escalationActionIdentity(input api.OperationWorkflowReviewResponseRequest) *api.OperationWorkflowReviewResponseEscalationActionReviewRequest {
	if input.EscalationRevision != nil {
		return &input.EscalationRevision.OperationWorkflowReviewResponseEscalationActionReviewRequest
	}
	return input.EscalationActionReview
}

func validateEscalationActionReview(n api.OperationWorkflowReadinessNotification, input api.OperationWorkflowReviewResponseRequest) error {
	identity := escalationActionIdentity(input)
	if identity == nil {
		return nil
	}
	if input.EscalationActionReview != nil && input.EscalationRevision != nil || input.EscalationCompletion != nil || input.EscalationAcknowledgment != nil || input.ScheduledReviewCancellation != nil || input.ScheduledReviewReschedule != nil || input.Decision != nil || input.HandoffAcceptance != nil || input.HandoffDecline != nil || input.HandoffReassignment != nil || input.Status != "active" || len(input.Reviews) != 0 {
		return ErrInvalidArgument
	}
	response := n.ReviewResponse
	if n.Kind != "business_impact_changed" || response == nil || response.Status != "active" || response.Decision == nil || response.DecisionReview == nil || n.Coordination == nil || n.Coordination.Completion != nil {
		return ErrConflict
	}
	review := response.DecisionReview
	ack := review.EscalationAcknowledgment
	handoff := reviewHandoffTime(review)
	if ack == nil || ack.Completion == nil || handoff == nil || review.AcceptanceEscalatedAt == nil || !response.Decision.At.Equal(identity.DecisionAt) || !review.DecisionAt.Equal(identity.DecisionAt) || !review.RequiredAt.Equal(identity.RequiredAt) || !handoff.Equal(identity.HandedOffAt) || !review.AcceptanceEscalatedAt.Equal(identity.EscalatedAt) || !ack.At.Equal(identity.AcknowledgedAt) || !ack.Completion.At.Equal(identity.CompletedAt) || len(ack.Completion.Evidence) == 0 || response.Owner != review.Owner || n.Coordination.Owner != response.Owner || input.Owner != response.Owner || input.Plan != response.Plan || input.CheckInAt == nil || response.CheckInAt == nil || !input.CheckInAt.Equal(*response.CheckInAt) {
		return ErrConflict
	}
	if revision := input.EscalationRevision; revision != nil {
		now := time.Now().UTC()
		if revision.Coordinator == "" || strings.TrimSpace(revision.Coordinator) != revision.Coordinator || len(revision.Coordinator) > 256 || !utf8.ValidString(revision.Coordinator) || strings.IndexFunc(revision.Coordinator, unicode.IsControl) >= 0 || strings.TrimSpace(revision.Plan) == "" || len(revision.Plan) > 1024 || !utf8.ValidString(revision.Plan) || strings.IndexFunc(revision.Plan, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' }) >= 0 || !revision.DueAt.After(now) || revision.DueAt.After(now.Add(7*24*time.Hour)) {
			return ErrInvalidArgument
		}
		if !currentReviewEscalation(n) || ack.Completion.Review == nil || !ack.Completion.Review.At.Equal(revision.ReviewAt) || ack.Completion.Review.Health.Status == "clear" {
			return ErrConflict
		}
	}
	return nil
}

// Reuse scoped playbook observation for only the immutable completion evidence.
// The notification lock fences the episode, not concurrent application-state reporting.
func prepareEscalationActionReview(ctx context.Context, store readinessSubscriptionBackend, account, tenant, app, scope, id string, sequence int64, req api.OperationWorkflowReadinessCoordinationRequest, operator bool) (*api.OperationWorkflowReviewResponseEscalationActionReview, error) {
	if req.ReviewResponse == nil || escalationActionIdentity(*req.ReviewResponse) == nil {
		return nil, nil
	}
	page, err := store.readReadinessInbox(ctx, account, tenant, app, scope, uuid.MustParse(id).String(), sequence-1, 1, operator)
	if err != nil {
		return nil, err
	}
	if len(page.Items) != 1 || page.Items[0].Sequence != sequence || page.Items[0].Coordination == nil || page.Items[0].Coordination.Revision != req.Revision {
		return nil, ErrConflict
	}
	n := page.Items[0]
	if err = validateEscalationActionReview(n, *req.ReviewResponse); err != nil {
		return nil, err
	}
	completion := n.ReviewResponse.DecisionReview.EscalationAcknowledgment.Completion
	response := *n.ReviewResponse
	response.Reviews = slices.Clone(completion.Evidence)
	response.Health = nil
	n.ReviewResponse = &response
	record := readinessSubscriptionRecord{Account: account, Tenant: tenant, Operator: operator, Subscription: page.Subscription}
	updates, _ := evaluateReviewResponseBlockages(ctx, store, &record, []api.OperationWorkflowReadinessNotification{n})
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	var health api.OperationWorkflowReviewResponseHealth
	if len(updates) == 1 && updates[0].ReviewBlockage != nil {
		observation := updates[0].ReviewBlockage
		checked := withReviewResponseHealth(n, observation, observation.Blockers, nil)
		health = *checked.ReviewResponse.Health
	} else {
		at := time.Now().UTC().Truncate(time.Microsecond)
		health = api.OperationWorkflowReviewResponseHealth{Status: "unavailable", CheckedAt: at, RemainingBlockers: []string{}, RecoveredBlockers: []string{}, Sources: []api.OperationWorkflowReviewResponseSourceHealth{}}
		for i := range completion.Evidence {
			health.Sources = append(health.Sources, api.OperationWorkflowReviewResponseSourceHealth{ReviewIndex: i, Status: "unavailable", CheckedAt: at, Blockers: []string{}, Explanation: "Scoped review evidence could not be read."})
		}
		health.Fingerprint = reviewResponseHealthFingerprint(health)
	}
	if req.ReviewResponse.EscalationRevision != nil && health.Status == "clear" {
		return nil, ErrConflict
	}
	return &api.OperationWorkflowReviewResponseEscalationActionReview{CompletedAt: completion.At, Health: health}, nil
}
