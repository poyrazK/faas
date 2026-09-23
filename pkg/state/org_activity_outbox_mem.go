package state

import (
	"context"
	"errors"
	"strings"
	"time"
)

type orgActivityOutboxRow struct {
	OrgActivityOutboxItem
	state       string
	availableAt time.Time
	claimedBy   string
	claimedAt   time.Time
	leaseUntil  time.Time
	deliveredAt time.Time
	createdAt   time.Time
	lastError   string
}

var (
	_ OrgActivityOutboxStore      = (*MemStore)(nil)
	_ OrgActivityEnvMutationStore = (*MemStore)(nil)
)

func orgActivityOutboxKey(entry OrgActivity) string {
	return entry.OrgID.String() + "\x00" + entry.SourceType + "\x00" + entry.SourceID
}

func (m *MemStore) enqueueOrgActivityOutboxLocked(entry OrgActivity) int64 {
	if m.orgActivityOutbox == nil {
		m.orgActivityOutbox = make(map[int64]orgActivityOutboxRow)
	}
	if m.orgActivityOutboxByKey == nil {
		m.orgActivityOutboxByKey = make(map[string]int64)
	}
	key := orgActivityOutboxKey(entry)
	if id, ok := m.orgActivityOutboxByKey[key]; ok {
		return id
	}
	if m.nextOrgActivityOutboxID <= 0 {
		m.nextOrgActivityOutboxID = 1
	}
	now := time.Now().UTC()
	item := OrgActivityOutboxItem{ID: m.nextOrgActivityOutboxID, Activity: cloneOrgActivity(entry)}
	m.orgActivityOutbox[item.ID] = orgActivityOutboxRow{
		OrgActivityOutboxItem: item,
		state:                 orgActivityOutboxStatePending,
		availableAt:           now,
		createdAt:             now,
	}
	m.orgActivityOutboxByKey[key] = item.ID
	m.nextOrgActivityOutboxID++
	return item.ID
}

func (m *MemStore) EnqueueOrgActivityOutbox(_ context.Context, entry OrgActivity) (int64, error) {
	entry, err := normalizeOrgActivity(entry, time.Now())
	if err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.enqueueOrgActivityOutboxLocked(entry), nil
}

func (m *MemStore) UpsertAppEnvInScopeWithActivity(_ context.Context, accountID, appID, scope, key, value string, entry OrgActivity) (int64, error) {
	entry, err := normalizeOrgActivity(entry, time.Now())
	if err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	envKey := envKey{AppID: appID, Scope: scope, Key: key}
	existing, ok := m.envs[envKey]
	now := time.Now().UTC()
	if !ok {
		m.envs[envKey] = AppEnv{AccountID: accountID, AppID: appID, Scope: scope, Key: key, Value: value, CreatedAt: now, UpdatedAt: now}
	} else {
		if existing.AccountID != accountID {
			return 0, ErrNotFound
		}
		existing.Value = value
		existing.UpdatedAt = now
		m.envs[envKey] = existing
	}
	return m.enqueueOrgActivityOutboxLocked(entry), nil
}

func (m *MemStore) DeleteAppEnvInScopeWithActivity(_ context.Context, accountID, appID, scope, key string, entry OrgActivity) (int64, error) {
	entry, err := normalizeOrgActivity(entry, time.Now())
	if err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	envKey := envKey{AppID: appID, Scope: scope, Key: key}
	row, ok := m.envs[envKey]
	if !ok || row.AccountID != accountID {
		return 0, ErrNotFound
	}
	delete(m.envs, envKey)
	return m.enqueueOrgActivityOutboxLocked(entry), nil
}

func (m *MemStore) ClaimOrgActivityOutbox(_ context.Context, consumer string, lease time.Duration) (OrgActivityOutboxItem, error) {
	if strings.TrimSpace(consumer) == "" {
		return OrgActivityOutboxItem{}, errors.New("state: org activity outbox consumer required")
	}
	now := time.Now().UTC()
	lease = orgActivityOutboxLease(lease)
	m.mu.Lock()
	defer m.mu.Unlock()
	var selected *orgActivityOutboxRow
	var selectedID int64
	for id := range m.orgActivityOutbox {
		row := m.orgActivityOutbox[id]
		eligible := row.state == orgActivityOutboxStatePending && !row.availableAt.After(now)
		if row.state == orgActivityOutboxStateProcessing && !row.leaseUntil.After(now) {
			eligible = true
		}
		if !eligible || (selected != nil && id >= selectedID) {
			continue
		}
		copyRow := row
		selected = &copyRow
		selectedID = id
	}
	if selected == nil {
		return OrgActivityOutboxItem{}, ErrNotFound
	}
	selected.state = orgActivityOutboxStateProcessing
	selected.Attempts++
	selected.claimedBy = consumer
	selected.claimedAt = now
	selected.leaseUntil = now.Add(lease)
	m.orgActivityOutbox[selectedID] = *selected
	return cloneOrgActivityOutboxItem(selected.OrgActivityOutboxItem), nil
}

func (m *MemStore) DeliverOrgActivityOutbox(_ context.Context, id int64) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.orgActivityOutbox[id]
	if !ok {
		return false, ErrNotFound
	}
	if row.state == orgActivityOutboxStateDelivered || row.state == orgActivityOutboxStateDeadLetter {
		return false, nil
	}
	entry, err := normalizeOrgActivity(row.Activity, time.Now())
	if err != nil {
		return false, err
	}
	appendOrgActivityLocked(m, entry)
	now := time.Now().UTC()
	row.state = orgActivityOutboxStateDelivered
	row.deliveredAt = now
	row.claimedBy = ""
	row.claimedAt = time.Time{}
	row.leaseUntil = time.Time{}
	row.lastError = ""
	m.orgActivityOutbox[id] = row
	return true, nil
}

func appendOrgActivityLocked(m *MemStore, entry OrgActivity) {
	for _, current := range m.orgActivity {
		if current.OrgID == entry.OrgID && current.SourceType == entry.SourceType && current.SourceID == entry.SourceID {
			return
		}
	}
	entry.ID = int64(len(m.orgActivity) + 1)
	m.orgActivity = append(m.orgActivity, cloneOrgActivity(entry))
}

func (m *MemStore) FailOrgActivityOutbox(_ context.Context, id int64, cause error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.orgActivityOutbox[id]
	if !ok {
		return ErrNotFound
	}
	if row.state == orgActivityOutboxStateDelivered || row.state == orgActivityOutboxStateDeadLetter {
		return nil
	}
	row.lastError = orgActivityOutboxFailureMessage(cause)
	row.claimedBy = ""
	row.claimedAt = time.Time{}
	row.leaseUntil = time.Time{}
	if row.Attempts >= OrgActivityOutboxMaxAttempts {
		row.state = orgActivityOutboxStateDeadLetter
		row.availableAt = time.Time{}
	} else {
		row.state = orgActivityOutboxStatePending
		row.availableAt = time.Now().UTC().Add(orgActivityOutboxRetryDelay(row.Attempts))
	}
	m.orgActivityOutbox[id] = row
	return nil
}

func (m *MemStore) PruneOrgActivityOutbox(_ context.Context, before time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var removed int64
	for id, row := range m.orgActivityOutbox {
		if row.state != orgActivityOutboxStateDelivered || !row.deliveredAt.Before(before) {
			continue
		}
		delete(m.orgActivityOutbox, id)
		delete(m.orgActivityOutboxByKey, orgActivityOutboxKey(row.Activity))
		removed++
	}
	return removed, nil
}

func cloneOrgActivityOutboxItem(item OrgActivityOutboxItem) OrgActivityOutboxItem {
	item.Activity = cloneOrgActivity(item.Activity)
	return item
}
