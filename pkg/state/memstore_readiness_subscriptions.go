package state

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) insertReadinessSubscription(_ context.Context, r readinessSubscriptionRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	data := m.operationMemoryLocked()
	if data.readinessSubscriptions == nil {
		data.readinessSubscriptions = map[string]readinessSubscriptionRecord{}
		data.readinessInbox = map[string][]api.OperationWorkflowReadinessNotification{}
	}
	count := 0
	for _, stored := range data.readinessSubscriptions {
		if sameOperationHistoryIdentity(stored.Account, r.Account) && sameOperationHistoryIdentity(stored.Subscription.AppID, r.Subscription.AppID) {
			count++
		}
	}
	if count >= api.OperationWorkflowReadinessSubscriptionsPerAppMax {
		return NewOperationLimitError("readiness_subscriptions_per_app", int64(api.OperationWorkflowReadinessSubscriptionsPerAppMax), int64(count+1))
	}
	owned := false
	for _, app := range m.apps {
		if sameOperationHistoryIdentity(app.ID, r.Subscription.AppID) && sameOperationHistoryIdentity(app.AccountID, r.Account) {
			owned = true
			break
		}
	}
	if !owned {
		return ErrNotFound
	}
	if r.Tenant != "" {
		found := false
		for _, tenant := range m.platformTenants {
			if sameOperationHistoryIdentity(tenant.ID, r.Tenant) && sameOperationHistoryIdentity(tenant.AccountID, r.Account) {
				found = true
				break
			}
		}
		if !found {
			return ErrNotFound
		}
	}
	data.readinessSubscriptions[r.Subscription.ID] = cloneReadinessRecord(r)
	data.readinessInbox[r.Subscription.ID] = []api.OperationWorkflowReadinessNotification{}
	return nil
}
func readinessRecordMatches(r readinessSubscriptionRecord, account, tenant, app, scope string, operator bool) bool {
	return sameOperationHistoryIdentity(r.Account, account) && sameOperationHistoryIdentity(r.Subscription.AppID, app) && r.Operator == operator && r.Subscription.Scope == scope && (tenant == "" || sameOperationHistoryIdentity(r.Tenant, tenant))
}
func (m *MemStore) listReadinessSubscriptions(_ context.Context, account, tenant, app, scope string, operator bool) ([]api.OperationWorkflowReadinessSubscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []api.OperationWorkflowReadinessSubscription{}
	for _, r := range m.operationMemoryLocked().readinessSubscriptions {
		if readinessRecordMatches(r, account, tenant, app, scope, operator) {
			out = append(out, cloneReadinessRecord(r).Subscription)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}
func (m *MemStore) deleteReadinessSubscription(_ context.Context, account, tenant, app, scope, id string, operator bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	data := m.operationMemoryLocked()
	r, ok := data.readinessSubscriptions[id]
	if !ok || !readinessRecordMatches(r, account, tenant, app, scope, operator) {
		return ErrNotFound
	}
	delete(data.readinessSubscriptions, id)
	delete(data.readinessInbox, id)
	return nil
}
func (m *MemStore) readReadinessInbox(_ context.Context, account, tenant, app, scope, id string, after int64, limit int, operator bool) (api.OperationWorkflowReadinessInboxResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data := m.operationMemoryLocked()
	r, ok := data.readinessSubscriptions[id]
	if !ok || !readinessRecordMatches(r, account, tenant, app, scope, operator) {
		return api.OperationWorkflowReadinessInboxResponse{}, ErrNotFound
	}
	items := []api.OperationWorkflowReadinessNotification{}
	for _, n := range data.readinessInbox[id] {
		if n.Sequence > after && len(items) < limit {
			items = append(items, n)
		}
	}
	// JSON copy preserves MemStore's isolation from caller mutation of nested observations.
	page := readinessInboxPage(cloneReadinessRecord(r).Subscription, items, after)
	return cloneReadinessInbox(page), nil
}
func (m *MemStore) claimReadinessSubscriptions(_ context.Context) ([]readinessSubscriptionRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data := m.operationMemoryLocked()
	now := time.Now().UTC()
	out := []readinessSubscriptionRecord{}
	for _, r := range data.readinessSubscriptions {
		if !r.NextEvaluation.After(now) && !r.LeaseUntil.After(now) {
			out = append(out, cloneReadinessRecord(r))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].NextEvaluation.Equal(out[j].NextEvaluation) {
			return out[i].Subscription.ID < out[j].Subscription.ID
		}
		return out[i].NextEvaluation.Before(out[j].NextEvaluation)
	})
	if len(out) > api.OperationWorkflowReadinessSubscriptionBatchMax {
		out = out[:api.OperationWorkflowReadinessSubscriptionBatchMax]
	}
	for i := range out {
		out[i].Lease = uuid.NewString()
		out[i].LeaseUntil = now.Add(api.OperationWorkflowReadinessSubscriptionLease)
		data.readinessSubscriptions[out[i].Subscription.ID] = cloneReadinessRecord(out[i])
	}
	return out, nil
}
func (m *MemStore) finishReadinessSubscription(_ context.Context, r readinessSubscriptionRecord, notifications []api.OperationWorkflowReadinessNotification) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	data := m.operationMemoryLocked()
	old, ok := data.readinessSubscriptions[r.Subscription.ID]
	if !ok || old.Lease != r.Lease {
		return nil
	}
	r.Lease = ""
	r.LeaseUntil = time.Time{}
	data.readinessSubscriptions[r.Subscription.ID] = cloneReadinessRecord(r)
	inbox := append(data.readinessInbox[r.Subscription.ID], notifications...)
	if len(inbox) > api.OperationWorkflowReadinessInboxMax {
		inbox = inbox[len(inbox)-api.OperationWorkflowReadinessInboxMax:]
	}
	data.readinessInbox[r.Subscription.ID] = cloneReadinessInbox(api.OperationWorkflowReadinessInboxResponse{Items: inbox}).Items
	return nil
}
