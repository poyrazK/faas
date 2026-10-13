package state

import (
	"context"
	"github.com/onebox-faas/faas/pkg/api"
	"strings"
	"time"
)

func hasPendingFulfillmentReviews(coord *api.OperationWorkflowReadinessCoordination) bool {
	if coord == nil {
		return false
	}
	for _, r := range coord.FulfillmentReviews {
		if r.ReviewRequired && r.RequiredAt != nil && activeFulfillment(coord, r.Binding, r.FulfilledAt) != nil {
			return true
		}
	}
	return false
}
func fulfillmentReviewActionObligation(subscription string, sequence int64, r api.OperationWorkflowObligationFulfillmentReview) *api.OperationWorkflowActionObligation {
	if r.RequiredAt == nil {
		return nil
	}
	return &api.OperationWorkflowActionObligation{ID: readinessDigest([]any{subscription, sequence, "fulfillment_review", r.Binding.ObligationID, r.Binding.Case, r.FulfilledAt, r.BasisAt, *r.RequiredAt}), StartedAt: *r.RequiredAt}
}
func projectFulfillmentReviewActions(row api.OperationWorkflowReadinessFollowUp, subject api.OperationSubject, tenant string, opts api.OperationWorkflowCoordinationTimelineOptions, operator bool, now time.Time) []api.OperationWorkflowCaseNextAction {
	actions := []api.OperationWorkflowCaseNextAction{}
	for _, r := range row.Coordination.FulfillmentReviews {
		c := r.Binding.Case
		customer := c.PlatformTenantID
		if customer == "" {
			customer = row.PlatformTenantID
		}
		if c.Subject != subject || c.Workflow != opts.Workflow || c.InstanceID != opts.InstanceID || operator && !sameOperationHistoryIdentity(customer, tenant) || !r.ReviewRequired || r.RequiredAt == nil || activeFulfillment(&row.Coordination, r.Binding, r.FulfilledAt) == nil {
			continue
		}
		value := r
		if !operator {
			value.Binding.Case.PlatformTenantID = ""
		}
		actions = append(actions, api.OperationWorkflowCaseNextAction{FulfillmentReview: &value, Obligation: fulfillmentReviewActionObligation(row.SubscriptionID, row.Sequence, r), Kind: "fulfillment_review", Summary: "Fulfillment evidence needs explicit review: " + strings.Join(r.Reasons, ", ") + ". Reaffirm with fresh evidence or explicitly reopen; fulfillment remains recorded.", Owner: r.Owner, DueAt: r.DueAt, Overdue: r.DueAt != nil && !r.DueAt.After(now), Trigger: "human", SubscriptionID: row.SubscriptionID, Sequence: row.Sequence})
	}
	return actions
}
func fulfillmentActionFingerprint(a api.OperationWorkflowCaseNextAction) string {
	if a.FulfillmentReview == nil {
		return ""
	}
	return a.FulfillmentReview.Fingerprint
}
func nextActionDigestFingerprint(item api.OperationWorkflowNextActionQueueItem) string {
	if item.Action.FulfillmentReview != nil {
		value := *item.Action.FulfillmentReview
		value.CheckedAt = value.BasisAt
		value.CurrentOperationID = ""
		value.CurrentReportID = ""
		value.CurrentStateRevision = 0
		item.Action.FulfillmentReview = &value
	}
	return readinessDigest(item)
}

func fulfillmentReviewActionTerminal(n api.OperationWorkflowReadinessNotification, a api.OperationWorkflowCaseNextAction) string {
	r := a.FulfillmentReview
	if r == nil || r.RequiredAt == nil || n.Coordination == nil {
		return ""
	}
	for _, decision := range n.Coordination.Reaffirmations {
		if decision.FulfilledAt.Equal(r.FulfilledAt) && readinessDigest(decision.Binding) == readinessDigest(r.Binding) && !decision.ReaffirmedAt.Before(*r.RequiredAt) && !decision.ReaffirmedAt.Before(r.BasisAt) {
			return "completed"
		}
	}
	for _, decision := range n.Coordination.Reopenings {
		if decision.FulfilledAt.Equal(r.FulfilledAt) && readinessDigest(decision.Binding) == readinessDigest(r.Binding) && !decision.ReopenedAt.Before(*r.RequiredAt) {
			return "canceled"
		}
	}
	return ""
}
func checkFulfillmentReviewDigest(ctx context.Context, n api.OperationWorkflowReadinessNotification, sub api.OperationWorkflowReadinessSubscription, tenant, selectedTenant, workflow string, operator bool, saved api.OperationWorkflowNextActionQueueItem, at time.Time, result api.OperationWorkflowNextActionDigestCheckItem) (api.OperationWorkflowNextActionDigestCheckItem, error) {
	events, terminal := workflowObligationReceipts(n, saved.Action, at)
	result.Lifecycle = scopedDigestObligationLifecycle(events, saved, tenant, selectedTenant, sub.TenantID, operator)
	page, err := nextActionWorkQueue(ctx, []api.OperationWorkflowReadinessFollowUp{readinessFollowUpProjection(sub, tenant, n)}, selectedTenant, api.OperationWorkflowReadinessFollowUpOptions{Scope: sub.Scope, Workflow: workflow, ActionKind: "fulfillment_review"}, operator, readinessQueueOffsetMax("next_actions"), api.OperationWorkflowReadinessFollowUpsResponse{EvaluatedAt: time.Now().UTC()})
	if err != nil {
		return result, err
	}
	result.Status = "no_longer_outstanding"
	result.Explanation = "The saved review no longer projects; absent work alone does not establish a completed decision."
	for _, item := range page.NextActions {
		if saved.Action.Obligation != nil && item.Action.Obligation != nil && saved.Action.Obligation.ID == item.Action.Obligation.ID {
			value := item
			result.Current = &value
			result.Status = "outstanding"
			result.Explanation = "This exact fulfillment review remains pending. Inspect case evidence and reaffirm or reopen explicitly."
			result.ChangedFields = explainNextActionDigest(item, saved, true, true, time.Now().UTC()).ChangedFields
			if nextActionDigestFingerprint(item) != nextActionDigestFingerprint(saved) {
				result.Status = "changed"
			}
			return result, nil
		}
	}
	if decision := fulfillmentReviewActionTerminal(n, saved.Action); decision != "" {
		terminal = decision
	}
	if terminal != "" {
		result.Status = terminal
		result.Explanation = "An exact review-cycle receipt records " + terminal + ". Reaffirmation completes the review; reopening closes it while restoring the original work."
	}
	return result, nil
}
