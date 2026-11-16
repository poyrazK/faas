package state

import (
	"context"
	"encoding/json"
	"sort"
	"time"
)

// GetAppProtection implements AppProtectionStore.
func (m *MemStore) GetAppProtection(_ context.Context, appID string) (AppProtection, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.appProtection[appID]
	return p, ok, nil
}

// SetAppProtection implements AppProtectionStore.
func (m *MemStore) SetAppProtection(_ context.Context, appID, accountID, state string) (AppProtection, error) {
	if state != AppProtectionOn && state != AppProtectionOff {
		return AppProtection{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.apps[appID]; !ok {
		return AppProtection{}, ErrNotFound
	}
	if _, ok := m.accounts[accountID]; !ok {
		return AppProtection{}, ErrNotFound
	}
	now := time.Now().UTC()
	p, ok := m.appProtection[appID]
	if !ok {
		p = AppProtection{AppID: appID, AccountID: accountID, CreatedAt: now}
	}
	p.State = state
	p.UpdatedAt = now
	if m.appProtection == nil {
		m.appProtection = map[string]AppProtection{}
	}
	m.appProtection[appID] = p
	return p, nil
}

// RecordAppProtectionObject implements AppProtectionStore.
func (m *MemStore) RecordAppProtectionObject(_ context.Context, obj AppProtectionObject) error {
	if !validAppProtectionObject(obj) || !json.Valid(appProtectionWritten(obj.Written)) {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.appProtection[obj.AppID]; !ok {
		return ErrNotFound
	}
	if m.appProtectionObjects == nil {
		m.appProtectionObjects = map[string]map[AppProtectionItem]AppProtectionObject{}
	}
	byItem := m.appProtectionObjects[obj.AppID]
	if byItem == nil {
		byItem = map[AppProtectionItem]AppProtectionObject{}
		m.appProtectionObjects[obj.AppID] = byItem
	}
	obj.Written = append(json.RawMessage(nil), appProtectionWritten(obj.Written)...)
	obj.CreatedAt = time.Now().UTC()
	byItem[obj.Item] = obj
	return nil
}

// ListAppProtectionObjects implements AppProtectionStore.
func (m *MemStore) ListAppProtectionObjects(_ context.Context, appID string) ([]AppProtectionObject, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]AppProtectionObject, 0, len(m.appProtectionObjects[appID]))
	for _, obj := range m.appProtectionObjects[appID] {
		obj.Written = append(json.RawMessage(nil), obj.Written...)
		out = append(out, obj)
	}
	sort.Slice(out, func(i, j int) bool {
		return appProtectionItemRank(out[i].Item) < appProtectionItemRank(out[j].Item)
	})
	return out, nil
}
