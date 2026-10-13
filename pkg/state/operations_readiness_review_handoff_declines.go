package state

import (
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
)

func validateReviewHandoffDecline(n api.OperationWorkflowReadinessNotification, input api.OperationWorkflowReviewResponseRequest) error {
	decline := input.HandoffDecline
	if decline == nil {
		return nil
	}
	if input.HandoffReassignment != nil || input.EscalationAcknowledgment != nil || input.EscalationCompletion != nil || input.EscalationActionReview != nil || input.EscalationRevision != nil || input.ScheduledReviewCancellation != nil || input.ScheduledReviewReschedule != nil || input.Decision != nil || input.HandoffAcceptance != nil || input.Status != "active" || strings.TrimSpace(decline.Reason) == "" || len(decline.Reason) > 1024 || !utf8.ValidString(decline.Reason) || strings.IndexFunc(decline.Reason, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' }) >= 0 {
		return ErrInvalidArgument
	}
	response := n.ReviewResponse
	if response == nil || response.Status != "active" || response.Decision == nil || response.DecisionReview == nil || n.Coordination == nil || n.Coordination.Completion != nil {
		return ErrConflict
	}
	review := response.DecisionReview
	if !matchesReviewHandoffTime(review, decline.HandedOffAt) || !review.Reassigned || review.DeadlineNotifiedAt == nil || review.Acceptance != nil || review.Decline != nil || review.PreviousOwner == "" || review.Owner == "" || input.Owner != review.Owner || response.Owner != review.Owner || n.Coordination.Owner != review.Owner || !review.DecisionAt.Equal(decline.DecisionAt) || !response.Decision.At.Equal(decline.DecisionAt) || !review.RequiredAt.Equal(decline.RequiredAt) || input.Plan != response.Plan || input.CheckInAt == nil || response.CheckInAt == nil || !input.CheckInAt.Equal(*response.CheckInAt) {
		return ErrConflict
	}
	return nil
}

type reviewHandoffDeclineUpdate struct {
	DecisionAt, RequiredAt, DeclinedAt time.Time
	Actor                              string
}

func pendingReviewHandoffDecline(n api.OperationWorkflowReadinessNotification) *api.OperationWorkflowReviewResponseHandoffDecline {
	if n.Kind != "business_impact_changed" || n.ReviewResponse == nil {
		return nil
	}
	for _, change := range n.ReviewResponse.History {
		if change.HandoffDecline != nil && change.HandoffDecline.NotifiedAt == nil {
			return change.HandoffDecline
		}
	}
	return nil
}
func reviewHandoffDeclineUpdates(items []api.OperationWorkflowReadinessNotification, r *readinessSubscriptionRecord) []readinessWatchUpdate {
	candidates := []api.OperationWorkflowReadinessNotification{}
	for _, n := range items {
		if pendingReviewHandoffDecline(n) != nil {
			candidates = append(candidates, n)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i].Sequence > r.HandoffDeclineCursor, candidates[j].Sequence > r.HandoffDeclineCursor
		if a != b {
			return a
		}
		return candidates[i].Sequence < candidates[j].Sequence
	})
	if len(candidates) > api.OperationWorkflowReadinessEscalationResponsesPerPassMax {
		candidates = candidates[:api.OperationWorkflowReadinessEscalationResponsesPerPassMax]
	}
	updates := []readinessWatchUpdate{}
	for _, n := range candidates {
		decline := pendingReviewHandoffDecline(n)
		updates = append(updates, readinessWatchUpdate{SourceID: n.ID, Sequence: n.Sequence, HandoffDecline: &reviewHandoffDeclineUpdate{DecisionAt: decline.DecisionAt, RequiredAt: decline.RequiredAt, DeclinedAt: decline.At, Actor: decline.Actor}})
		r.HandoffDeclineCursor = n.Sequence
	}
	return updates
}

// Retained history keeps notifications deliverable after a decision supersedes its episode.
func applyReviewHandoffDeclineNotice(r *readinessSubscriptionRecord, n api.OperationWorkflowReadinessNotification, u readinessWatchUpdate) (api.OperationWorkflowReadinessNotification, []api.OperationWorkflowReadinessNotification, bool) {
	if n.ID != u.SourceID || n.Kind != "business_impact_changed" || n.ReviewResponse == nil {
		return n, nil, false
	}
	matches := func(d *api.OperationWorkflowReviewResponseHandoffDecline) bool {
		return d != nil && d.NotifiedAt == nil && d.DecisionAt.Equal(u.HandoffDecline.DecisionAt) && d.RequiredAt.Equal(u.HandoffDecline.RequiredAt) && d.At.Equal(u.HandoffDecline.DeclinedAt) && d.Actor == u.HandoffDecline.Actor
	}
	response := *n.ReviewResponse
	response.History = slices.Clone(response.History)
	var published *api.OperationWorkflowReviewResponseHandoffDecline
	now := time.Now().UTC().Truncate(time.Microsecond)
	for i := range response.History {
		d := response.History[i].HandoffDecline
		if matches(d) {
			copy := *d
			copy.NotifiedAt = &now
			response.History[i].HandoffDecline = &copy
			published = &copy
			break
		}
	}
	if published == nil {
		return n, nil, false
	}
	if response.DecisionReview != nil && matches(response.DecisionReview.Decline) {
		review := *response.DecisionReview
		review.Decline = published
		response.DecisionReview = &review
	}
	n.ReviewResponse = &response
	notice := readinessSubscriptionNotify(r, "review_response_handoff_declined", "The fallback team declined this handoff. Review current ownership and arrange a manual reassignment; this notice records a historical decline and does not change deadlines or application execution.", nil)
	if n.BusinessImpact != nil {
		impact := *n.BusinessImpact
		impact.Response = nil
		notice.BusinessImpact = &impact
	}
	notice.HandoffDecline = &api.OperationWorkflowReviewResponseHandoffDeclineEvent{SourceNotificationID: n.ID, SourceSequence: n.Sequence, Decline: *published}
	notice.Coordination = &api.OperationWorkflowReadinessCoordination{Revision: 1, Status: "unread", Owner: published.PreviousOwner, UpdatedAt: now, UpdatedBy: "system:handoff-decline"}
	return n, []api.OperationWorkflowReadinessNotification{notice}, true
}
