package state

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
)

// Evidence links identify saved review references; they do not claim fresh application verification.
func validateReviewEscalationCompletion(n api.OperationWorkflowReadinessNotification, input api.OperationWorkflowReviewResponseRequest) error {
	completion := input.EscalationCompletion
	if completion == nil {
		return nil
	}
	if completion.ReviewAfterSeconds != nil && (*completion.ReviewAfterSeconds < 60 || *completion.ReviewAfterSeconds > 604800) {
		return ErrInvalidArgument
	}
	if input.EscalationAcknowledgment != nil || input.EscalationActionReview != nil || input.EscalationRevision != nil || input.ScheduledReviewCancellation != nil || input.ScheduledReviewReschedule != nil || input.Decision != nil || input.HandoffAcceptance != nil || input.HandoffDecline != nil || input.HandoffReassignment != nil || input.Status != "active" || len(input.Reviews) != 0 || strings.TrimSpace(completion.Summary) == "" || len(completion.Summary) > 1024 || !utf8.ValidString(completion.Summary) || strings.IndexFunc(completion.Summary, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' }) >= 0 || len(completion.ReviewIndices) < 1 || len(completion.ReviewIndices) > 8 {
		return ErrInvalidArgument
	}
	response := n.ReviewResponse
	if n.Kind != "business_impact_changed" || response == nil || response.Status != "active" || response.Decision == nil || response.DecisionReview == nil || n.Coordination == nil || n.Coordination.Completion != nil {
		return ErrConflict
	}
	review := response.DecisionReview
	ack := review.EscalationAcknowledgment
	handoff := reviewHandoffTime(review)
	if ack == nil || ack.Completion != nil || handoff == nil || review.AcceptanceEscalatedAt == nil || !response.Decision.At.Equal(completion.DecisionAt) || !review.DecisionAt.Equal(completion.DecisionAt) || !review.RequiredAt.Equal(completion.RequiredAt) || !handoff.Equal(completion.HandedOffAt) || !review.AcceptanceEscalatedAt.Equal(completion.EscalatedAt) || !ack.At.Equal(completion.AcknowledgedAt) || response.Owner != review.Owner || n.Coordination.Owner != response.Owner || input.Owner != response.Owner || input.Plan != response.Plan || input.CheckInAt == nil || response.CheckInAt == nil || !input.CheckInAt.Equal(*response.CheckInAt) {
		return ErrConflict
	}
	seen := make(map[int]bool, len(completion.ReviewIndices))
	for _, index := range completion.ReviewIndices {
		if index < 0 || index >= len(response.Reviews) || seen[index] {
			return ErrInvalidArgument
		}
		seen[index] = true
	}
	return nil
}
