package state

import (
	"context"
	"sort"
	"time"
)

var _ ObjectBucketStore = (*MemStore)(nil)

func (m *MemStore) ReserveObjectBucket(_ context.Context, b ObjectBucket, limit int) (ObjectBucket, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[b.AppID]
	if !ok || app.AccountID != b.AccountID || app.Status == AppDeleted {
		return ObjectBucket{}, ErrNotFound
	}
	count := 0
	for _, row := range m.objectBuckets {
		if row.AppID != b.AppID || row.AccountID != b.AccountID || row.State == "deleted" {
			continue
		}
		if row.Name == b.Name && row.Scope == b.Scope {
			return row, nil
		}
		count++
	}
	if count >= limit {
		return ObjectBucket{}, ErrConflict
	}
	if m.objectBuckets == nil {
		m.objectBuckets = map[string]ObjectBucket{}
	}
	b.State = "provisioning"
	b.CreatedAt = time.Now().UTC()
	b.UpdatedAt = b.CreatedAt
	m.objectBuckets[b.ID] = b
	return b, nil
}

func (m *MemStore) ListObjectBuckets(_ context.Context, accountID, appID string) ([]ObjectBucket, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := make([]ObjectBucket, 0)
	for _, b := range m.objectBuckets {
		if b.AccountID == accountID && b.AppID == appID && b.State != "deleted" {
			rows = append(rows, b)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].CreatedAt.Before(rows[j].CreatedAt) || rows[i].CreatedAt.Equal(rows[j].CreatedAt) && rows[i].ID < rows[j].ID
	})
	return rows, nil
}

func (m *MemStore) GetObjectBucket(_ context.Context, accountID, appID, id string) (ObjectBucket, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.objectBuckets[id]
	if !ok || b.AccountID != accountID || b.AppID != appID || b.State == "deleted" {
		return ObjectBucket{}, ErrNotFound
	}
	return b, nil
}

func (m *MemStore) ClaimObjectBucket(_ context.Context, accountID, appID, id, token, next string) (ObjectBucket, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.objectBuckets[id]
	if !ok || b.AccountID != accountID || b.AppID != appID || b.State == "deleted" {
		return ObjectBucket{}, ErrNotFound
	}
	if b.LeaseUntil.After(time.Now()) || (next == "provisioning" && b.State != "provisioning") {
		return ObjectBucket{}, ErrConflict
	}
	if token == "" || (next != "provisioning" && next != "deleting") {
		return ObjectBucket{}, ErrConflict
	}
	b.State, b.LeaseToken, b.LeaseUntil, b.UpdatedAt = next, token, time.Now().Add(ObjectBucketLeaseDuration), time.Now().UTC()
	m.objectBuckets[id] = b
	return b, nil
}

func (m *MemStore) FinishObjectBucket(_ context.Context, id, token, next string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.objectBuckets[id]
	if !ok || b.LeaseToken != token || token == "" {
		return ErrConflict
	}
	if next != "provisioning" && next != "ready" && next != "deleting" && next != "deleted" {
		return ErrConflict
	}
	b.State, b.LeaseToken, b.LeaseUntil, b.UpdatedAt = next, "", time.Time{}, time.Now().UTC()
	m.objectBuckets[id] = b
	return nil
}
