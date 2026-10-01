package state

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (m *MemStore) CreateQueueBindingWithConsumer(_ context.Context, in QueueBinding) (QueueBindingConsumerResult, error) {
	if in.ID == "" {
		in.ID = newID()
	}
	return m.mutateQueueBindingConsumer(in.AccountID, in.AppID, in.ID, &in, nil, false)
}

func (m *MemStore) UpdateQueueBindingWithConsumer(_ context.Context, accountID, appID, id string, p UpdateQueueBindingParams) (QueueBindingConsumerResult, error) {
	return m.mutateQueueBindingConsumer(accountID, appID, id, nil, &p, false)
}

func (m *MemStore) DeleteQueueBindingWithConsumer(_ context.Context, accountID, appID, id string) (QueueBindingConsumerResult, error) {
	return m.mutateQueueBindingConsumer(accountID, appID, id, nil, nil, true)
}

func (m *MemStore) mutateQueueBindingConsumer(accountID, appID, id string, create *QueueBinding, patch *UpdateQueueBindingParams, remove bool) (QueueBindingConsumerResult, error) {
	for _, value := range []string{accountID, appID, id} {
		if _, err := uuid.Parse(value); err != nil {
			return QueueBindingConsumerResult{}, ErrInvalidArgument
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[appID]
	if !ok || app.AccountID != accountID || app.Status == AppDeleted {
		return QueueBindingConsumerResult{}, ErrNotFound
	}
	account, ok := m.accounts[accountID]
	if !ok {
		return QueueBindingConsumerResult{}, ErrNotFound
	}
	limits, _ := api.LimitsFor(account.Plan)
	var binding QueueBinding
	if create != nil {
		if _, exists := m.queueBindings[id]; exists {
			return QueueBindingConsumerResult{}, ErrConflict
		}
		binding = applyQueueBindingPatch(*create, UpdateQueueBindingParams{})
		binding.CreatedAt = time.Now().UTC()
	} else {
		binding, ok = m.queueBindings[id]
		if !ok || binding.AppID != appID || binding.AccountID != accountID {
			return QueueBindingConsumerResult{}, ErrNotFound
		}
		if patch != nil {
			binding = applyQueueBindingPatch(binding, *patch)
		}
	}
	if !remove {
		if err := validateQueueBindingConsumer(binding, app.Type, app.WorkloadClass); err != nil {
			return QueueBindingConsumerResult{}, err
		}
		for otherID, other := range m.queueBindings {
			if otherID != id && other.AppID == appID && (other.Name == binding.Name || other.QueueName == binding.QueueName) {
				return QueueBindingConsumerResult{}, ErrConflict
			}
		}
	}
	var owned sqlc.Trigger
	for _, trigger := range m.triggers {
		if trigger.AppID.String() == canonicalMemUUID(appID) && trigger.Kind == "queue" && trigger.Source.Valid && trigger.Source.String == "queue" && ((trigger.QueueBindingID.Valid && trigger.QueueBindingID.Bytes == parseMemUUIDString(id)) || queueConsumerBindingID(trigger.Config) == id) {
			if owned.ID.Valid || !trigger.QueueBindingID.Valid {
				return QueueBindingConsumerResult{}, ErrConflict
			}
			owned = trigger
		}
	}
	result := QueueBindingConsumerResult{Binding: binding}
	var projected sqlc.Trigger
	if !remove && binding.Mode == "push" {
		definition, err := queueConsumerForBinding(binding, limits)
		if err != nil {
			return QueueBindingConsumerResult{}, err
		}
		appCount, accountCount := 0, 0
		for triggerID, trigger := range m.triggers {
			if trigger.AppID.String() == canonicalMemUUID(appID) {
				appCount++
				if triggerID != owned.ID.String() && (trigger.Slug == binding.QueueName ||
					(binding.Enabled && trigger.Kind == "queue" && trigger.Enabled && trigger.Source.Valid && trigger.Source.String == "queue")) {
					return QueueBindingConsumerResult{}, ErrConflict
				}
			}
			if otherApp, ok := m.triggerAppLocked(trigger); ok && otherApp.AccountID == accountID && otherApp.Status != AppDeleted {
				accountCount++
			}
		}
		kind := "updated"
		projected = owned
		if !owned.ID.Valid {
			if appCount >= limits.TriggerLimitPerApp {
				return QueueBindingConsumerResult{}, &TriggerQuotaError{Scope: TriggerQuotaScopeApp, Limit: limits.TriggerLimitPerApp, Observed: appCount}
			}
			if accountCount >= limits.TriggerLimitPerAccount {
				return QueueBindingConsumerResult{}, &TriggerQuotaError{Scope: TriggerQuotaScopeAccount, Limit: limits.TriggerLimitPerAccount, Observed: accountCount}
			}
			kind = "created"
			projected = sqlc.Trigger{ID: pgtype.UUID{Bytes: memNewUUID(), Valid: true}, AccountID: pgtype.UUID{Bytes: parseMemUUIDString(accountID), Valid: true},
				AppID: pgtype.UUID{Bytes: parseMemUUIDString(appID), Valid: true}, QueueBindingID: pgtype.UUID{Bytes: parseMemUUIDString(id), Valid: true}, Kind: "queue", Source: nullableTriggerSource("queue"), BrokerPoisonStrategy: "commit",
				CreatedAt: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}}
		}
		projected.Slug, projected.Enabled, projected.Config = binding.QueueName, binding.Enabled, definition.Config
		projected.BatchSizeMax, projected.BatchWindowMs, projected.MaxAttempts, projected.PayloadMaxBytes = definition.BatchSize, definition.BatchWindow, definition.MaxAttempts, definition.PayloadMax
		projected.UpdatedAt = pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}
		result.Changes = append(result.Changes, QueueConsumerChange{Kind: kind, AppID: appID, TriggerID: projected.ID.String()})
	} else if owned.ID.Valid {
		result.Changes = append(result.Changes, QueueConsumerChange{Kind: "deleted", AppID: appID, TriggerID: owned.ID.String()})
	}
	// Publish only after every validation and quota check. No error path below
	// can leave a binding without its corresponding projection.
	if remove {
		delete(m.queueBindings, id)
	} else {
		binding.UpdatedAt = time.Now().UTC()
		binding.RetryPolicyJSON = append([]byte(nil), binding.RetryPolicyJSON...)
		m.queueBindings[id] = binding
		result.Binding = binding
	}
	if projected.ID.Valid {
		if m.triggers == nil {
			m.triggers = make(map[string]sqlc.Trigger)
		}
		m.triggers[projected.ID.String()] = projected
	} else if owned.ID.Valid {
		delete(m.triggers, owned.ID.String())
		delete(m.triggerWorkBindings, owned.ID.String())
	}
	result.Binding.RetryPolicyJSON = append([]byte(nil), result.Binding.RetryPolicyJSON...)
	return result, nil
}

var _ QueueBindingConsumerStore = (*MemStore)(nil)
