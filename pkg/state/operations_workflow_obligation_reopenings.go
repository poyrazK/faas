package state

import (
	"context"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
)

func fulfillmentReopened(coord api.OperationWorkflowReadinessCoordination, receipt api.OperationWorkflowObligationFulfillment) bool {
	for _, reopening := range coord.Reopenings {
		if reopening.FulfilledAt.Equal(receipt.CompletedAt) && readinessDigest(reopening.Binding) == readinessDigest(receipt.Binding) {
			return true
		}
	}
	return false
}

func prepareObligationReopening(ctx context.Context, store readinessSubscriptionBackend, account, tenant, app, scope, id string, sequence int64, req api.OperationWorkflowReadinessCoordinationRequest, operator bool) (*api.OperationWorkflowObligationReopening, error) {
	input := req.Reopening
	if input == nil {
		return nil, nil
	}
	if req.Fulfillment != nil || req.ReviewCoordination != nil || req.Reaffirmation != nil || req.TargetBinding != nil || req.Completion != nil || req.ReviewResponse != nil || input.FulfilledAt.IsZero() || input.Reason == "" || len(input.Reason) > 2048 || strings.TrimSpace(input.Reason) != input.Reason || !utf8.ValidString(input.Reason) || strings.IndexFunc(input.Reason, unicode.IsControl) >= 0 {
		return nil, ErrInvalidArgument
	}
	if !operator && input.Binding.Case.PlatformTenantID != "" || operator && input.Binding.Case.PlatformTenantID == "" {
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
	sourceTenant := tenant
	if operator {
		sourceTenant = page.Subscription.TenantID
	}
	return currentObligationReopening(ctx, n, id, scope, sourceTenant, *input)
}

// Validate against the latest fulfillment and the source's unsuppressed projection.
// Clearing/rebinding a target, retiring a plan or starting a new cycle fences reopening.
func currentObligationReopening(ctx context.Context, n api.OperationWorkflowReadinessNotification, id, scope, sourceTenant string, input api.OperationWorkflowObligationReopeningRequest) (*api.OperationWorkflowObligationReopening, error) {
	coord := n.Coordination
	if coord == nil || len(coord.Reopenings) >= 128 || !obligationFulfilled(*coord, input.Binding.ObligationID, input.Binding.Case, sourceTenant) {
		return nil, ErrConflict
	}
	bound := false
	for _, binding := range coord.TargetBindings {
		if readinessDigest(binding) == readinessDigest(input.Binding) {
			bound = true
		}
	}
	if !bound {
		return nil, ErrConflict
	}
	var latest *api.OperationWorkflowObligationFulfillment
	for i := len(coord.Fulfillments) - 1; i >= 0; i-- {
		receipt := coord.Fulfillments[i]
		b := receipt.Binding
		if b.ObligationID == input.Binding.ObligationID && b.Case == input.Binding.Case {
			latest = &receipt
			break
		}
	}
	if latest == nil || !latest.CompletedAt.Equal(input.FulfilledAt) || readinessDigest(latest.Binding) != readinessDigest(input.Binding) || fulfillmentReopened(*coord, *latest) {
		return nil, ErrConflict
	}
	raw := n
	copy := *coord
	copy.Fulfillments = nil
	raw.Coordination = &copy
	for _, item := range sourceObligationActions(ctx, id, scope, raw, sourceTenant) {
		if item.Action.Obligation == nil || item.Action.Obligation.ID != input.Binding.ObligationID || !bindableObligationKind(item.Action.Kind) {
			continue
		}
		for _, c := range item.Cases {
			b := input.Binding.Case
			if c.Subject == b.Subject && c.Workflow == b.Workflow && c.InstanceID == b.InstanceID && (b.PlatformTenantID == "" || sameOperationHistoryIdentity(c.PlatformTenantID, b.PlatformTenantID)) {
				return &api.OperationWorkflowObligationReopening{SourceSequence: n.Sequence, Binding: input.Binding, FulfilledAt: latest.CompletedAt, Reason: input.Reason, ActionKind: item.Action.Kind, Owner: item.Action.Owner}, nil
			}
		}
	}
	return nil, ErrConflict
}

func applyObligationReopening(ctx context.Context, n api.OperationWorkflowReadinessNotification, req readinessCoordinationUpdate, actor string, now time.Time) (api.OperationWorkflowReadinessNotification, error) {
	if req.Reopening == nil {
		return n, nil
	}
	if req.PreparedReopening == nil {
		return n, ErrConflict
	}
	reopening, err := currentObligationReopening(ctx, n, req.TargetSubscriptionID, req.TargetScope, req.TargetSourceTenant, *req.Reopening)
	if err != nil {
		return n, err
	}
	if reopening.Owner != req.PreparedReopening.Owner || reopening.ActionKind != req.PreparedReopening.ActionKind {
		return n, ErrConflict
	}
	reopening.ReopenedAt, reopening.ReopenedBy = now, actor
	coord := *n.Coordination
	coord.Reopenings = append(slices.Clone(coord.Reopenings), *reopening)
	n.Coordination = &coord
	return n, nil
}

func reopeningEvent(reopening api.OperationWorkflowObligationReopening) api.OperationWorkflowObligationEvent {
	value := reopening
	return api.OperationWorkflowObligationEvent{Reopening: &value, ObligationID: reopening.Binding.ObligationID, ActionKind: reopening.ActionKind, Cases: []api.OperationWorkflowNextActionCase{reopening.Binding.Case}, Kind: "reopened", At: reopening.ReopenedAt, Actor: reopening.ReopenedBy, Explanation: "Explicit case obligation reopening: " + reopening.Reason}
}

func reopeningNoticeKey(n api.OperationWorkflowReadinessNotification, reopening api.OperationWorkflowObligationReopening) string {
	return n.ID + "/" + readinessDigest(reopening)
}

// Pending receipts are durable; delivery checkpoints commit with notices under the lease.
func evaluateObligationReopenings(ctx context.Context, store readinessSubscriptionBackend, r *readinessSubscriptionRecord) ([]readinessWatchUpdate, error) {
	page, err := store.readReadinessInbox(ctx, r.Account, r.Tenant, r.Subscription.AppID, r.Subscription.Scope, r.Subscription.ID, 0, api.OperationWorkflowReadinessInboxMax, r.Operator)
	if err != nil {
		return nil, err
	}
	candidates := []api.OperationWorkflowReadinessNotification{}
	retained := map[string]bool{}
	for _, n := range page.Items {
		if n.Coordination == nil || len(n.Coordination.Reopenings) == 0 {
			continue
		}
		candidates = append(candidates, n)
		for _, reopening := range n.Coordination.Reopenings {
			retained[reopeningNoticeKey(n, reopening)] = true
		}
	}
	for key := range r.ReopeningKnown {
		if !retained[key] {
			delete(r.ReopeningKnown, key)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i].Sequence > r.ReopeningCursor, candidates[j].Sequence > r.ReopeningCursor
		if a != b {
			return a
		}
		return candidates[i].Sequence < candidates[j].Sequence
	})
	if len(candidates) > 5 {
		candidates = candidates[:5]
	}
	updates := []readinessWatchUpdate{}
	for _, n := range candidates {
		r.ReopeningCursor = n.Sequence
		for _, reopening := range n.Coordination.Reopenings {
			if err := ctx.Err(); err != nil {
				return updates, err
			}
			if len(updates) >= 20 {
				return updates, nil
			}
			if r.ReopeningKnown[reopeningNoticeKey(n, reopening)] {
				continue
			}
			value := reopening
			updates = append(updates, readinessWatchUpdate{Reopening: &value, Sequence: n.Sequence, SourceID: n.ID, Revision: n.Coordination.Revision})
		}
	}
	return updates, nil
}

func applyObligationReopeningNotice(r *readinessSubscriptionRecord, n api.OperationWorkflowReadinessNotification, u readinessWatchUpdate) (api.OperationWorkflowReadinessNotification, []api.OperationWorkflowReadinessNotification, bool) {
	if u.Reopening == nil || n.ID != u.SourceID || n.Coordination == nil || n.Coordination.Revision != u.Revision {
		return n, nil, false
	}
	found := false
	for _, reopening := range n.Coordination.Reopenings {
		if readinessDigest(reopening) == readinessDigest(*u.Reopening) {
			found = true
		}
	}
	key := reopeningNoticeKey(n, *u.Reopening)
	if !found || r.ReopeningKnown[key] {
		return n, nil, false
	}
	if r.ReopeningKnown == nil {
		r.ReopeningKnown = map[string]bool{}
	}
	r.ReopeningKnown[key] = true
	notice := readinessSubscriptionNotify(r, "obligation_reopened", "A fulfilled case obligation was explicitly reopened: "+u.Reopening.Reason+". Inspect the current source before acting; this notice retains the reopening observation.", nil)
	notice.ObligationReopening = u.Reopening
	notice.Coordination = &api.OperationWorkflowReadinessCoordination{Status: "unread", Owner: u.Reopening.Owner, Revision: 1, UpdatedAt: notice.CreatedAt, UpdatedBy: "system:obligation-reopening"}
	return n, []api.OperationWorkflowReadinessNotification{notice}, true
}

func newObligationReopenings(before, after *api.OperationWorkflowReadinessCoordination) []api.OperationWorkflowObligationReopening {
	out := []api.OperationWorkflowObligationReopening{}
	if after == nil {
		return out
	}
	for _, reopening := range after.Reopenings {
		if before != nil && slices.ContainsFunc(before.Reopenings, func(previous api.OperationWorkflowObligationReopening) bool {
			return readinessDigest(previous) == readinessDigest(reopening)
		}) {
			continue
		}
		out = append(out, reopening)
	}
	return out
}
