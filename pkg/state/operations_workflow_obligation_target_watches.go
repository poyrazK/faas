package state

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Rotate through five retained sources per pass. Only explicit current-cycle bindings
// are observed; first observations establish a baseline without generating work.
func evaluateObligationTargets(ctx context.Context, store readinessSubscriptionBackend, r *readinessSubscriptionRecord) ([]readinessWatchUpdate, error) {
	page, err := store.readReadinessInbox(ctx, r.Account, r.Tenant, r.Subscription.AppID, r.Subscription.Scope, r.Subscription.ID, 0, api.OperationWorkflowReadinessInboxMax, r.Operator)
	if err != nil {
		return nil, err
	}
	cases, ok := store.(OperationWorkflowCaseStore)
	if !ok {
		return nil, fmt.Errorf("workflow case backend unavailable")
	}
	candidates := []api.OperationWorkflowReadinessNotification{}
	retained := map[string]bool{}
	for _, n := range page.Items {
		if n.Coordination == nil || len(n.Coordination.TargetBindings) == 0 {
			continue
		}
		candidates = append(candidates, n)
		for _, binding := range n.Coordination.TargetBindings {
			retained[targetObservationKey(n, binding)] = true
		}
	}
	for key := range r.TargetObservations {
		if !retained[key] {
			delete(r.TargetObservations, key)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i].Sequence > r.TargetCursor, candidates[j].Sequence > r.TargetCursor
		if a != b {
			return a
		}
		return candidates[i].Sequence < candidates[j].Sequence
	})
	if len(candidates) > 5 {
		candidates = candidates[:5]
	}
	updates := []readinessWatchUpdate{}
	var firstErr error
	for _, n := range candidates {
		r.TargetCursor = n.Sequence
		for _, binding := range n.Coordination.TargetBindings {
			if err := ctx.Err(); err != nil {
				return updates, err
			}
			tenant := r.Tenant
			opts := api.OperationWorkflowCoordinationTimelineOptions{AppID: r.Subscription.AppID, Scope: r.Subscription.Scope, SubjectType: binding.Case.Subject.Type, SubjectID: binding.Case.Subject.ID, Workflow: binding.Case.Workflow, InstanceID: binding.Case.InstanceID}
			if r.Operator {
				tenant = binding.Case.PlatformTenantID
				opts.TenantID = tenant
			}
			current, err := cases.WorkflowCase(ctx, r.Account, tenant, opts, r.Operator)
			if errors.Is(err, ErrNotFound) {
				current.ObligationAssessments = []api.OperationWorkflowObligationAssessment{{SubscriptionID: r.Subscription.ID, Sequence: n.Sequence, ObligationID: binding.ObligationID, Binding: &binding, Status: "target_unavailable", Summary: "The bound workflow case is no longer available. Refresh evidence or revise the binding; obligation fulfillment is not established.", CheckedAt: time.Now().UTC()}}
				err = nil
			}
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			for _, assessment := range current.ObligationAssessments {
				if assessment.SubscriptionID != r.Subscription.ID || assessment.Sequence != n.Sequence || assessment.ObligationID != binding.ObligationID || assessment.Binding == nil {
					continue
				}
				// Preserve the exact stored identity for the commit fence, including customer scope.
				assessment.Binding = &binding
				assessment.CheckedAt = time.Now().UTC().Truncate(time.Microsecond)
				updates = append(updates, readinessWatchUpdate{Sequence: n.Sequence, SourceID: n.ID, Revision: n.Coordination.Revision, TargetAssessment: &assessment})
				break
			}
		}
	}
	return updates, firstErr
}

func targetObservationKey(n api.OperationWorkflowReadinessNotification, binding api.OperationWorkflowObligationTargetBinding) string {
	return n.ID + "/" + readinessDigest(binding)
}

// Called with the source locked and the subscription lease validated. An observation
// and its durable notice commit together; source edits fence prepared observations.
func applyObligationTargetObservation(ctx context.Context, r *readinessSubscriptionRecord, n api.OperationWorkflowReadinessNotification, u readinessWatchUpdate) (api.OperationWorkflowReadinessNotification, []api.OperationWorkflowReadinessNotification, bool) {
	assessment := u.TargetAssessment
	if assessment == nil || assessment.Binding == nil || n.ID != u.SourceID || n.Coordination == nil || n.Coordination.Revision != u.Revision {
		return n, nil, false
	}
	bound := false
	for _, binding := range n.Coordination.TargetBindings {
		if readinessDigest(binding) == readinessDigest(*assessment.Binding) {
			bound = true
			break
		}
	}
	if !bound {
		return n, nil, false
	}
	active := false
	owner := n.Coordination.Owner
	for _, action := range sourceObligationActions(ctx, r.Subscription.ID, r.Subscription.Scope, n, r.Subscription.TenantID) {
		if action.Action.Obligation != nil && action.Action.Obligation.ID == assessment.ObligationID {
			active = true
			owner = action.Action.Owner
			break
		}
	}
	if !active {
		return n, nil, false
	}
	key := targetObservationKey(n, *assessment.Binding)
	if r.TargetObservations == nil {
		r.TargetObservations = map[string]string{}
	}
	previous := r.TargetObservations[key]
	if previous == assessment.Status {
		return n, nil, false
	}
	r.TargetObservations[key] = assessment.Status
	if previous == "" {
		return n, nil, true
	}
	notice := readinessSubscriptionNotify(r, "obligation_target_changed", fmt.Sprintf("Bound obligation prerequisites changed from %s to %s. %s", previous, assessment.Status, assessment.Summary), nil)
	notice.TargetAssessment = assessment
	notice.Coordination = &api.OperationWorkflowReadinessCoordination{Status: "unread", Owner: owner, Revision: 1, UpdatedAt: notice.CreatedAt, UpdatedBy: "system:obligation-target"}
	return n, []api.OperationWorkflowReadinessNotification{notice}, true
}
