package state

import (
	"context"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Capture only transitions in the locked source, never reconstruct an audit from old snapshots.
func captureObligationReceipts(id, scope string, before, after api.OperationWorkflowReadinessNotification, actor string) api.OperationWorkflowReadinessNotification {
	if after.NextActionDigest != nil {
		return after
	}
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
		page, err := nextActionWorkQueue(context.Background(), []api.OperationWorkflowReadinessFollowUp{row}, "", api.OperationWorkflowReadinessFollowUpOptions{Scope: scope}, true, readinessQueueOffsetMax("next_actions"), api.OperationWorkflowReadinessFollowUpsResponse{EvaluatedAt: now})
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
			if previous.Summary != next.Summary || readinessDigest(previous.Evidence) != readinessDigest(next.Evidence) {
				add(id, "changed", "The obligation plan or linked evidence changed.")
			}
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
			if terminal == "completed" {
				add(id, "completed", "This change fulfilled the coordination obligation.")
			} else if terminal == "canceled" {
				add(id, "canceled", "This obligation was canceled or declined.")
			} else {
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
		if _, exists := old[id]; !exists {
			add(id, "assigned", "This obligation became active for "+current[id].Owner+".")
		}
	}
	receipts = linkObligationSuccessors(before, after, old, current, cases, actor, now, receipts)
	after.ObligationReceiptsTruncated = before.ObligationReceiptsTruncated
	if len(receipts) > 128 {
		receipts = receipts[len(receipts)-128:]
		after.ObligationReceiptsTruncated = true
	}
	after.ObligationReceipts = receipts
	return after
}
