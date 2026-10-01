package state

import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (m *MemStore) CreateQueueBinding(_ context.Context, in QueueBinding) (QueueBinding, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if in.RetiredAt != nil {
		return QueueBinding{}, ErrInvalidArgument
	}
	app, ok := m.apps[in.AppID]
	if !ok || app.Status == AppDeleted || app.AccountID != in.AccountID {
		return QueueBinding{}, ErrNotFound
	}
	for _, existing := range m.queueBindings {
		if existing.AppID == in.AppID && (existing.Name == in.Name || existing.QueueName == in.QueueName) {
			return QueueBinding{}, ErrConflict
		}
	}
	if in.ID == "" {
		in.ID = newID()
	}
	if _, exists := m.queueBindings[in.ID]; exists {
		return QueueBinding{}, ErrConflict
	}
	if in.CreatedAt.IsZero() {
		in.CreatedAt = time.Now().UTC()
	}
	in.UpdatedAt = in.CreatedAt
	if in.RetryPolicyJSON == nil {
		in.RetryPolicyJSON = []byte("{}")
	}
	in.RetryPolicyJSON = append([]byte(nil), in.RetryPolicyJSON...)
	m.queueBindings[in.ID] = in
	return in, nil
}

func (m *MemStore) QueueBindingByID(ctx context.Context, accountID, appID, id string) (QueueBinding, error) {
	row, err := m.QueueBindingHistoryByID(ctx, accountID, appID, id)
	if err == nil && row.RetiredAt != nil {
		return QueueBinding{}, ErrNotFound
	}
	return row, err
}

func (m *MemStore) QueueBindingHistoryByID(_ context.Context, accountID, appID, id string) (QueueBinding, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.queueBindings[id]
	if !ok || b.AccountID != accountID || b.AppID != appID {
		return QueueBinding{}, ErrNotFound
	}
	return cloneQueueBinding(b), nil
}

func (m *MemStore) ListQueueBindingsForApp(ctx context.Context, accountID, appID string) ([]QueueBinding, error) {
	rows, err := m.ListQueueBindingHistoryForApp(ctx, accountID, appID)
	if err != nil {
		return nil, err
	}
	out := make([]QueueBinding, 0, len(rows))
	for _, row := range rows {
		if row.RetiredAt == nil {
			out = append(out, row)
		}
	}
	return out, nil
}

func (m *MemStore) ListQueueBindingHistoryForApp(_ context.Context, accountID, appID string) ([]QueueBinding, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []QueueBinding
	for _, b := range m.queueBindings {
		if b.AccountID == accountID && b.AppID == appID {
			out = append(out, cloneQueueBinding(b))
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

func (m *MemStore) UpdateQueueBinding(_ context.Context, accountID, appID, id string, p UpdateQueueBindingParams) (QueueBinding, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.queueBindings[id]
	if !ok || b.AccountID != accountID || b.AppID != appID || b.RetiredAt != nil {
		return QueueBinding{}, ErrNotFound
	}
	for _, trigger := range m.triggers {
		if trigger.AppID.String() == canonicalMemUUID(appID) &&
			(trigger.QueueBindingID.Valid && trigger.QueueBindingID.String() == canonicalMemUUID(id) || queueConsumerBindingID(trigger.Config) == id) {
			return QueueBinding{}, ErrConflict
		}
	}
	if p.QueueName != nil {
		b.QueueName = *p.QueueName
	}
	if p.Mode != nil {
		b.Mode = *p.Mode
	}
	if p.WorkloadClass != nil {
		b.WorkloadClass = *p.WorkloadClass
	}
	if p.Enabled != nil {
		b.Enabled = *p.Enabled
	}
	if p.MaxConcurrency != nil {
		b.MaxConcurrency = *p.MaxConcurrency
	}
	if p.RetryPolicyJSON != nil {
		b.RetryPolicyJSON = append([]byte(nil), (*p.RetryPolicyJSON)...)
	}
	for otherID, other := range m.queueBindings {
		if otherID == id || other.AppID != appID {
			continue
		}
		if other.Name == b.Name || other.QueueName == b.QueueName {
			return QueueBinding{}, ErrConflict
		}
	}
	b.UpdatedAt = time.Now().UTC()
	m.queueBindings[id] = b
	b.RetryPolicyJSON = append([]byte(nil), b.RetryPolicyJSON...)
	return b, nil
}

func (m *MemStore) DeleteQueueBinding(ctx context.Context, accountID, appID, id string) error {
	_, err := m.DeleteQueueBindingWithConsumer(ctx, accountID, appID, id)
	return err
}

func cloneQueueBinding(row QueueBinding) QueueBinding {
	row.RetryPolicyJSON = append([]byte(nil), row.RetryPolicyJSON...)
	if row.RetiredAt != nil {
		retired := *row.RetiredAt
		row.RetiredAt = &retired
	}
	return row
}

func (m *MemStore) triggerConsumesQuotaLocked(trigger sqlc.Trigger) bool {
	if !trigger.QueueBindingID.Valid {
		return true
	}
	for _, binding := range m.queueBindings {
		if canonicalMemUUID(binding.ID) == trigger.QueueBindingID.String() {
			return binding.RetiredAt == nil && binding.Mode == "push"
		}
	}
	// Corrupt or missing ownership must never free an admission slot.
	return true
}

func (m *MemStore) queueBindingRetiredLocked(inv Invocation) bool {
	if inv.Source != InvocationQueue {
		return false
	}
	for _, binding := range m.queueBindings {
		if binding.AppID == inv.AppID && binding.QueueName == inv.QueueName && binding.RetiredAt != nil {
			return true
		}
	}
	return false
}

var _ QueueBindingHistoryStore = (*MemStore)(nil)

func (m *MemStore) queueConsumerCanClaimLocked(triggerID string) bool {
	trigger, ok := m.triggers[triggerID]
	if !ok || !trigger.QueueBindingID.Valid {
		return true
	}
	for _, binding := range m.queueBindings {
		if canonicalMemUUID(binding.ID) == trigger.QueueBindingID.String() {
			return binding.Enabled && binding.RetiredAt == nil && binding.Mode == "push"
		}
	}
	return false
}
