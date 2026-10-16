package state

import (
	"context"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
)

func prepareObligationTarget(ctx context.Context, store readinessSubscriptionBackend, account, tenant, app, scope, id string, sequence int64, req api.OperationWorkflowReadinessCoordinationRequest, operator bool) (*api.OperationWorkflowObligationTargetBinding, error) {
	input := req.TargetBinding
	if input == nil {
		return nil, nil
	}
	if input.ObligationID == "" || len(input.ObligationID) > 128 || input.Reason == "" || len(input.Reason) > 2048 || strings.TrimSpace(input.Reason) != input.Reason || !utf8.ValidString(input.Reason) || strings.IndexFunc(input.Reason, unicode.IsControl) >= 0 || req.Reopening != nil || req.ReviewCoordination != nil || req.Reaffirmation != nil || req.ReviewResponse != nil || req.Completion != nil || input.Clear == (input.Transition != nil) {
		return nil, ErrInvalidArgument
	}
	c := input.Case
	if !operator && c.PlatformTenantID != "" || operator && c.PlatformTenantID == "" {
		return nil, ErrInvalidArgument
	}
	page, err := store.readReadinessInbox(ctx, account, tenant, app, scope, id, sequence-1, 1, operator)
	if err != nil {
		return nil, err
	}
	if len(page.Items) != 1 || page.Items[0].Sequence != sequence {
		return nil, ErrNotFound
	}
	n := page.Items[0]
	if n.Coordination == nil || n.Coordination.Revision != req.Revision {
		return nil, ErrConflict
	}
	if req.Status != n.Coordination.Status || req.Owner != n.Coordination.Owner || req.Note != n.Coordination.Note || readinessDigest(req.FollowUpAt) != readinessDigest(n.Coordination.FollowUpAt) {
		return nil, ErrInvalidArgument
	}
	if input.Clear {
		for _, binding := range n.Coordination.TargetBindings {
			if binding.ObligationID == input.ObligationID && binding.Case == c {
				return &binding, nil
			}
		}
		return nil, ErrNotFound
	}
	cases, ok := store.(OperationWorkflowCaseStore)
	if !ok {
		return nil, ErrInvalidArgument
	}
	opts := api.OperationWorkflowCoordinationTimelineOptions{AppID: app, Scope: scope, SubjectType: c.Subject.Type, SubjectID: c.Subject.ID, Workflow: c.Workflow, InstanceID: c.InstanceID}
	if operator {
		opts.TenantID = c.PlatformTenantID
	}
	current, err := cases.WorkflowCase(ctx, account, tenant, opts, operator)
	if err != nil {
		return nil, err
	}
	if current.Instance == nil || current.Instance.State == nil || current.Instance.State.Stale || current.Instance.State.Terminal || current.Instance.Readiness == nil {
		return nil, ErrConflict
	}
	found := false
	for _, action := range current.NextActions {
		if action.SubscriptionID == id && action.Sequence == sequence && action.Obligation != nil && action.Obligation.ID == input.ObligationID && bindableObligationKind(action.Kind) {
			found = true
		}
	}
	if !found {
		return nil, ErrConflict
	}
	target := *input.Transition
	declared := false
	for _, edge := range current.Instance.Readiness.Items {
		if edge.Declared && edge.ContractVersion == target.ContractVersion && edge.Transition.Operation == target.Operation && edge.Transition.From == target.From && edge.Transition.To == target.To {
			declared = true
		}
	}
	if !declared {
		return nil, ErrInvalidArgument
	}
	return &api.OperationWorkflowObligationTargetBinding{ObligationID: input.ObligationID, Case: c, Transition: target, Reason: input.Reason}, nil
}
func bindableObligationKind(kind string) bool {
	return kind == "response_check_in" || kind == "coordinator_completion" || kind == "follow_up"
}
func sourceObligationActions(ctx context.Context, id, scope string, n api.OperationWorkflowReadinessNotification, sourceTenant string) []api.OperationWorkflowNextActionQueueItem {
	if n.Coordination == nil {
		return nil
	}
	row := readinessFollowUpProjection(api.OperationWorkflowReadinessSubscription{ID: id, Scope: scope, TenantID: sourceTenant}, sourceTenant, n)
	page, _ := nextActionWorkQueue(ctx, []api.OperationWorkflowReadinessFollowUp{row}, "", api.OperationWorkflowReadinessFollowUpOptions{Scope: scope}, true, readinessQueueOffsetMax("next_actions"), api.OperationWorkflowReadinessFollowUpsResponse{EvaluatedAt: time.Now().UTC()})
	return page.NextActions
}
func applyObligationTarget(ctx context.Context, n api.OperationWorkflowReadinessNotification, req readinessCoordinationUpdate, actor string, now time.Time) (api.OperationWorkflowReadinessNotification, error) {
	if req.TargetBinding == nil {
		return n, nil
	}
	binding := req.PreparedTargetBinding
	if binding == nil || n.Coordination == nil {
		return n, ErrConflict
	}
	actions := sourceObligationActions(ctx, req.TargetSubscriptionID, req.TargetScope, n, req.TargetSourceTenant)
	active := false
	activeIDs := map[string]bool{}
	for _, item := range actions {
		if item.Action.Obligation == nil {
			continue
		}
		activeIDs[item.Action.Obligation.ID] = true
		if item.Action.Obligation.ID != binding.ObligationID || !bindableObligationKind(item.Action.Kind) {
			continue
		}
		if req.TargetBinding.Clear {
			active = true
			continue
		}
		for _, c := range item.Cases {
			if c.Subject == binding.Case.Subject && c.Workflow == binding.Case.Workflow && c.InstanceID == binding.Case.InstanceID && (binding.Case.PlatformTenantID == "" || sameOperationHistoryIdentity(c.PlatformTenantID, binding.Case.PlatformTenantID)) {
				active = true
			}
		}
	}
	if !active {
		return n, ErrConflict
	}
	bindings := []api.OperationWorkflowObligationTargetBinding{}
	matched := false
	for _, existing := range n.Coordination.TargetBindings {
		if existing.ObligationID == binding.ObligationID && existing.Case == binding.Case {
			matched = true
			continue
		}
		if activeIDs[existing.ObligationID] {
			bindings = append(bindings, existing)
		}
	}
	if req.TargetBinding.Clear && !matched {
		return n, ErrConflict
	}
	if !req.TargetBinding.Clear {
		value := *binding
		value.BoundAt = now
		value.BoundBy = actor
		bindings = append(bindings, value)
	}
	if len(bindings) > 32 {
		return n, ErrConflict
	}
	c := *n.Coordination
	c.TargetBindings = slices.Clone(bindings)
	n.Coordination = &c
	if req.TargetBinding.Clear {
		value := *binding
		n.ObligationReceipts = append(slices.Clone(n.ObligationReceipts), api.OperationWorkflowObligationEvent{ObligationID: binding.ObligationID, Cases: []api.OperationWorkflowNextActionCase{binding.Case}, Kind: "target_cleared", At: now, Actor: actor, Explanation: "Target cleared: " + req.TargetBinding.Reason, TargetBinding: &value})
	}
	return n, nil
}

func captureObligationTargetReceipts(before, after api.OperationWorkflowReadinessNotification, old, current map[string]api.OperationWorkflowCaseNextAction, cases map[string][]api.OperationWorkflowNextActionCase, actor string, now time.Time, receipts []api.OperationWorkflowObligationEvent) []api.OperationWorkflowObligationEvent {
	if before.Coordination == nil || after.Coordination == nil {
		return receipts
	}
	add := func(binding api.OperationWorkflowObligationTargetBinding, kind, reason string) {
		action, exists := current[binding.ObligationID]
		if !exists {
			action, exists = old[binding.ObligationID]
		}
		if !exists {
			return
		}
		value := binding
		receipts = append(receipts, api.OperationWorkflowObligationEvent{ObligationID: binding.ObligationID, ActionKind: action.Kind, Cases: []api.OperationWorkflowNextActionCase{binding.Case}, SharedPlan: len(cases[binding.ObligationID]) > 1, Kind: kind, At: now, Actor: actor, Explanation: reason, TargetBinding: &value})
	}
	for _, binding := range after.Coordination.TargetBindings {
		found := false
		for _, previous := range before.Coordination.TargetBindings {
			if readinessDigest(previous) == readinessDigest(binding) {
				found = true
			}
		}
		if !found {
			add(binding, "target_bound", "Explicit transition target recorded: "+binding.Reason)
		}
	}
	for _, binding := range before.Coordination.TargetBindings {
		retained := false
		for _, next := range after.Coordination.TargetBindings {
			if next.ObligationID == binding.ObligationID && next.Case == binding.Case {
				retained = true
			}
		}
		if !retained && current[binding.ObligationID].Obligation != nil {
			recorded := false
			for _, event := range after.ObligationReceipts {
				if event.Kind == "target_cleared" && event.ObligationID == binding.ObligationID && event.TargetBinding != nil && event.TargetBinding.Case == binding.Case && event.At.Equal(after.Coordination.UpdatedAt) && !hasObligationEvent(before.ObligationReceipts, event) {
					event.ActionKind = current[binding.ObligationID].Kind
					event.SharedPlan = len(cases[binding.ObligationID]) > 1
					receipts = append(receipts, event)
					recorded = true
				}
			}
			if !recorded {
				add(binding, "target_cleared", "The explicit transition target was cleared.")
			}
		}
	}
	return receipts
}
