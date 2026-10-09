package state

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ RuntimeUpgradeTargetStore = (*PgStore)(nil)

func (s *PgStore) PinDeploymentRuntimeUpgradeTarget(ctx context.Context, depID, releaseID, sourceSHA256 string) error {
	dep, err := uuid.Parse(depID)
	if err != nil || !runtimeSHA.MatchString(releaseID) || !runtimeSHA.MatchString(sourceSHA256) {
		return ErrInvalidArgument
	}
	target, err := s.RuntimeReleaseByID(ctx, releaseID)
	if err != nil {
		return err
	}
	if err := target.Validate(); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err := q.LockRuntimeUpgradeTargetApp(ctx, tx, NewPgtypeUUID(dep)); err != nil {
		return mapErr(err)
	}
	_, err = q.PinDeploymentRuntimeUpgradeTarget(ctx, tx, sqlc.PinDeploymentRuntimeUpgradeTargetParams{
		DeploymentID: NewPgtypeUUID(dep), ReleaseID: releaseID, SourceSha256: sourceSHA256,
		SourceFieldLimit: api.RuntimeUpgradeSourceFieldMaxBytes,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return mapErr(err)
	}
	return mapErr(tx.Commit(ctx))
}

func (s *PgStore) DeploymentRuntimeUpgradeTarget(ctx context.Context, depID string) (RuntimeRelease, error) {
	dep, err := uuid.Parse(depID)
	if err != nil {
		return RuntimeRelease{}, ErrInvalidArgument
	}
	r, err := sqlc.New().GetDeploymentRuntimeUpgradeTarget(ctx, s.pool, NewPgtypeUUID(dep))
	if err != nil {
		return RuntimeRelease{}, mapErr(err)
	}
	if !r.SourceMatches {
		return RuntimeRelease{}, ErrConflict
	}
	return RuntimeRelease{ID: r.ID, Runtime: r.Runtime, Architecture: r.Architecture, SourceRef: r.SourceRef,
		GuestInitSHA256: r.GuestInitSha256, LayoutVersion: r.LayoutVersion, BaseSHA256: r.BaseSha256, CreatedAt: r.CreatedAt.Time}, nil
}
