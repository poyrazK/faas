package state

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/google/uuid"
)

var _ DashboardStore = (*MemStore)(nil)

func cloneDashboard(d Dashboard) Dashboard {
	d.Widgets = append(json.RawMessage(nil), d.Widgets...)
	return d
}

// CreateDashboard mirrors the PostgreSQL insert, unique name and cap.
func (m *MemStore) CreateDashboard(_ context.Context, d Dashboard, limit int) (Dashboard, error) {
	if err := validateDashboard(d); err != nil {
		return Dashboard{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.dashboards == nil {
		m.dashboards = map[string]Dashboard{}
	}
	count := 0
	for _, existing := range m.dashboards {
		if existing.AccountID != d.AccountID {
			continue
		}
		if existing.Name == d.Name {
			return Dashboard{}, ErrConflict
		}
		count++
	}
	if count >= limit {
		return Dashboard{}, &DashboardQuotaError{Limit: limit, Observed: count}
	}
	now := time.Now().UTC()
	d.ID, d.CreatedAt, d.UpdatedAt = uuid.NewString(), now, now
	m.dashboards[d.ID] = cloneDashboard(d)
	return cloneDashboard(d), nil
}

// ReplaceDashboard mirrors the PostgreSQL update.
func (m *MemStore) ReplaceDashboard(_ context.Context, d Dashboard) (Dashboard, error) {
	if err := validateDashboard(d); err != nil {
		return Dashboard{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	existing, ok := m.dashboards[d.ID]
	if !ok || existing.AccountID != d.AccountID {
		return Dashboard{}, ErrNotFound
	}
	for id, other := range m.dashboards {
		if id != d.ID && other.AccountID == d.AccountID && other.Name == d.Name {
			return Dashboard{}, ErrConflict
		}
	}
	existing.Name, existing.Widgets, existing.UpdatedAt = d.Name, d.Widgets, time.Now().UTC()
	m.dashboards[d.ID] = cloneDashboard(existing)
	return cloneDashboard(existing), nil
}

// ListDashboards mirrors the PostgreSQL read, ordered by name.
func (m *MemStore) ListDashboards(_ context.Context, accountID string) ([]Dashboard, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Dashboard
	for _, d := range m.dashboards {
		if d.AccountID == accountID {
			out = append(out, cloneDashboard(d))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// DashboardByID mirrors the PostgreSQL read.
func (m *MemStore) DashboardByID(_ context.Context, accountID, id string) (Dashboard, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.dashboards[id]
	if !ok || d.AccountID != accountID {
		return Dashboard{}, ErrNotFound
	}
	return cloneDashboard(d), nil
}

// DeleteDashboard mirrors the PostgreSQL delete.
func (m *MemStore) DeleteDashboard(_ context.Context, accountID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.dashboards[id]
	if !ok || d.AccountID != accountID {
		return ErrNotFound
	}
	delete(m.dashboards, id)
	return nil
}
