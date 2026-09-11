package state

import (
	"context"
	"sort"
	"time"
)

func (m *MemStore) UpsertAppLogDrainHealth(_ context.Context, health AppLogDrainHealth) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.appLogDrains[health.DrainID]; !ok {
		return ErrNotFound
	}
	if health.Status == "" {
		health.Status = "unknown"
	}
	if health.UpdatedAt.IsZero() {
		health.UpdatedAt = time.Now().UTC()
	}
	m.appLogDrainHealth[health.DrainID] = health
	return nil
}

func (m *MemStore) AppLogDrainHealthByDrainID(_ context.Context, drainID string) (AppLogDrainHealth, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	health, ok := m.appLogDrainHealth[drainID]
	if !ok {
		return AppLogDrainHealth{}, ErrNotFound
	}
	return health, nil
}

func (m *MemStore) ListAppLogDrainHealthForApp(_ context.Context, appID string) ([]AppLogDrainHealth, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]AppLogDrainHealth, 0)
	for drainID, health := range m.appLogDrainHealth {
		drain, ok := m.appLogDrains[drainID]
		if ok && drain.AppID == appID {
			out = append(out, health)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DrainID < out[j].DrainID })
	return out, nil
}
