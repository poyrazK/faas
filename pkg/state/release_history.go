package state

import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ReleaseRun is one started release task of a deployment (ADR-1448).
type ReleaseRun struct {
	DeploymentID string
	Revision     int
	Status       AppTaskStatus
	StartedAt    time.Time
}

// ReleaseRunStore lists release tasks that ran after a rollback target last
// served, so a rollback can say which release commands it would not undo.
type ReleaseRunStore interface {
	// ReleaseRunsCrossedBy returns the started release tasks of other
	// deployments in the target's scope that started after the target last
	// served, or after it was created if it never served, oldest first. An
	// unknown target returns none.
	ReleaseRunsCrossedBy(ctx context.Context, appID, targetID string) ([]ReleaseRun, error)
}

// ReleaseDatabase is a managed PostgreSQL database a release task can migrate.
type ReleaseDatabase struct {
	ID, Name string
}

// ReleaseMigrationDatabaseStore lists the managed PostgreSQL databases whose
// migration binding delivers into an app scope (ADR-1448). Only PgStore
// implements it: MemStore has no managed PostgreSQL catalog.
type ReleaseMigrationDatabaseStore interface {
	ReleaseMigrationDatabases(ctx context.Context, appID, scope string) ([]ReleaseDatabase, error)
}

var (
	_ ReleaseRunStore               = (*PgStore)(nil)
	_ ReleaseRunStore               = (*MemStore)(nil)
	_ ReleaseMigrationDatabaseStore = (*PgStore)(nil)
)

func releaseScope(scope string) string {
	if scope == "" {
		return api.DefaultEnvScope
	}
	return scope
}

func (s *PgStore) ReleaseRunsCrossedBy(ctx context.Context, appID, targetID string) ([]ReleaseRun, error) {
	rows, err := sqlc.New().ReleaseRunsCrossedBy(ctx, s.pool, sqlc.ReleaseRunsCrossedByParams{AppID: appID, TargetID: targetID})
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]ReleaseRun, 0, len(rows))
	for _, r := range rows {
		out = append(out, ReleaseRun{
			DeploymentID: r.DeploymentID, Revision: int(r.Revision),
			Status: AppTaskStatus(r.Status), StartedAt: r.StartedAt.Time.UTC(),
		})
	}
	return out, nil
}

func (s *PgStore) ReleaseMigrationDatabases(ctx context.Context, appID, scope string) ([]ReleaseDatabase, error) {
	rows, err := sqlc.New().ReleaseMigrationDatabases(ctx, s.pool, sqlc.ReleaseMigrationDatabasesParams{AppID: appID, Scope: releaseScope(scope)})
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]ReleaseDatabase, 0, len(rows))
	for _, r := range rows {
		out = append(out, ReleaseDatabase{ID: r.DatabaseID, Name: r.Name})
	}
	return out, nil
}

func (m *MemStore) ReleaseRunsCrossedBy(_ context.Context, appID, targetID string) ([]ReleaseRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	target, ok := m.deployments[targetID]
	if !ok || target.AppID != appID {
		return nil, nil
	}
	since := target.CreatedAt
	if ended, served := m.deploymentServingEndedAt[targetID]; served {
		since = ended
	}
	scope := releaseScope(target.Scope)
	var out []ReleaseRun
	for _, task := range m.appTasks {
		if task.AppID != appID || task.Kind != AppTaskKindRelease || task.StartedAt == nil ||
			!task.StartedAt.After(since) || task.DeploymentID == targetID ||
			releaseScope(task.DeploymentScope) != scope {
			continue
		}
		run := ReleaseRun{DeploymentID: task.DeploymentID, Status: task.Status, StartedAt: task.StartedAt.UTC()}
		if d, ok := m.deployments[task.DeploymentID]; ok {
			run.Revision = d.Revision
		}
		out = append(out, run)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.Before(out[j].StartedAt) })
	return out, nil
}
