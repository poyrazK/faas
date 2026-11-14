package state

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// OutpacedReleaseStore is the optional capability imaged uses to retire a
// release a newer revision overtook before its release command runs
// (ADR-20261020085706 amendment 13).
//
// The promotion fence (MarkDeploymentLiveIfLatest) supersedes such a release
// only at go-live, after its release command (a migration) has run. A release
// waiting for approval can wait for days while a newer revision ships, and
// approving it ran the older migration against the newer schema.
// SupersedeOutpacedRelease applies the fence's own test first: when a newer
// revision of the app and scope exists, the release is superseded and its
// command never runs. A command still queued is cancelled with it (ADR-1448
// amendment 1); one a worker already claimed keeps its release. It reports
// whether it superseded the release.
type OutpacedReleaseStore interface {
	SupersedeOutpacedRelease(ctx context.Context, deploymentID string) (bool, error)
}

var (
	_ OutpacedReleaseStore = (*PgStore)(nil)
	_ OutpacedReleaseStore = (*MemStore)(nil)
)

// outpaces reports whether other is a newer revision of d's app and scope
// that can still ship, mirroring HasNewerDeploymentRevision. A newer revision
// that failed or was cancelled ships nothing, so it does not outpace d
// (ADR-20261020085706 amendment 14).
func outpaces(other, d Deployment) bool {
	return other.AppID == d.AppID && normalizedDeploymentScope(other.Scope) == normalizedDeploymentScope(d.Scope) &&
		other.Revision > d.Revision && other.Status != DeployFailed && other.Status != DeployCancelled
}

// releaseOutpaceEligible reports whether d is a latest-revision release in
// its release phase that the promotion fence would supersede. A prepared
// rollback deliberately targets an older revision and is never outpaced.
func releaseOutpaceEligible(d Deployment, rollbackPrepared bool) bool {
	return d.Status == DeploySnapshotting && !rollbackPrepared &&
		d.Kind.RequiresLatestRevision() && d.Revision > 0 && d.EnvironmentWorkloadRuntime == ""
}

func (s *PgStore) SupersedeOutpacedRelease(ctx context.Context, deploymentID string) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var d Deployment
	var kind, status string
	var prepared, admitted bool
	if err := tx.QueryRow(ctx,
		`select app_id::text, scope, status, kind, coalesce(revision, 0), coalesce(environment_workload_runtime::text, ''),
		        rollback_prepared_at is not null,
		        exists (select 1 from app_tasks t where t.deployment_id = deployments.id and t.kind = 'release')
		   from deployments where id = $1 for update`, deploymentID).
		Scan(&d.AppID, &d.Scope, &status, &kind, &d.Revision, &d.EnvironmentWorkloadRuntime, &prepared, &admitted); err != nil {
		return false, mapErr(err)
	}
	d.Status, d.Kind = DeploymentStatus(status), DeploymentKind(kind)
	if !releaseOutpaceEligible(d, prepared) {
		return false, nil
	}
	newer, err := sqlc.New().HasNewerDeploymentRevision(ctx, tx, sqlc.HasNewerDeploymentRevisionParams{
		AppID: mustPgUUID(d.AppID), Scope: normalizedDeploymentScope(d.Scope), Revision: int64(d.Revision),
	})
	if err != nil {
		return false, fmt.Errorf("state: check newer deployment revision: %w", err)
	}
	if !newer {
		return false, nil
	}
	if admitted {
		// ADR-1448 amendment 1: a release command still queued has not
		// started, so it is cancelled with its release. The status guard
		// loses to a claim that already took the row.
		tag, err := tx.Exec(ctx,
			`update app_tasks set status = 'cancelled', retry_at = null, finished_at = clock_timestamp(), updated_at = clock_timestamp()
			  where deployment_id = $1 and kind = 'release' and status = 'queued' and cancel_requested_at is null`, deploymentID)
		if err != nil {
			return false, fmt.Errorf("state: cancel outpaced release task: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return false, nil
		}
	}
	if err := supersedeOutpacedDeploymentTx(ctx, tx, deploymentID); err != nil {
		return false, fmt.Errorf("state: supersede outpaced release: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("state: commit outpaced release: %w", err)
	}
	return true, nil
}

func (m *MemStore) SupersedeOutpacedRelease(_ context.Context, deploymentID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.deployments[deploymentID]
	if !ok {
		return false, ErrNotFound
	}
	_, prepared := m.rollbackPrepared[deploymentID]
	if !releaseOutpaceEligible(d, prepared) {
		return false, nil
	}
	newer := false
	for _, other := range m.deployments {
		if outpaces(other, d) {
			newer = true
			break
		}
	}
	if !newer {
		return false, nil
	}
	queued := ""
	for id, task := range m.appTasks {
		if task.DeploymentID != deploymentID || task.Kind != AppTaskKindRelease {
			continue
		}
		if task.Status != AppTaskQueued || task.CancelRequested != nil {
			return false, nil
		}
		queued = id
	}
	if queued != "" {
		now := time.Now().UTC()
		task := m.appTasks[queued]
		task.Status, task.RetryAt, task.FinishedAt, task.UpdatedAt = AppTaskCancelled, nil, appTaskTimePtr(now), now
		m.appTasks[queued] = task
	}
	m.putDeploymentLocked(d.ID, m.supersedeOutpacedDeploymentLocked(d))
	return true, nil
}
