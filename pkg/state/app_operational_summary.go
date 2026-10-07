package state

import (
	"context"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

// Summary reads are scoped before limiting. Fleet worker batches cannot serve
// this surface: filtering one would silently omit another account's app work.
type AppOperationalStore interface {
	ListAppPendingRollbacks(context.Context, string, string) ([]api.RollbackOperation, error)
	GetAppOpenMonitorIncident(context.Context, string, string) (*api.AppOperationalIncident, error)
}

// The memory store has no durable notification outbox. Absence of this optional
// reader must be surfaced as unavailable rather than an empty restart queue.
type AppOperationalRestartStore interface {
	ListAppPendingRestarts(context.Context, string, string) ([]api.RuntimeConfigRestartStatusResponse, error)
}

func (m *MemStore) ListAppPendingRollbacks(_ context.Context, accountID, appID string) ([]api.RollbackOperation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[appID]
	if !ok || app.AccountID != accountID || app.Status == AppDeleted {
		return nil, ErrNotFound
	}
	out := []api.RollbackOperation{}
	for _, operation := range m.checkedRollbacks {
		if operation.AppID == appID && !rollbackTerminal(operation) {
			out = append(out, cloneRollback(operation))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	if len(out) > api.AppOperationalRecoveryLimit+1 {
		out = out[:api.AppOperationalRecoveryLimit+1]
	}
	return out, nil
}

func (m *MemStore) GetAppOpenMonitorIncident(_ context.Context, accountID, appID string) (*api.AppOperationalIncident, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.monitorIncidentAccessLocked(accountID, appID); err != nil {
		return nil, err
	}
	for _, incident := range m.routeMonitorIncidents[appID] {
		if incident.Status == "open" {
			return &api.AppOperationalIncident{ID: incident.ID, DeploymentID: incident.DeploymentID, OpenedAt: incident.OpenedAt}, nil
		}
	}
	return nil, nil
}
