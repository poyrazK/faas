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

// ReleaseRunReplacingStore finds the release task of the deployment that
// replaced a rollback target. ADR-1448 does not count it as crossed, because
// it ran while the target served; a rollback that restores the database
// undoes it too (ADR-20261023181931 amendment, 2026-11-16).
type ReleaseRunReplacingStore interface {
	// ReleaseRunReplacing returns the newest started release task of another
	// deployment in the target's scope that started after the target was
	// created and no later than it last stopped serving, whose deployment
	// still served then. ok is false when there is none, or the target never
	// stopped serving.
	ReleaseRunReplacing(ctx context.Context, appID, targetID string) (run ReleaseRun, ok bool, err error)
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
	_ ReleaseRunReplacingStore      = (*PgStore)(nil)
	_ ReleaseRunReplacingStore      = (*MemStore)(nil)
)

func releaseScope(scope string) string {
	if scope == "" {
		return api.DefaultEnvScope
	}
	return scope
}

func (s *PgStore) ReleaseRunsCrossedBy(ctx context.Context, appID, targetID string) ([]ReleaseRun, error) {
	return releaseRunsCrossedByDBTX(ctx, s.pool, appID, targetID)
}

func releaseRunsCrossedByDBTX(ctx context.Context, db sqlc.DBTX, appID, targetID string) ([]ReleaseRun, error) {
	rows, err := sqlc.New().ReleaseRunsCrossedBy(ctx, db, sqlc.ReleaseRunsCrossedByParams{AppID: appID, TargetID: targetID})
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

func (s *PgStore) ReleaseRunReplacing(ctx context.Context, appID, targetID string) (ReleaseRun, bool, error) {
	rows, err := sqlc.New().ReleaseRunReplacing(ctx, s.pool, sqlc.ReleaseRunReplacingParams{AppID: appID, TargetID: targetID})
	if err != nil {
		return ReleaseRun{}, false, mapErr(err)
	}
	if len(rows) == 0 {
		return ReleaseRun{}, false, nil
	}
	r := rows[0]
	return ReleaseRun{
		DeploymentID: r.DeploymentID, Revision: int(r.Revision),
		Status: AppTaskStatus(r.Status), StartedAt: r.StartedAt.Time.UTC(),
	}, true, nil
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
	return m.releaseRunsCrossedByLocked(appID, targetID), nil
}

// ReleaseRunReplacing mirrors PgStore.ReleaseRunReplacing.
func (m *MemStore) ReleaseRunReplacing(_ context.Context, appID, targetID string) (ReleaseRun, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	target, ok := m.deployments[targetID]
	if !ok || target.AppID != appID {
		return ReleaseRun{}, false, nil
	}
	ended, served := m.deploymentServingEndedAt[targetID]
	if !served {
		return ReleaseRun{}, false, nil
	}
	scope := releaseScope(target.Scope)
	var best ReleaseRun
	found := false
	for _, task := range m.appTasks {
		if task.AppID != appID || task.Kind != AppTaskKindRelease || task.StartedAt == nil ||
			task.DeploymentID == targetID || releaseScope(task.DeploymentScope) != scope ||
			!task.StartedAt.After(target.CreatedAt) || task.StartedAt.After(ended) {
			continue
		}
		d, ok := m.deployments[task.DeploymentID]
		if !ok {
			continue
		}
		dEnded, dServed := m.deploymentServingEndedAt[d.ID]
		if d.Status != DeployLive && (!dServed || dEnded.Before(ended)) {
			continue
		}
		if found && !task.StartedAt.After(best.StartedAt) {
			continue
		}
		best = ReleaseRun{DeploymentID: task.DeploymentID, Revision: d.Revision, Status: task.Status, StartedAt: task.StartedAt.UTC()}
		found = true
	}
	return best, found, nil
}

func (m *MemStore) releaseRunsCrossedByLocked(appID, targetID string) []ReleaseRun {
	target, ok := m.deployments[targetID]
	if !ok || target.AppID != appID {
		return nil
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
	return out
}
