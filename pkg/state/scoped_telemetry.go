package state

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type ScopedOperationalStore interface {
	ListScopedPendingRollbacks(context.Context, string, string, string) ([]api.RollbackOperation, error)
	GetScopedOpenMonitorIncident(context.Context, string, string, string) (*api.AppOperationalIncident, error)
}
type ScopedRestartStore interface {
	ListScopedPendingRestarts(context.Context, string, string, string) ([]api.RuntimeConfigRestartStatusResponse, error)
}
type ScopedRouteStore interface {
	ListScopedObservedRoutes(context.Context, string, string, string, time.Time, time.Time) ([]string, error)
}
type WorkerRolloutObservationStore interface {
	ReadWorkerRolloutObservation(context.Context, string, string, string) (api.WorkerRolloutObservation, error)
}

func (s *PgStore) ListScopedObservedRoutes(ctx context.Context, account, app, scope string, since, until time.Time) ([]string, error) {
	if err := s.operationalAppOwner(ctx, account, app); err != nil {
		return nil, err
	}
	return sqlc.New().ListScopedObservedRoutes(ctx, s.pool, sqlc.ListScopedObservedRoutesParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), Scope: scope, Since: pgtype.Timestamptz{Time: since, Valid: true}, Until: pgtype.Timestamptz{Time: until, Valid: true}, RowLimit: api.RouteMetricsPerAppCap + 1})
}
func (s *PgStore) ListScopedPendingRollbacks(ctx context.Context, account, app, scope string) ([]api.RollbackOperation, error) {
	if err := s.operationalAppOwner(ctx, account, app); err != nil {
		return nil, err
	}
	rows, err := sqlc.New().ListScopedPendingRollbacks(ctx, s.pool, sqlc.ListScopedPendingRollbacksParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), Scope: scope, RowLimit: api.AppOperationalRecoveryLimit + 1})
	if err != nil {
		return nil, err
	}
	out := make([]api.RollbackOperation, 0, len(rows))
	for _, raw := range rows {
		r, err := decodeCheckedRollback(raw, nil)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}
func (s *PgStore) GetScopedOpenMonitorIncident(ctx context.Context, account, app, scope string) (*api.AppOperationalIncident, error) {
	if err := s.operationalAppOwner(ctx, account, app); err != nil {
		return nil, err
	}
	row, err := sqlc.New().ScopedOpenMonitorIncident(ctx, s.pool, sqlc.ScopedOpenMonitorIncidentParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), Scope: scope})
	if errors.Is(mapErr(err), ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &api.AppOperationalIncident{ID: pgUUIDString(row.ID), DeploymentID: pgUUIDString(row.DeploymentID), OpenedAt: row.OpenedAt.Time}, nil
}
func (s *PgStore) ListScopedPendingRestarts(ctx context.Context, account, app, scope string) ([]api.RuntimeConfigRestartStatusResponse, error) {
	if err := s.operationalAppOwner(ctx, account, app); err != nil {
		return nil, err
	}
	rows, err := sqlc.New().ListScopedPendingRestarts(ctx, s.pool, sqlc.ListScopedPendingRestartsParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), Scope: scope, RowLimit: api.AppOperationalRecoveryLimit + 1})
	if err != nil {
		return nil, err
	}
	out := make([]api.RuntimeConfigRestartStatusResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, api.RuntimeConfigRestartStatusResponse{WakeID: row.WakeID, Status: row.Status, Attempts: int(row.Attempts), FailureReason: row.FailureReason, RequestedAt: row.RequestedAt.Time})
	}
	return out, nil
}
func (m *MemStore) ListScopedPendingRollbacks(_ context.Context, account, appID, scope string) ([]api.RollbackOperation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[appID]
	if !ok || app.AccountID != account || app.Status == AppDeleted {
		return nil, ErrNotFound
	}
	rows := []api.RollbackOperation{}
	for _, r := range m.checkedRollbacks {
		if r.AppID == appID && normalizedDeploymentScope(r.Scope) == scope && !rollbackTerminal(r) {
			rows = append(rows, cloneRollback(r))
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].UpdatedAt.Equal(rows[j].UpdatedAt) {
			return rows[i].ID > rows[j].ID
		}
		return rows[i].UpdatedAt.After(rows[j].UpdatedAt)
	})
	if len(rows) > api.AppOperationalRecoveryLimit+1 {
		rows = rows[:api.AppOperationalRecoveryLimit+1]
	}
	return rows, nil
}
func (m *MemStore) GetScopedOpenMonitorIncident(_ context.Context, account, app, scope string) (*api.AppOperationalIncident, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.monitorIncidentAccessLocked(account, app); err != nil {
		return nil, err
	}
	for _, r := range m.routeMonitorIncidents[app] {
		d, ok := m.deployments[r.DeploymentID]
		if ok && d.AppID == app && normalizedDeploymentScope(d.Scope) == scope && r.Status == "open" {
			return &api.AppOperationalIncident{ID: r.ID, DeploymentID: r.DeploymentID, OpenedAt: r.OpenedAt}, nil
		}
	}
	return nil, nil
}
func finishWorkerObservation(out api.WorkerRolloutObservation) api.WorkerRolloutObservation {
	out.CheckedAt = time.Now().UTC()
	out.Basis = "resident_generation_snapshot"
	out.GenerationConverged = out.SelectedDeploymentID == out.DeploymentID && out.Running > 0 && out.Starting == 0 && out.Draining == 0 && out.OtherResidents == 0 && out.RetiredResidents == 0
	return out
}
func (s *PgStore) ReadWorkerRolloutObservation(ctx context.Context, account, app, id string) (api.WorkerRolloutObservation, error) {
	row, err := sqlc.New().ReadWorkerRolloutObservation(ctx, s.pool, sqlc.ReadWorkerRolloutObservationParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), DeploymentID: mustPgUUID(id)})
	if err != nil {
		return api.WorkerRolloutObservation{}, mapErr(err)
	}
	return finishWorkerObservation(api.WorkerRolloutObservation{AppID: app, DeploymentID: row.DeploymentID, Scope: row.Scope, SelectedDeploymentID: row.SelectedDeploymentID, Running: row.Running, Starting: row.Starting, Draining: row.Draining, OtherResidents: row.OtherResidents, RetiredResidents: row.RetiredResidents}), nil
}
func (m *MemStore) ReadWorkerRolloutObservation(_ context.Context, account, appID, id string) (api.WorkerRolloutObservation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[appID]
	if !ok || app.AccountID != account || app.Status == AppDeleted {
		return api.WorkerRolloutObservation{}, ErrNotFound
	}
	d, ok := m.deployments[id]
	if !ok || d.AppID != appID {
		return api.WorkerRolloutObservation{}, ErrNotFound
	}
	scope := normalizedDeploymentScope(d.Scope)
	out := api.WorkerRolloutObservation{AppID: appID, DeploymentID: id, Scope: scope}
	var selected Deployment
	for _, r := range m.deployments {
		if r.AppID == appID && normalizedDeploymentScope(r.Scope) == scope && r.Status == DeployLive && (selected.ID == "" || r.CreatedAt.After(selected.CreatedAt) || r.CreatedAt.Equal(selected.CreatedAt) && r.ID > selected.ID) {
			selected = r
		}
	}
	out.SelectedDeploymentID = selected.ID
	for _, r := range m.instances {
		dep, ok := m.deployments[r.DeploymentID]
		if !ok || dep.AppID != appID || normalizedDeploymentScope(dep.Scope) != scope || r.AppID != appID || r.Mode != "worker" || !State(r.State).CountsForRAM() {
			continue
		}
		if r.DeploymentID != id {
			out.RetiredResidents++
			continue
		}
		switch State(r.State) {
		case StateRunning:
			out.Running++
		case StateWaking, StateColdBooting:
			out.Starting++
		case StateDraining:
			out.Draining++
		default:
			out.OtherResidents++
		}
	}
	return finishWorkerObservation(out), nil
}
