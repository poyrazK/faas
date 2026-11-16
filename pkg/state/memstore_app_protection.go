package state

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// appProtectionLease is the lease state AppProtection does not carry.
type appProtectionLease struct {
	until time.Time
}

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
	p.NextReconcileAt = now
	if m.appProtection == nil {
		m.appProtection = map[string]AppProtection{}
	}
	m.appProtection[appID] = p
	return p, nil
}

// RecordAppProtectionObject implements AppProtectionStore.
func (m *MemStore) RecordAppProtectionObject(_ context.Context, obj AppProtectionObject) error {
	obj, ok := normalizeAppProtectionObject(obj)
	if !ok {
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
	if existing, ok := byItem[obj.Item]; ok && existing.Status == AppProtectionObjectCreated {
		return ErrConflict
	}
	obj.Written = append(json.RawMessage(nil), obj.Written...)
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

// ClaimAppProtectionReconciles implements AppProtectionStore.
func (m *MemStore) ClaimAppProtectionReconciles(_ context.Context, limit int, lease time.Duration) ([]AppProtection, error) {
	if limit <= 0 {
		return nil, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	var due []AppProtection
	for id, p := range m.appProtection {
		if p.State != AppProtectionOn || p.NextReconcileAt.After(now) {
			continue
		}
		if l, ok := m.appProtectionLeases[id]; ok && l.until.After(now) {
			continue
		}
		due = append(due, p)
	}
	sort.Slice(due, func(i, j int) bool {
		if !due[i].NextReconcileAt.Equal(due[j].NextReconcileAt) {
			return due[i].NextReconcileAt.Before(due[j].NextReconcileAt)
		}
		return due[i].AppID < due[j].AppID
	})
	if len(due) > limit {
		due = due[:limit]
	}
	if m.appProtectionLeases == nil {
		m.appProtectionLeases = map[string]appProtectionLease{}
	}
	for _, p := range due {
		m.appProtectionLeases[p.AppID] = appProtectionLease{until: now.Add(lease)}
	}
	return due, nil
}

// ClaimProtectionHealthProbes implements AppProtectionStore.
func (m *MemStore) ClaimProtectionHealthProbes(_ context.Context, limit int) ([]ProtectionHealthProbe, error) {
	if limit <= 0 {
		return nil, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	type due struct {
		probe ProtectionHealthProbe
		last  time.Time
	}
	var candidates []due
	for appID, p := range m.appProtection {
		if p.State != AppProtectionOn {
			continue
		}
		rec := m.appProtectionObjects[appID][AppProtectionHealthCheck]
		if rec.Status != AppProtectionObjectCreated {
			continue
		}
		app, ok := m.apps[appID]
		if !ok || app.Status == AppDeleted || api.NormalizeAppVisibility(app.Visibility) == api.AppVisibilityInternal {
			continue
		}
		var check *SyntheticCheck
		for i := range m.syntheticChecks[appID] {
			if m.syntheticChecks[appID][i].ID == rec.ObjectID {
				check = &m.syntheticChecks[appID][i]
			}
		}
		if check == nil || !check.Enabled {
			continue
		}
		last := m.appProtectionHealthProbeAt[appID]
		if !last.IsZero() && now.Sub(last) < time.Duration(check.IntervalSeconds)*time.Second {
			continue
		}
		candidates = append(candidates, due{ProtectionHealthProbe{AppID: appID, AccountID: p.AccountID, CheckID: check.ID}, last})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if !candidates[i].last.Equal(candidates[j].last) {
			return candidates[i].last.Before(candidates[j].last)
		}
		return candidates[i].probe.AppID < candidates[j].probe.AppID
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	if m.appProtectionHealthProbeAt == nil {
		m.appProtectionHealthProbeAt = map[string]time.Time{}
	}
	out := make([]ProtectionHealthProbe, 0, len(candidates))
	for _, c := range candidates {
		m.appProtectionHealthProbeAt[c.probe.AppID] = now
		out = append(out, c.probe)
	}
	return out, nil
}

// FinishAppProtectionReconcile implements AppProtectionStore.
func (m *MemStore) FinishAppProtectionReconcile(_ context.Context, appID string, next time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.appProtection[appID]
	if !ok {
		return ErrNotFound
	}
	p.NextReconcileAt = next.UTC()
	m.appProtection[appID] = p
	delete(m.appProtectionLeases, appID)
	return nil
}
