package state

import (
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Relationships belong to one locked source and explicitly shared case coverage.
// Existing identities reactivated later are excluded to avoid inventing cyclic lineage.
func linkObligationSuccessors(before, after api.OperationWorkflowReadinessNotification, old, current map[string]api.OperationWorkflowCaseNextAction, cases map[string][]api.OperationWorkflowNextActionCase, actor string, now time.Time, receipts []api.OperationWorkflowObligationEvent) []api.OperationWorkflowObligationEvent {
	keys := []string{}
	for id := range current {
		keys = append(keys, id)
	}
	slices.Sort(keys)
	for _, successorID := range keys {
		successor := current[successorID]
		if _, exists := old[successorID]; exists {
			continue
		}
		if slices.ContainsFunc(before.ObligationReceipts, func(e api.OperationWorkflowObligationEvent) bool {
			return e.ObligationID == successorID || e.Replacement != nil && (e.Replacement.PredecessorID == successorID || e.Replacement.SuccessorID == successorID)
		}) {
			continue
		}
		predecessorID, predecessorKind := "", ""
		predecessorCases := []api.OperationWorkflowNextActionCase{}
		oldKeys := []string{}
		for id := range old {
			oldKeys = append(oldKeys, id)
		}
		slices.Sort(oldKeys)
		for pass := 0; pass < 2 && predecessorID == ""; pass++ {
			for _, id := range oldKeys {
				previous := old[id]
				if _, stillActive := current[id]; stillActive {
					continue
				}
				related := pass == 0 && explicitObligationSuccessor(before, after, previous, successor) || pass == 1 && previous.Kind == successor.Kind
				if !related || len(sharedObligationCases(cases[id], cases[successorID])) == 0 {
					continue
				}
				predecessorID, predecessorKind, predecessorCases = id, previous.Kind, cases[id]
				break
			}
		}
		if predecessorID == "" {
			for i := len(before.ObligationReceipts) - 1; i >= 0; i-- {
				event := before.ObligationReceipts[i]
				if event.ObligationID == "" || event.ObligationID == successorID || event.ActionKind != successor.Kind || event.Kind != "completed" && event.Kind != "fulfilled" && event.Kind != "canceled" && event.Kind != "superseded" {
					continue
				}
				if _, stillActive := current[event.ObligationID]; stillActive {
					continue
				}
				if len(sharedObligationCases(event.Cases, cases[successorID])) == 0 {
					continue
				}
				predecessorID, predecessorKind, predecessorCases = event.ObligationID, event.ActionKind, event.Cases
				break
			}
		}
		if predecessorID == "" {
			continue
		}
		reason, reasonKind := obligationSuccessorReason(before, after, successor.Kind)
		relation := &api.OperationWorkflowObligationReplacement{PredecessorID: predecessorID, SuccessorID: successorID, PredecessorKind: predecessorKind, SuccessorKind: successor.Kind, Reason: reason, ReasonKind: reasonKind}
		covered := sharedObligationCases(predecessorCases, cases[successorID])
		shared := len(predecessorCases) > len(covered) || len(cases[successorID]) > len(covered) || len(covered) > 1
		receipts = append(receipts,
			api.OperationWorkflowObligationEvent{ObligationID: predecessorID, ActionKind: predecessorKind, Cases: slices.Clone(covered), SharedPlan: shared, Kind: "replaced_by", At: now, Actor: actor, Explanation: "A successor obligation was recorded on this source.", Replacement: relation},
			api.OperationWorkflowObligationEvent{ObligationID: successorID, ActionKind: successor.Kind, Cases: slices.Clone(covered), SharedPlan: shared, Kind: "replaces", At: now, Actor: actor, Explanation: "A retained predecessor obligation was linked to this cycle.", Replacement: relation},
		)
	}
	return receipts
}
func sharedObligationCases(a, b []api.OperationWorkflowNextActionCase) []api.OperationWorkflowNextActionCase {
	out := []api.OperationWorkflowNextActionCase{}
	for _, c := range a {
		if slices.Contains(b, c) && !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	return out
}
func explicitObligationSuccessor(before, after api.OperationWorkflowReadinessNotification, previous, next api.OperationWorkflowCaseNextAction) bool {
	if previous.Obligation == nil || after.ReviewResponse == nil {
		return false
	}
	review := after.ReviewResponse.DecisionReview
	if previous.Kind == "handoff_reassignment" && next.Kind == "handoff_acceptance" && review != nil && review.Reassignment != nil {
		return review.Reassignment.Decline.At.Equal(previous.Obligation.StartedAt)
	}
	if previous.Kind == "coordinator_revision" && next.Kind == "coordinator_completion" && review != nil && review.EscalationAcknowledgment != nil {
		ack := review.EscalationAcknowledgment
		return ack.Basis != nil && ack.Basis.At.Equal(previous.Obligation.StartedAt)
	}
	if previous.Kind == "decision_revalidation" && next.Kind == "response_check_in" && after.ReviewResponse.Decision != nil {
		decision := after.ReviewResponse.Decision
		return before.ReviewResponse != nil && before.ReviewResponse.DecisionReview != nil && previous.Obligation.StartedAt.Equal(before.ReviewResponse.DecisionReview.RequiredAt) && decision.At.After(previous.Obligation.StartedAt) && (before.ReviewResponse.Decision == nil || !decision.At.Equal(before.ReviewResponse.Decision.At))
	}
	return false
}
func obligationSuccessorReason(before, after api.OperationWorkflowReadinessNotification, kind string) (string, string) {
	response := after.ReviewResponse
	if response != nil {
		if response.Decision != nil && (before.ReviewResponse == nil || before.ReviewResponse.Decision == nil || !response.Decision.At.Equal(before.ReviewResponse.Decision.At)) {
			return response.Decision.Reason, "recorded_decision"
		}
		if review := response.DecisionReview; review != nil {
			if kind == "handoff_acceptance" && review.Reassignment != nil && (before.ReviewResponse == nil || before.ReviewResponse.DecisionReview == nil || before.ReviewResponse.DecisionReview.Reassignment == nil || !review.Reassignment.At.Equal(before.ReviewResponse.DecisionReview.Reassignment.At)) {
				return review.Reassignment.Reason, "recorded_reassignment"
			}
			if kind == "coordinator_completion" && review.EscalationAcknowledgment != nil && review.EscalationAcknowledgment.Basis != nil {
				return "A revised coordinator commitment followed review of prior completion evidence.", "revised_commitment"
			}
		}
	}
	return "A later cycle of this obligation became active on the same source.", "new_cycle"
}
