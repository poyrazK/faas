package state

import (
	"slices"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
)

type workflowCaseObligationHistory struct {
	tenant    string
	opts      api.OperationWorkflowCoordinationTimelineOptions
	operator  bool
	items     []api.OperationWorkflowCaseObligationHistoryItem
	count     int
	truncated bool
}

func newWorkflowCaseObligationHistory(tenant string, opts api.OperationWorkflowCoordinationTimelineOptions, operator bool) *workflowCaseObligationHistory {
	return &workflowCaseObligationHistory{tenant: canonicalReadinessTenant(tenant), opts: opts, operator: operator, items: []api.OperationWorkflowCaseObligationHistoryItem{}}
}
func (h *workflowCaseObligationHistory) collect(subscription api.OperationWorkflowReadinessSubscription, n api.OperationWorkflowReadinessNotification) {
	groups := map[string]*api.OperationWorkflowCaseObligationHistoryItem{}
	for _, event := range n.ObligationReceipts {
		if event.ObligationID == "" || event.ActionKind == "" {
			continue
		}
		selected := []api.OperationWorkflowNextActionCase{}
		for _, ref := range event.Cases {
			if ref.Subject.Type != h.opts.SubjectType || ref.Subject.ID != h.opts.SubjectID || ref.Workflow != h.opts.Workflow || ref.InstanceID != h.opts.InstanceID {
				continue
			}
			customer := ref.PlatformTenantID
			if h.operator && customer == "" {
				customer = subscription.TenantID
			}
			if h.operator && !sameOperationHistoryIdentity(customer, h.tenant) || !h.operator && customer != "" && !sameOperationHistoryIdentity(customer, h.tenant) {
				continue
			}
			if !h.operator {
				ref.PlatformTenantID = ""
			}
			if !slices.Contains(selected, ref) {
				selected = append(selected, ref)
			}
		}
		if len(selected) == 0 {
			continue
		}
		shared := event.SharedPlan || len(event.Cases) > 1
		event.Cases = selected
		group := groups[event.ObligationID]
		if group == nil {
			group = &api.OperationWorkflowCaseObligationHistoryItem{ObligationID: event.ObligationID, ActionKind: event.ActionKind, SubscriptionID: subscription.ID, Sequence: n.Sequence, Scope: subscription.Scope, Events: []api.OperationWorkflowObligationEvent{}}
			if h.operator {
				group.SubscriptionTenantID = subscription.TenantID
			}
			groups[event.ObligationID] = group
		}
		group.SharedPlan = group.SharedPlan || shared
		group.Events = append(group.Events, event)
	}
	if len(groups) > 0 && n.ObligationReceiptsTruncated {
		h.truncated = true
	}
	for _, group := range groups {
		slices.SortStableFunc(group.Events, func(a, b api.OperationWorkflowObligationEvent) int {
			if order := a.At.Compare(b.At); order != 0 {
				return order
			}
			if a.Kind < b.Kind {
				return -1
			}
			if a.Kind > b.Kind {
				return 1
			}
			return 0
		})
		if len(group.Events) > 40 {
			group.Events = group.Events[len(group.Events)-40:]
			group.EventsTruncated = true
			h.truncated = true
		}
		h.items = append(h.items, *group)
		h.count++
	}
	slices.SortFunc(h.items, func(a, b api.OperationWorkflowCaseObligationHistoryItem) int {
		at, bt := a.Events[len(a.Events)-1].At, b.Events[len(b.Events)-1].At
		if order := bt.Compare(at); order != 0 {
			return order
		}
		ak, bk := caseObligationHistoryKey(a.SubscriptionID, a.Sequence, a.ObligationID), caseObligationHistoryKey(b.SubscriptionID, b.Sequence, b.ObligationID)
		if ak < bk {
			return -1
		}
		if ak > bk {
			return 1
		}
		return 0
	})
	if len(h.items) > 100 {
		h.items = h.items[:100]
		h.truncated = true
	}
}
func caseObligationHistoryKey(subscription string, sequence int64, obligation string) string {
	return subscription + "/" + strconv.FormatInt(sequence, 10) + "/" + obligation
}
func (h *workflowCaseObligationHistory) finish(out *api.OperationWorkflowCase) {
	current := map[string]api.OperationWorkflowCaseNextAction{}
	for _, action := range out.NextActions {
		if action.Obligation != nil {
			current[caseObligationHistoryKey(action.SubscriptionID, action.Sequence, action.Obligation.ID)] = action
		}
	}
	for i := range h.items {
		item := &h.items[i]
		if action, ok := current[caseObligationHistoryKey(item.SubscriptionID, item.Sequence, item.ObligationID)]; ok {
			item.Current = &action
		}
	}
	out.ObligationHistory = h.items
	out.ObligationHistoryCount = h.count
	out.ObligationHistoryTruncated = h.truncated || out.Timeline.SourcesTruncated
	out.ObligationHistoryLimited = true
	out.ObligationHistoryCheckedAt = out.Timeline.EvaluatedAt
	out.Limitations = append(out.Limitations, "Obligation history uses saved receipt case identities. Older unscoped receipts, deleted sources and pruned events are unavailable; absence of current work does not prove completion.")
}
