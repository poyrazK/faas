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

type reviewHandoffReassignmentUpdate struct {
	DecisionAt, RequiredAt, ReassignedAt time.Time
	Actor                                string
}

func pendingReviewHandoffReassignment(n api.OperationWorkflowReadinessNotification) *api.OperationWorkflowReviewResponseHandoffReassignment {
	if n.Kind != "business_impact_changed" || n.ReviewResponse == nil {
		return nil
	}
	for _, change := range n.ReviewResponse.History {
		if change.HandoffReassignment != nil && change.HandoffReassignment.NotifiedAt == nil {
			return change.HandoffReassignment
		}
	}
	return nil
}
func reviewHandoffReassignmentUpdates(items []api.OperationWorkflowReadinessNotification, r *readinessSubscriptionRecord) []readinessWatchUpdate {
	candidates := []api.OperationWorkflowReadinessNotification{}
	for _, n := range items {
		if pendingReviewHandoffReassignment(n) != nil {
			candidates = append(candidates, n)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i].Sequence > r.HandoffReassignmentCursor, candidates[j].Sequence > r.HandoffReassignmentCursor
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
		decline := pendingReviewHandoffReassignment(n)
		updates = append(updates, readinessWatchUpdate{SourceID: n.ID, Sequence: n.Sequence, HandoffReassignment: &reviewHandoffReassignmentUpdate{DecisionAt: decline.DecisionAt, RequiredAt: decline.RequiredAt, ReassignedAt: decline.At, Actor: decline.Actor}})
		r.HandoffReassignmentCursor = n.Sequence
	}
	return updates
}

// Retained history keeps notifications deliverable after a decision supersedes its episode.
func applyReviewHandoffReassignmentNotice(r *readinessSubscriptionRecord, n api.OperationWorkflowReadinessNotification, u readinessWatchUpdate) (api.OperationWorkflowReadinessNotification, []api.OperationWorkflowReadinessNotification, bool) {
	if n.ID != u.SourceID || n.Kind != "business_impact_changed" || n.ReviewResponse == nil {
		return n, nil, false
	}
	matches := func(d *api.OperationWorkflowReviewResponseHandoffReassignment) bool {
		return d != nil && d.NotifiedAt == nil && d.DecisionAt.Equal(u.HandoffReassignment.DecisionAt) && d.RequiredAt.Equal(u.HandoffReassignment.RequiredAt) && d.At.Equal(u.HandoffReassignment.ReassignedAt) && d.Actor == u.HandoffReassignment.Actor
	}
	response := *n.ReviewResponse
	response.History = slices.Clone(response.History)
	var published *api.OperationWorkflowReviewResponseHandoffReassignment
	now := time.Now().UTC().Truncate(time.Microsecond)
	for i := range response.History {
		d := response.History[i].HandoffReassignment
		if matches(d) {
			copy := *d
			copy.NotifiedAt = &now
			response.History[i].HandoffReassignment = &copy
			published = &copy
			break
		}
	}
	if published == nil {
		return n, nil, false
	}
	if response.DecisionReview != nil && matches(response.DecisionReview.Reassignment) {
		review := *response.DecisionReview
		review.Reassignment = published
		response.DecisionReview = &review
	}
	n.ReviewResponse = &response
	notice := readinessSubscriptionNotify(r, "review_response_handoff_reassigned", "A review was manually handed to your team. Inspect current ownership and explicitly accept the current handoff with a note and future check-in; the original review deadline is unchanged.", nil)
	if n.BusinessImpact != nil {
		impact := *n.BusinessImpact
		impact.Response = nil
		notice.BusinessImpact = &impact
	}
	notice.HandoffReassignment = &api.OperationWorkflowReviewResponseHandoffReassignmentEvent{SourceNotificationID: n.ID, SourceSequence: n.Sequence, Reassignment: *published}
	notice.Coordination = &api.OperationWorkflowReadinessCoordination{Revision: 1, Status: "unread", Owner: published.Owner, UpdatedAt: now, UpdatedBy: "system:handoff-reassignment"}
	return n, []api.OperationWorkflowReadinessNotification{notice}, true
}

func validateReviewHandoffReassignment(n api.OperationWorkflowReadinessNotification, input api.OperationWorkflowReviewResponseRequest) error {
	request := input.HandoffReassignment
	if request == nil {
		return nil
	}
	if input.EscalationAcknowledgment != nil || input.EscalationCompletion != nil || input.EscalationActionReview != nil || input.EscalationRevision != nil || input.ScheduledReviewCancellation != nil || input.ScheduledReviewReschedule != nil || input.Decision != nil || input.HandoffAcceptance != nil || input.HandoffDecline != nil || input.Status != "active" || strings.TrimSpace(request.Reason) == "" || len(request.Reason) > 1024 || !utf8.ValidString(request.Reason) || strings.IndexFunc(request.Reason, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' }) >= 0 {
		return ErrInvalidArgument
	}
	response := n.ReviewResponse
	if response == nil || response.Status != "active" || response.Decision == nil || response.DecisionReview == nil || n.Coordination == nil || n.Coordination.Completion != nil {
		return ErrConflict
	}
	review := response.DecisionReview
	if review.Decline == nil || !review.ReassignmentRequired || response.Owner != review.Decline.Owner || n.Coordination.Owner != response.Owner || input.Owner == response.Owner || !review.Decline.At.Equal(request.DeclinedAt) || !review.DecisionAt.Equal(request.DecisionAt) || !response.Decision.At.Equal(request.DecisionAt) || !review.RequiredAt.Equal(request.RequiredAt) || input.Plan != response.Plan || input.CheckInAt == nil || response.CheckInAt == nil || !input.CheckInAt.Equal(*response.CheckInAt) {
		return ErrConflict
	}
	return nil
}
