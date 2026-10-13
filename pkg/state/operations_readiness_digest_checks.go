package state

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// Read each retained source independently; no delivery or coordination state is written.
func checkNextActionDigest(ctx context.Context, store readinessSubscriptionBackend, account, tenant, app, scope, selectedTenant, workflow string, operator bool, digest *api.OperationWorkflowNextActionDigest) (*api.OperationWorkflowNextActionDigestCheck, error) {
	out := &api.OperationWorkflowNextActionDigestCheck{Items: []api.OperationWorkflowNextActionDigestCheckItem{}, RetentionLimited: true}
	if len(digest.Items) > 20 {
		return nil, ErrInvalidArgument
	}
	for _, saved := range digest.Items {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		a := saved.Action
		result := api.OperationWorkflowNextActionDigestCheckItem{SubscriptionID: a.SubscriptionID, Sequence: a.Sequence, ActionKind: a.Kind, Status: "unavailable", Explanation: "Source is missing, expired or outside the current access boundary; completion cannot be inferred.", ChangedFields: []string{}, HistoryLimited: true}
		sourceID, err := uuid.Parse(a.SubscriptionID)
		if err != nil || a.Sequence < 1 || saved.Scope != scope {
			out.Items = append(out.Items, result)
			continue
		}
		sourceTenant := tenant
		if operator {
			sourceTenant = selectedTenant
		}
		source, err := store.readReadinessInbox(ctx, account, sourceTenant, app, scope, sourceID.String(), a.Sequence-1, 1, operator)
		if errors.Is(err, ErrNotFound) {
			out.Items = append(out.Items, result)
			continue
		}
		if err != nil {
			return nil, err
		}
		if len(source.Items) != 1 || source.Items[0].Sequence != a.Sequence || source.Items[0].Coordination == nil {
			out.Items = append(out.Items, result)
			continue
		}
		n := source.Items[0]
		if a.Obligation != nil {
			result.ObligationID = a.Obligation.ID
		}
		if a.Kind == "fulfillment_review" {
			checked, e := checkFulfillmentReviewDigest(ctx, n, source.Subscription, sourceTenant, selectedTenant, workflow, operator, saved, digest.EvaluatedAt, result)
			if e != nil {
				return nil, e
			}
			out.Items = append(out.Items, checked)
			continue
		}
		lifecycle, terminal := workflowObligationReceipts(n, a, digest.EvaluatedAt)
		if a.Obligation != nil {
			all := len(saved.Cases) > 0
			for _, c := range saved.Cases {
				if !obligationFulfilled(*n.Coordination, a.Obligation.ID, c, source.Subscription.TenantID) {
					all = false
				}
			}
			lifecycle = appendFulfillmentEvents(lifecycle, n.Coordination, a.Obligation.ID)
			if all {
				terminal = "completed"
			}
		}
		result.Lifecycle = scopedDigestObligationLifecycle(lifecycle, saved, tenant, selectedTenant, source.Subscription.TenantID, operator)

		if completion := n.Coordination.Completion; completion != nil {
			result.Status = "no_longer_outstanding"
			result.Explanation = "The source follow-up was explicitly closed; this receipt alone does not establish completion of the saved obligation cycle."
			result.Completion = completion
			if completion.Outcome == "resolved" && (a.Obligation == nil || a.Kind == "follow_up") {
				result.Status = "completed"
				result.Explanation = "The source follow-up has an explicit resolved completion receipt. This confirms source coordination closure, not application success or live evidence health."
			}
			if terminal != "" {
				result.Status = terminal
				result.Explanation = "A retained lifecycle receipt explicitly records " + terminal + " for this saved obligation."
			}
			out.Items = append(out.Items, result)
			continue
		}
		row := readinessFollowUpProjection(source.Subscription, sourceTenant, n)
		now := time.Now().UTC()
		page, err := nextActionWorkQueue(ctx, []api.OperationWorkflowReadinessFollowUp{row}, selectedTenant, api.OperationWorkflowReadinessFollowUpOptions{AppID: app, Scope: scope, Workflow: workflow, ActionKind: a.Kind}, operator, readinessQueueOffsetMax("next_actions"), api.OperationWorkflowReadinessFollowUpsResponse{EvaluatedAt: now})
		if err != nil {
			return nil, err
		}
		result.Status = "no_longer_outstanding"
		result.Explanation = "The retained source no longer projects this action. It may have been superseded, canceled or otherwise changed; completion is not established."
		if len(page.NextActions) > 0 {
			current := page.NextActions[0]
			result.Current = &current
			result.ChangedFields = explainNextActionDigest(current, saved, true, true, now).ChangedFields
			result.Status = "outstanding"
			result.Explanation = "The source still projects this outstanding action. Inspect current source controls before acting."
			if a.Obligation != nil && current.Action.Obligation != nil && a.Obligation.ID != current.Action.Obligation.ID {
				result.Status = "replaced"
				result.Explanation = "A newer obligation of this kind is outstanding on the same source. Its current details do not describe the saved obligation."
			} else if readinessDigest(current) != readinessDigest(saved) {
				result.Status = "changed"
				result.Explanation = "The source still projects this action with changed details; use its current owner, deadline and source controls."
			}
		}
		if terminal != "" {
			result.Status = terminal
			result.Explanation = "A retained lifecycle receipt explicitly records " + terminal + " for this saved obligation; any current action with a different identity is a separate obligation."
		} else if a.Obligation == nil {
			result.Explanation += " This older digest has no obligation identity; exact cycle comparison is unavailable."
		}
		out.Items = append(out.Items, result)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// Shared-source edges are visible only when their saved coverage includes a delivered case.
func scopedDigestObligationLifecycle(events []api.OperationWorkflowObligationEvent, saved api.OperationWorkflowNextActionQueueItem, tenant, selectedTenant, sourceTenant string, operator bool) []api.OperationWorkflowObligationEvent {
	out := []api.OperationWorkflowObligationEvent{}
	for _, event := range events {
		visible := event.Replacement == nil && event.TargetBinding == nil && event.Fulfillment == nil && event.Reopening == nil && event.FulfillmentReview == nil && event.Reaffirmation == nil && event.ReviewCoordination == nil && event.FulfillmentReviewReminder == nil && event.FulfillmentReviewEscalation == nil
		if !visible {
			for _, covered := range event.Cases {
				for _, delivered := range saved.Cases {
					if covered.Subject != delivered.Subject || covered.Workflow != delivered.Workflow || covered.InstanceID != delivered.InstanceID {
						continue
					}
					if operator {
						actual, expected := covered.PlatformTenantID, delivered.PlatformTenantID
						if actual == "" {
							actual = sourceTenant
						}
						if expected == "" {
							expected = selectedTenant
						}
						if actual == "" || expected == "" || !sameOperationHistoryIdentity(actual, expected) {
							continue
						}
					} else if covered.PlatformTenantID != "" && !sameOperationHistoryIdentity(covered.PlatformTenantID, tenant) {
						continue
					}
					visible = true
				}
			}
		}
		if visible {
			if !operator && event.Fulfillment != nil {
				value := *event.Fulfillment
				value.Binding.Case.PlatformTenantID = ""
				value.Evidence.PlatformTenantID = ""
				event.Fulfillment = &value
			}
			if !operator && event.Reopening != nil {
				value := *event.Reopening
				value.Binding.Case.PlatformTenantID = ""
				event.Reopening = &value
			}
			if !operator && event.FulfillmentReview != nil {
				value := *event.FulfillmentReview
				value.Binding.Case.PlatformTenantID = ""
				event.FulfillmentReview = &value
			}
			if !operator && event.Reaffirmation != nil {
				value := *event.Reaffirmation
				value.Binding.Case.PlatformTenantID = ""
				value.Evidence.PlatformTenantID = ""
				event.Reaffirmation = &value
			}

			if !operator && event.FulfillmentReviewEscalation != nil {
				value := *event.FulfillmentReviewEscalation
				value.Review.Binding.Case.PlatformTenantID = ""
				event.FulfillmentReviewEscalation = &value
			}
			if !operator && event.FulfillmentReviewReminder != nil {
				value := *event.FulfillmentReviewReminder
				value.Review.Binding.Case.PlatformTenantID = ""
				event.FulfillmentReviewReminder = &value
			}
			if !operator && event.ReviewCoordination != nil {
				value := *event.ReviewCoordination
				value.Binding.Case.PlatformTenantID = ""
				event.ReviewCoordination = &value
			}
			event.Cases = nil
			out = append(out, event)
		}
	}
	slices.SortStableFunc(out, func(a, b api.OperationWorkflowObligationEvent) int { return a.At.Compare(b.At) })
	if len(out) > 40 {
		out = out[len(out)-40:]
	}
	return out
}
