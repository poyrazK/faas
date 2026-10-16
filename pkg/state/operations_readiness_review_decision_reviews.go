package state

import (
	"fmt"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Called with the source notification and subscription locked, after observing health.
// Preserve the immutable decision and publish only one review requirement for it.
func applyReviewResponseDecisionReview(r *readinessSubscriptionRecord, n api.OperationWorkflowReadinessNotification) (api.OperationWorkflowReadinessNotification, []api.OperationWorkflowReadinessNotification, bool) {
	response := n.ReviewResponse
	if response == nil || response.Status != "active" || response.Decision == nil || response.Health == nil || response.DecisionReview != nil || response.Decision.Health.Fingerprint == "" || response.Health.Fingerprint == "" || response.Health.Fingerprint == response.Decision.Health.Fingerprint {
		return n, nil, true
	}
	review := api.OperationWorkflowReviewResponseDecisionReview{InitialOwner: response.Owner, FallbackOwner: response.DecisionReviewFallbackOwner, DecisionAt: response.Decision.At, RequiredAt: response.Health.CheckedAt, Changes: reviewResponseDecisionChanges(response.Decision.Health, *response.Health), Health: *response.Health}
	due := review.RequiredAt.Add(time.Duration(effectiveDecisionReviewWindow(response)) * time.Second)
	review.DueAt = &due
	updated := *response
	updated.DecisionReview = &review
	n.ReviewResponse = &updated
	notice := readinessSubscriptionNotify(r, "review_response_decision_review_required", "The condition reviewed for this response decision has changed. Review the latest plan health and record a new explicit decision; the existing intent and check-in remain in place.", nil)
	if n.BusinessImpact != nil {
		impact := *n.BusinessImpact
		impact.Response = nil
		notice.BusinessImpact = &impact
	}
	notice.DecisionReview = &api.OperationWorkflowReviewResponseDecisionReviewEvent{SourceNotificationID: n.ID, SourceSequence: n.Sequence, Decision: *response.Decision, Review: review}
	notice.Coordination = &api.OperationWorkflowReadinessCoordination{Revision: 1, Status: "unread", Owner: response.Owner, UpdatedAt: review.RequiredAt, UpdatedBy: "system:review-response-decision"}
	return n, []api.OperationWorkflowReadinessNotification{notice}, true
}

func reviewResponseDecisionChanges(previous, current api.OperationWorkflowReviewResponseHealth) []string {
	changes := []string{}
	if previous.Status != current.Status {
		changes = append(changes, fmt.Sprintf("Plan health changed from %s to %s", previous.Status, current.Status))
	}
	for _, blocker := range current.RemainingBlockers {
		if !slices.Contains(previous.RemainingBlockers, blocker) {
			changes = append(changes, "New or changed blocker: "+blocker)
		}
	}
	// Disappearance alone cannot claim recovery; health retains unverified prior blockers.
	for _, blocker := range previous.RemainingBlockers {
		if !slices.Contains(current.RemainingBlockers, blocker) {
			changes = append(changes, "Previously reviewed blocker no longer present: "+blocker)
		}
	}
	oldSources := map[int]api.OperationWorkflowReviewResponseSourceHealth{}
	for _, source := range previous.Sources {
		oldSources[source.ReviewIndex] = source
	}
	for _, source := range current.Sources {
		prefix := fmt.Sprintf("Linked review %d: ", source.ReviewIndex+1)
		old, found := oldSources[source.ReviewIndex]
		if !found {
			changes = append(changes, prefix+"source observation changed")
			continue
		}
		if old.Status != source.Status {
			changes = append(changes, prefix+"availability changed from "+old.Status+" to "+source.Status)
		}
		if old.ProgressRevision != source.ProgressRevision {
			message := "retained progress revision changed"
			if source.ProgressRevision == 0 {
				message = "retained progress revision is unavailable"
			} else if old.ProgressRevision == 0 {
				message = "retained progress revision became available"
			}
			changes = append(changes, prefix+message)
		}
		if old.StateRevision != source.StateRevision {
			message := "reported application state revision changed"
			if source.StateRevision == 0 {
				message = "reported application state revision is unavailable"
			} else if old.StateRevision == 0 {
				message = "reported application state revision became available"
			}
			changes = append(changes, prefix+message)
		}
		if old.ReviewRequired != source.ReviewRequired {
			message := "validity review obligation changed"
			if source.Status == "unavailable" {
				message = "validity review obligation requires inspection of the unavailable source"
			}
			changes = append(changes, prefix+message)
		}
		if old.Explanation != source.Explanation && source.Explanation != "" {
			changes = append(changes, prefix+source.Explanation)
		}
	}
	if len(changes) == 0 {
		changes = append(changes, "The sampled source condition differs from the reviewed decision snapshot")
	}
	return changes
}
