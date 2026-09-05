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
	b.RetryAt = b.CreatedAt
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
	return m.claimObjectBucket(accountID, appID, id, token, next, false)
}

func (m *MemStore) ClaimObjectBucketRecovery(_ context.Context, accountID, appID, id, token, next string) (ObjectBucket, error) {
	return m.claimObjectBucket(accountID, appID, id, token, next, true)
}

func (m *MemStore) claimObjectBucket(accountID, appID, id, token, next string, recovery bool) (ObjectBucket, error) {
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
	if (recovery && b.State != next) || (b.State == next && b.RetryAt.After(time.Now())) {
		return ObjectBucket{}, ErrConflict
	}
	if b.State != next {
		b.AttemptCount = 0
		b.LastErrorCode = ""
	}
	if b.AttemptCount < 30 {
		b.AttemptCount++
	}
	b.State, b.LeaseToken, b.LeaseUntil, b.UpdatedAt = next, token, time.Now().Add(ObjectBucketLeaseDuration), time.Now().UTC()
	b.RetryAt = b.UpdatedAt
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
	b.AttemptCount, b.LastErrorCode, b.RetryAt = 0, "", b.UpdatedAt
	m.objectBuckets[id] = b
	if next == "deleted" {
		for key, grant := range m.objectAccessGrants {
			if grant.BucketID == id {
				delete(m.objectAccessGrants, key)
			}
		}
	}
	return nil
}

func (m *MemStore) RetryObjectBucket(_ context.Context, id, token, code string, delay time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.objectBuckets[id]
	if !ok || token == "" || b.LeaseToken != token || !validObjectBucketRetry(code, delay) || (b.State != "provisioning" && b.State != "deleting") {
		return ErrConflict
	}
	b.LeaseToken, b.LeaseUntil = "", time.Time{}
	b.LastErrorCode, b.UpdatedAt, b.RetryAt = code, time.Now().UTC(), time.Now().UTC().Add(delay)
	m.objectBuckets[id] = b
	return nil
}

func (m *MemStore) DueObjectBuckets(_ context.Context, provision bool, limit int32) ([]ObjectBucket, error) {
	if limit < 1 || limit > 100 {
		return nil, ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := make([]ObjectBucket, 0)
	now := time.Now()
	for _, b := range m.objectBuckets {
		if (b.State == "deleting" || (provision && b.State == "provisioning")) && !b.RetryAt.After(now) && !b.LeaseUntil.After(now) {
			rows = append(rows, b)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].RetryAt.Before(rows[j].RetryAt) || (rows[i].RetryAt.Equal(rows[j].RetryAt) && rows[i].ID < rows[j].ID)
	})
	if len(rows) > int(limit) {
		rows = rows[:limit]
	}
	return rows, nil
}
