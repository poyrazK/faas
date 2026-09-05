package state

import (
	"context"
	"fmt"
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
	rows, err := s.pool.Query(ctx, `select d.app_id,d.id,coalesce(p.builder_node_id,'')
  from deployments d join builds b on b.deployment_id=d.id
  join build_provenance p on p.build_id=b.id
  where d.status in ('pending','building') and b.status='succeeded'
    and d.rootfs_path is not null
    and (nullif($1,'') is null or coalesce(p.builder_node_id,'')='' or p.builder_node_id=$1)
  order by b.finished_at,b.id limit $2`, nodeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BuildImageWork
	for rows.Next() {
		var work BuildImageWork
		if err := rows.Scan(&work.AppID, &work.DeploymentID, &work.NodeID); err != nil {
			return nil, err
		}
		out = append(out, work)
	}
	return out, rows.Err()
}

// FailBuild fences failure against cancellation, reaping and a newer claim.
func (s *PgStore) FailBuild(ctx context.Context, claim Build, fc FailureClass, message string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status DeploymentStatus
	if err := tx.QueryRow(ctx, `select status from deployments where id=$1 for update`, claim.DeploymentID).Scan(&status); err != nil {
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
	if _, err := tx.Exec(ctx, `update deployments set status='failed',error=$2 where id=$1`, claim.DeploymentID, message); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
