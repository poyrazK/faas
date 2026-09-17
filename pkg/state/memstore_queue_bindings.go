package state

import (
	"context"
	"sort"
	"time"
)

func (m *MemStore) CreateQueueBinding(_ context.Context, in QueueBinding) (QueueBinding, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
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

func (m *MemStore) QueueBindingByID(_ context.Context, accountID, appID, id string) (QueueBinding, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.queueBindings[id]
	if !ok || b.AccountID != accountID || b.AppID != appID {
		return QueueBinding{}, ErrNotFound
	}
	b.RetryPolicyJSON = append([]byte(nil), b.RetryPolicyJSON...)
	return b, nil
}

func (m *MemStore) ListQueueBindingsForApp(_ context.Context, accountID, appID string) ([]QueueBinding, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []QueueBinding
	for _, b := range m.queueBindings {
		if b.AccountID == accountID && b.AppID == appID {
			b.RetryPolicyJSON = append([]byte(nil), b.RetryPolicyJSON...)
			out = append(out, b)
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
	if !ok || b.AccountID != accountID || b.AppID != appID {
		return QueueBinding{}, ErrNotFound
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

func (m *MemStore) DeleteQueueBinding(_ context.Context, accountID, appID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.queueBindings[id]
	if !ok || b.AccountID != accountID || b.AppID != appID {
		return ErrNotFound
	}
	delete(m.queueBindings, id)
	return nil
}
