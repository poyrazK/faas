package state

import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) CreateAppLogDrain(_ context.Context, in AppLogDrain) (AppLogDrain, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.createAppLogDrainLocked(in)
}

func (m *MemStore) CreateAppLogDrainIfUnderQuota(_ context.Context, in AppLogDrain, limits api.Limits) (AppLogDrain, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.appLogDrains {
		if existing.AppID == in.AppID && existing.TargetURL == in.TargetURL {
			return AppLogDrain{}, ErrConflict
		}
	}
	app, ok := m.apps[in.AppID]
	if !ok || app.Status == AppDeleted {
		return AppLogDrain{}, ErrNotFound
	}
	appCount, accountCount := 0, 0
	for _, drain := range m.appLogDrains {
		if drain.AppID == in.AppID {
			appCount++
		}
		if drain.AccountID == in.AccountID {
			if owner, exists := m.apps[drain.AppID]; !exists || owner.Status != AppDeleted {
				accountCount++
			}
		}
	}
	if appCount >= limits.LogDrainPerApp {
		return AppLogDrain{}, &AppLogDrainQuotaError{Scope: AppLogDrainQuotaScopeApp, Limit: limits.LogDrainPerApp, Observed: appCount}
	}
	if accountCount >= limits.LogDrainPerAccount {
		return AppLogDrain{}, &AppLogDrainQuotaError{Scope: AppLogDrainQuotaScopeAccount, Limit: limits.LogDrainPerAccount, Observed: accountCount}
	}
	return m.createAppLogDrainLocked(in)
}

func (m *MemStore) createAppLogDrainLocked(in AppLogDrain) (AppLogDrain, error) {
	for _, existing := range m.appLogDrains {
		if existing.AppID == in.AppID && existing.TargetURL == in.TargetURL {
			return AppLogDrain{}, ErrConflict
		}
	}
	if in.ID == "" {
		in.ID = newID()
	}
	if in.CreatedAt.IsZero() {
		in.CreatedAt = time.Now()
	}
	if in.UpdatedAt.IsZero() {
		in.UpdatedAt = in.CreatedAt
	}
	m.appLogDrains[in.ID] = cloneAppLogDrain(in)
	return in, nil
}

func (m *MemStore) AppLogDrainByID(_ context.Context, id string) (AppLogDrain, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	drain, ok := m.appLogDrains[id]
	if !ok {
		return AppLogDrain{}, ErrNotFound
	}
	return cloneAppLogDrain(drain), nil
}

func (m *MemStore) UpdateAppLogDrain(_ context.Context, id string, p UpdateAppLogDrainParams) (AppLogDrain, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	drain, ok := m.appLogDrains[id]
	if !ok {
		return AppLogDrain{}, ErrNotFound
	}
	if p.Kind != nil {
		drain.Kind = *p.Kind
	}
	if p.TargetURL != nil {
		for otherID, other := range m.appLogDrains {
			if otherID != id && other.AppID == drain.AppID && other.TargetURL == *p.TargetURL {
				return AppLogDrain{}, ErrConflict
			}
		}
		drain.TargetURL = *p.TargetURL
	}
	if p.AuthHeaderSealed != nil {
		drain.AuthHeaderSealed = append([]byte(nil), (*p.AuthHeaderSealed)...)
	}
	if p.Enabled != nil {
		drain.Enabled = *p.Enabled
	}
	drain.UpdatedAt = time.Now()
	m.appLogDrains[id] = cloneAppLogDrain(drain)
	return drain, nil
}

func (m *MemStore) DeleteAppLogDrain(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.appLogDrains[id]; !ok {
		return ErrNotFound
	}
	delete(m.appLogDrains, id)
	delete(m.appLogDrainHealth, id)
	for key, sample := range m.appLogDrainAnalytics {
		if sample.DrainID == id {
			delete(m.appLogDrainAnalytics, key)
		}
	}
	return nil
}

func (m *MemStore) ListAppLogDrainsForApp(_ context.Context, appID string) ([]AppLogDrain, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return listAppLogDrains(func(d AppLogDrain) bool { return d.AppID == appID }, m.appLogDrains), nil
}

func (m *MemStore) ListEnabledAppLogDrains(_ context.Context) ([]AppLogDrain, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return listAppLogDrains(func(d AppLogDrain) bool { return d.Enabled }, m.appLogDrains), nil
}

func listAppLogDrains(match func(AppLogDrain) bool, rows map[string]AppLogDrain) []AppLogDrain {
	out := make([]AppLogDrain, 0)
	for _, drain := range rows {
		if match(drain) {
			out = append(out, cloneAppLogDrain(drain))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out
}

func cloneAppLogDrain(in AppLogDrain) AppLogDrain {
	in.AuthHeaderSealed = append([]byte(nil), in.AuthHeaderSealed...)
	return in
}
