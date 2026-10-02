package state

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) CompleteBuild(ctx context.Context, claim Build, path, key string, bytes int64, prov BuildProvenance) error {
	return s.CompleteBuildWithArtifact(ctx, claim, path, key, bytes, prov, BuildArtifactReference{})
}

func (s *PgStore) CompleteBuildWithArtifact(ctx context.Context, claim Build, path, key string, bytes int64, prov BuildProvenance, ref BuildArtifactReference) error {
	if claim.ID == "" || claim.StartedAt.IsZero() || path == "" || bytes <= 0 || prov.BuildID != claim.ID {
		return fmt.Errorf("state: complete build: invalid claim or artifact")
	}
	depID, err := parsePgUUID(claim.DeploymentID)
	if err != nil {
		return err
	}
	buildID, err := parsePgUUID(claim.ID)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	// Match cancellation's lock order: deployment first, then its build.
	dep, err := q.LockBuildCompletionDeployment(ctx, tx, depID)
	if err != nil {
		return mapErr(err)
	}
	if dep.Status != string(DeployPending) && dep.Status != string(DeployBuilding) {
		return ErrNotFound
	}
	current, err := q.LockBuildCompletionClaim(ctx, tx, sqlc.LockBuildCompletionClaimParams{ID: buildID, DeploymentID: depID})
	if err != nil {
		return mapErr(err)
	}
	if current.Status != string(BuildRunning) || !current.StartedAt.Valid || !current.StartedAt.Time.Equal(claim.StartedAt) {
		return ErrNotFound
	}
	if !ref.ValidFor(dep.ComputeRegion) || (ref.Key != "" && ref.Bytes != bytes) {
		return ErrInvalidArgument
	}
	if err := validateBuildSBOMReference(dep.ComputeRegion, claim.ID, prov.SBOMStorageKey); err != nil {
		return err
	}
	if err := q.SetBuildCompletionArtifact(ctx, tx, sqlc.SetBuildCompletionArtifactParams{
		DeploymentID: depID, ArtifactPath: path, RuntimeKey: key, ArtifactBytes: bytes,
		ExportKey: ref.Key, ExportSha256: ref.SHA256, ExportBytes: ref.Bytes,
	}); err != nil {
		return err
	}
	changed, err := q.SucceedBuildClaim(ctx, tx, sqlc.SucceedBuildClaimParams{
		BuildID: buildID, DeploymentID: depID, ClaimStartedAt: pgtype.Timestamptz{Time: claim.StartedAt, Valid: true},
	})
	if err != nil {
		return err
	}
	if changed != 1 {
		return ErrNotFound
	}
	if err := createBuildProvenance(ctx, tx, prov); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *PgStore) ListBuildsAwaitingImage(ctx context.Context, nodeID string, limit int) ([]BuildImageWork, error) {
	if limit < 0 {
		limit = 2147483647
	}
	rows, err := sqlc.New().ListBuildsAwaitingRegionalOrLocalImage(ctx, s.pool, sqlc.ListBuildsAwaitingRegionalOrLocalImageParams{NodeName: nodeID, BatchSize: int32(limit)})
	if err != nil {
		return nil, err
	}
	out := make([]BuildImageWork, 0, len(rows))
	for _, row := range rows {
		out = append(out, BuildImageWork{AppID: row.DAppID, DeploymentID: row.DeploymentID, NodeID: row.NodeID})
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
