package state

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) CompleteBuild(ctx context.Context, claim Build, path, key string, bytes int64, prov BuildProvenance) error {
	if claim.ID == "" || claim.StartedAt.IsZero() || path == "" || bytes <= 0 || prov.BuildID != claim.ID {
		return fmt.Errorf("state: complete build: invalid claim or artifact")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Match cancellation's lock order: deployment first, then its builds.
	var depStatus DeploymentStatus
	if err := tx.QueryRow(ctx, `select status from deployments where id=$1 for update`, claim.DeploymentID).Scan(&depStatus); err != nil {
		return mapErr(err)
	}
	if depStatus != DeployPending && depStatus != DeployBuilding {
		return ErrNotFound
	}
	tag, err := tx.Exec(ctx, `update builds set status='succeeded', finished_at=now()
  where id=$1 and deployment_id=$2 and status='running' and started_at=$3`, claim.ID, claim.DeploymentID, claim.StartedAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx, `update deployments set rootfs_path=$2,rootfs_key=$3,rootfs_bytes=$4 where id=$1`, claim.DeploymentID, path, key, bytes); err != nil {
		return err
	}
	if err := createBuildProvenance(ctx, tx, prov); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *PgStore) ListBuildsAwaitingImage(ctx context.Context, nodeID string, limit int) ([]BuildImageWork, error) {
	rows, err := sqlc.New().ListBuildsAwaitingImage(ctx, s.pool, sqlc.ListBuildsAwaitingImageParams{NodeID: strings.TrimSpace(nodeID), BatchLimit: int32(limit)})
	if err != nil {
		return nil, err
	}
	out := make([]BuildImageWork, 0, len(rows))
	for _, work := range rows {
		out = append(out, BuildImageWork{AppID: uuid.UUID(work.AppID.Bytes).String(), DeploymentID: uuid.UUID(work.DeploymentID.Bytes).String(), NodeID: work.NodeID})
	}
	return out, nil
}

// FailBuild fences failure against cancellation, reaping and a newer claim.
func (s *PgStore) FailBuild(ctx context.Context, claim Build, fc FailureClass, message string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status DeploymentStatus
	var appID string
	if err := tx.QueryRow(ctx, `select status,app_id from deployments where id=$1 for update`, claim.DeploymentID).Scan(&status, &appID); err != nil {
		return mapErr(err)
	}
	if status != DeployPending && status != DeployBuilding {
		return ErrNotFound
	}
	tag, err := tx.Exec(ctx, `update builds set status='failed', failure_class=$4, finished_at=now()
	where id=$1 and deployment_id=$2 and status='running' and started_at=$3`, claim.ID, claim.DeploymentID, claim.StartedAt, fc)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx, `update deployments set status='failed',error=$2,
	traffic_percent=0,rollout_state='aborted',rollout_completed_at=null,
	rollout_aborted_at=coalesce(rollout_aborted_at,now()),
	rollout_aborted_reason=coalesce(nullif($2,''),'deployment failed') where id=$1`, claim.DeploymentID, message); err != nil {
		return err
	}
	if err := rebalanceTrafficAfterFailure(ctx, tx, appID, claim.DeploymentID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
