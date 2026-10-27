package state

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// OutpacedBuildStore is the optional capability builderd uses to skip a
// build whose deployment can no longer go live (ADR-20261028090000).
//
// A latest-revision deployment (DeploymentKind.RequiresLatestRevision) is
// superseded at promotion when any deployment of the same app and scope has a
// higher revision. Its build still ran to completion first, so a burst of
// pushes built every revision in turn on the single guaranteed builder slot
// before the newest one could start. SupersedeOutpacedBuild applies the
// promotion fence's own test to a claimed build: when the deployment is
// already outpaced it is superseded and the build cancelled in one
// transaction, fenced on the claim. It reports whether it did so.
type OutpacedBuildStore interface {
	SupersedeOutpacedBuild(ctx context.Context, claim Build) (bool, error)
}

// buildOutpaceEligible reports whether d is a latest-revision deployment the
// promotion fence would supersede, or one CreateDeployment already superseded
// while it was pending (that leaves its queued build behind). Environment-
// managed workloads are left to the graph activation that owns their
// promotion, as CreateDeployment does.
func buildOutpaceEligible(d Deployment) bool {
	return (d.Status == DeployPending || d.Status == DeployBuilding || d.Status == DeploySuperseded) &&
		d.Kind.RequiresLatestRevision() && d.Revision > 0 && d.EnvironmentWorkloadRuntime == ""
}

func (s *PgStore) SupersedeOutpacedBuild(ctx context.Context, claim Build) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var d Deployment
	var kind, status string
	if err := tx.QueryRow(ctx,
		`select app_id::text, scope, status, kind, coalesce(revision, 0), coalesce(environment_workload_runtime::text, '')
		   from deployments where id = $1 for update`, claim.DeploymentID).
		Scan(&d.AppID, &d.Scope, &status, &kind, &d.Revision, &d.EnvironmentWorkloadRuntime); err != nil {
		return false, mapErr(err)
	}
	d.Status, d.Kind = DeploymentStatus(status), DeploymentKind(kind)
	if !buildOutpaceEligible(d) {
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
	tag, err := tx.Exec(ctx, `update builds
		    set status = 'cancelled', cancelled_at = now(), finished_at = now()
		  where id = $1 and deployment_id = $2 and status = 'running' and started_at = $3`,
		claim.ID, claim.DeploymentID, claim.StartedAt)
	if err != nil {
		return false, fmt.Errorf("state: cancel outpaced build: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return false, nil
	}
	if _, err := tx.Exec(ctx, `update deployments set status = 'superseded', traffic_percent = 0 where id = $1 and status <> 'superseded'`, claim.DeploymentID); err != nil {
		return false, fmt.Errorf("state: supersede outpaced deployment: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("state: commit outpaced build: %w", err)
	}
	return true, nil
}

func (m *MemStore) SupersedeOutpacedBuild(_ context.Context, claim Build) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.deployments[claim.DeploymentID]
	if !ok {
		return false, ErrNotFound
	}
	if !buildOutpaceEligible(d) {
		return false, nil
	}
	newer := false
	for _, other := range m.deployments {
		if other.AppID == d.AppID && normalizedDeploymentScope(other.Scope) == normalizedDeploymentScope(d.Scope) && other.Revision > d.Revision {
			newer = true
			break
		}
	}
	if !newer {
		return false, nil
	}
	b, ok := m.builds[claim.ID]
	if !ok || b.Status != BuildRunning || b.DeploymentID != claim.DeploymentID || !b.StartedAt.Equal(claim.StartedAt) {
		return false, nil
	}
	now := time.Now()
	b.Status, b.CancelledAt, b.FinishedAt = BuildCancelled, &now, now
	m.builds[b.ID] = b
	d.Status, d.TrafficPercent = DeploySuperseded, 0
	m.putDeploymentLocked(d.ID, d)
	return true, nil
}
