package release

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// RollbackGuard reports the release commands a rollback to target would not
// undo (ADR-1448): release tasks of other deployments in its scope that
// started after it last served. It returns nil when there are none or the
// caller acknowledged them, and the rollback_crosses_release problem
// otherwise. A store without release history reports none.
func RollbackGuard(ctx context.Context, store any, appID string, target state.Deployment, acknowledged bool) ([]state.ReleaseRun, *api.Problem, error) {
	history, ok := store.(state.ReleaseRunStore)
	if !ok {
		return nil, nil, nil
	}
	runs, err := history.ReleaseRunsCrossedBy(ctx, appID, target.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("list releases crossed by rollback: %w", err)
	}
	if len(runs) == 0 || acknowledged {
		return runs, nil, nil
	}
	var databases []state.ReleaseDatabase
	if catalog, ok := store.(state.ReleaseMigrationDatabaseStore); ok {
		databases, err = catalog.ReleaseMigrationDatabases(ctx, appID, target.Scope)
		if err != nil {
			return nil, nil, fmt.Errorf("list release migration databases: %w", err)
		}
	}
	return runs, CrossingProblem(target, runs, databases), nil
}

// CrossingProblem is the 409 for an unacknowledged rollback over runs, with a
// restore point per migratable database: the start of the earliest crossed
// release, truncated to the second so it falls before the release ran.
func CrossingProblem(target state.Deployment, runs []state.ReleaseRun, databases []state.ReleaseDatabase) *api.Problem {
	crossed := make([]api.RollbackCrossedRelease, 0, len(runs))
	earliest := runs[0]
	for _, r := range runs {
		crossed = append(crossed, api.RollbackCrossedRelease{
			DeploymentID: r.DeploymentID, Revision: r.Revision, Status: string(r.Status),
			StartedAt: r.StartedAt.UTC().Format(time.RFC3339),
		})
		if r.StartedAt.Before(earliest.StartedAt) {
			earliest = r
		}
	}
	point := earliest.StartedAt.UTC().Truncate(time.Second).Format(time.RFC3339)
	restore := make([]api.ReleaseRestorePoint, 0, len(databases))
	for _, db := range databases {
		restore = append(restore, api.ReleaseRestorePoint{
			DatabaseID: db.ID, DatabaseName: db.Name, PointInTime: point,
			Command: fmt.Sprintf("gregale postgres restore %s --name %s --point-in-time %s",
				db.ID, restoreTargetName(db.Name, earliest), point),
		})
	}
	return api.ErrRollbackCrossesRelease(deploymentRef(target), crossed, restore)
}

// restoreTargetName suggests "<name>-before-v<rev>" within managed
// PostgreSQL's 63-character name limit.
func restoreTargetName(name string, run state.ReleaseRun) string {
	suffix := "-before-release"
	if run.Revision > 0 {
		suffix = fmt.Sprintf("-before-v%d", run.Revision)
	}
	if limit := 63 - len(suffix); len(name) > limit {
		name = strings.TrimRight(name[:limit], "-")
	}
	return name + suffix
}

func deploymentRef(d state.Deployment) string {
	if d.Revision > 0 {
		return fmt.Sprintf("v%d", d.Revision)
	}
	return d.ID
}

// ReleaseSummary reports a deployment's release command run for single
// deployment reads (ADR-1448). It returns nil for a deployment without a
// release command or when the store has no release tasks.
func ReleaseSummary(ctx context.Context, store any, d state.Deployment) (*api.DeploymentRelease, error) {
	if len(d.ReleaseCommand) == 0 {
		return nil, nil
	}
	tasks, ok := store.(interface {
		ReleaseAppTaskByDeployment(ctx context.Context, deploymentID string) (state.AppTask, error)
	})
	if !ok {
		return nil, nil
	}
	task, err := tasks.ReleaseAppTaskByDeployment(ctx, d.ID)
	if errors.Is(err, state.ErrNotFound) {
		return &api.DeploymentRelease{Status: "pending"}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load release task: %w", err)
	}
	out := &api.DeploymentRelease{Status: string(task.Status)}
	if task.StartedAt != nil {
		out.StartedAt = task.StartedAt.UTC().Format(time.RFC3339)
	}
	if task.FinishedAt != nil {
		out.FinishedAt = task.FinishedAt.UTC().Format(time.RFC3339)
	}
	if catalog, ok := store.(state.ReleaseMigrationDatabaseStore); ok {
		databases, err := catalog.ReleaseMigrationDatabases(ctx, d.AppID, d.Scope)
		if err != nil {
			return nil, fmt.Errorf("list release migration databases: %w", err)
		}
		for _, db := range databases {
			out.MigrationDatabases = append(out.MigrationDatabases, api.ReleaseDatabaseRef{ID: db.ID, Name: db.Name})
		}
	}
	return out, nil
}
