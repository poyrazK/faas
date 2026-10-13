package state

import (
	"context"
	"encoding/json"
	"slices"
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
func (m *MemStore) finishReadinessSubscription(ctx context.Context, r readinessSubscriptionRecord, notifications []api.OperationWorkflowReadinessNotification, watchUpdates []readinessWatchUpdate) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	data := m.operationMemoryLocked()
	old, ok := data.readinessSubscriptions[r.Subscription.ID]
	if !ok || old.Lease != r.Lease {
		return nil
	}
	for _, u := range watchUpdates {
		if u.Playbook != nil {
			identity := playbookEvidenceWatchIdentity(r, u.Playbook.Watch)
			var progress *api.OperationWorkflowPlaybookProgress
			if row, found := data.playbookProgress[u.Playbook.Watch.ProgressID]; found && row.key() == identity.key() {
				p := row.Progress
				progress = &p
			}
			published, accepted := applyPlaybookEvidenceWatchUpdate(&r, *u.Playbook, progress)
			notifications = append(notifications, published...)
			if accepted {
				for _, watch := range r.Subscription.PlaybookEvidenceWatches {
					if watch.ID != u.Playbook.Watch.ID {
						continue
					}
					for _, ref := range append(slices.Clone(watch.StepAssignments), watch.ReviewAssignments...) {
						for j, n := range data.readinessInbox[r.Subscription.ID] {
							if updated, ok := syncPlaybookStepAssignment(n, *progress, ref); ok {
								data.readinessInbox[r.Subscription.ID][j] = captureObligationReceipts(ctx, r.Subscription.ID, r.Subscription.Scope, n, updated, "")
								break
							}
						}
					}
					for j, n := range data.readinessInbox[r.Subscription.ID] {
						if updated, ok := syncPlaybookOutcomeEscalation(n, watch); ok {
							data.readinessInbox[r.Subscription.ID][j] = captureObligationReceipts(ctx, r.Subscription.ID, r.Subscription.Scope, n, updated, "")
							break
						}
					}
					break
				}
			}
			continue
		}
		for i, n := range data.readinessInbox[r.Subscription.ID] {
			if n.Sequence != u.Sequence {
				continue
			}
			prior := cloneReadinessInbox(api.OperationWorkflowReadinessInboxResponse{Items: []api.OperationWorkflowReadinessNotification{n}}).Items[0]
			refreshed := false
			if u.ResponseWatchID != "" && n.PlaybookStep != nil && n.PlaybookEvidence != nil {
				identity := playbookStepResponseIdentity(r, n)
				if row, exists := data.playbookProgress[n.PlaybookStep.ProgressID]; exists && row.key() == identity.key() {
					n, refreshed = refreshPlaybookStepResponse(n, []api.OperationWorkflowPlaybookProgress{row.Progress})
				}
			}
			updated, published, ok := applyReadinessWatchUpdate(ctx, &r, n, u)
			if ok || refreshed {
				data.readinessInbox[r.Subscription.ID][i] = captureObligationReceipts(ctx, r.Subscription.ID, r.Subscription.Scope, prior, updated, "")
				if n.BusinessOutcomeWatch != nil && n.BusinessOutcomeWatch.Escalation != nil {
					for j, target := range data.readinessInbox[r.Subscription.ID] {
						if synced, ok := syncReadinessOutcomeEscalation(target, updated); ok {
							data.readinessInbox[r.Subscription.ID][j] = captureObligationReceipts(ctx, r.Subscription.ID, r.Subscription.Scope, target, synced, "")
							break
						}
					}
				}
				notifications = append(notifications, published...)
			}
			break
		}
	}
	r.Lease = ""
	r.LeaseUntil = time.Time{}
	data.readinessSubscriptions[r.Subscription.ID] = cloneReadinessRecord(r)
	for i, n := range notifications {
		notifications[i] = captureObligationReceipts(ctx, r.Subscription.ID, r.Subscription.Scope, api.OperationWorkflowReadinessNotification{}, n, "")
	}
	inbox := append(data.readinessInbox[r.Subscription.ID], notifications...)
	if len(inbox) > api.OperationWorkflowReadinessInboxMax {
		inbox = inbox[len(inbox)-api.OperationWorkflowReadinessInboxMax:]
	}
	data.readinessInbox[r.Subscription.ID] = cloneReadinessInbox(api.OperationWorkflowReadinessInboxResponse{Items: inbox}).Items
	return nil
}

func (m *MemStore) updateReadinessNotification(ctx context.Context, account, tenant, app, scope, id string, sequence int64, actor string, req readinessCoordinationUpdate, operator bool) (api.OperationWorkflowReadinessNotification, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out api.OperationWorkflowReadinessNotification
	data := m.operationMemoryLocked()
	r, ok := data.readinessSubscriptions[id]
	if !ok || !readinessRecordMatches(r, account, tenant, app, scope, operator) {
		return out, ErrNotFound
	}
	for i, n := range data.readinessInbox[id] {
		if n.Sequence == sequence {
			out, err := applyReadinessCoordination(ctx, cloneReadinessInbox(api.OperationWorkflowReadinessInboxResponse{Items: []api.OperationWorkflowReadinessNotification{n}}).Items[0], actor, req)
			if err != nil {
				return out, err
			}
			out = captureObligationReceipts(ctx, id, scope, n, out, actor)
			out = cloneReadinessInbox(api.OperationWorkflowReadinessInboxResponse{Items: []api.OperationWorkflowReadinessNotification{out}}).Items[0]
			data.readinessInbox[id][i] = out
			return cloneReadinessInbox(api.OperationWorkflowReadinessInboxResponse{Items: []api.OperationWorkflowReadinessNotification{out}}).Items[0], nil
		}
	}
	return out, ErrNotFound
}

func (m *MemStore) readReadinessFollowUps(ctx context.Context, account, tenant, app, scope string, operator bool) ([]api.OperationWorkflowReadinessFollowUp, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []api.OperationWorkflowReadinessFollowUp{}
	data := m.operationMemoryLocked()
	selectedTenant := tenant
	if operator {
		selectedTenant = ""
	}
	for id, r := range data.readinessSubscriptions {
		if !readinessRecordMatches(r, account, selectedTenant, app, scope, operator) {
			continue
		}
		for _, n := range data.readinessInbox[id] {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if n.Coordination != nil && (n.Coordination.FollowUpAt != nil || hasPendingFulfillmentReviews(n.Coordination)) {
				out = append(out, readinessFollowUpProjection(r.Subscription, r.Tenant, n))
			}
		}
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(raw, &out)
	return out, err
}

func (m *MemStore) PublishDueWorkflowReadinessReminders(ctx context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data := m.operationMemoryLocked()
	now := time.Now().UTC().Truncate(time.Microsecond)
	type candidate struct {
		id       string
		index    int
		due      time.Time
		sequence int64
	}
	rows := []candidate{}
	for id, inbox := range data.readinessInbox {
		for i, n := range inbox {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
			if readinessReminderDue(n, now) {
				rows = append(rows, candidate{id: id, index: i, due: *n.Coordination.FollowUpAt, sequence: n.Sequence})
			}
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if !a.due.Equal(b.due) {
			return a.due.Before(b.due)
		}
		if a.id != b.id {
			return a.id < b.id
		}
		return a.sequence < b.sequence
	})
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	rows = rows[:min(len(rows), api.OperationWorkflowReadinessReminderBatchMax)]
	for _, row := range rows {
		n := data.readinessInbox[row.id][row.index]
		n.Reminder = readinessDueReminder(n, now)
		data.readinessInbox[row.id][row.index] = n
	}
	return len(rows), nil
}
