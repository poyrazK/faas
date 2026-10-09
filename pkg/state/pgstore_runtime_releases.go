package state

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ RuntimeReleaseStore = (*PgStore)(nil)

func runtimeReleaseFromRow(r sqlc.RuntimeRelease) RuntimeRelease {
	return RuntimeRelease{ID: r.ID, Runtime: r.Runtime, Architecture: r.Architecture, SourceRef: r.SourceRef, GuestInitSHA256: r.GuestInitSha256, LayoutVersion: r.LayoutVersion, BaseSHA256: r.BaseSha256, CreatedAt: r.CreatedAt.Time}
}
func (s *PgStore) PublishRuntimeRelease(ctx context.Context, r RuntimeRelease) (RuntimeRelease, error) {
	if err := r.Validate(); err != nil {
		return RuntimeRelease{}, err
	}
	row, err := sqlc.New().PublishRuntimeRelease(ctx, s.pool, sqlc.PublishRuntimeReleaseParams{ID: r.ID, Runtime: r.Runtime, Architecture: r.Architecture, SourceRef: r.SourceRef, GuestInitSha256: r.GuestInitSHA256, LayoutVersion: r.LayoutVersion, BaseSha256: r.BaseSHA256})
	return runtimeReleaseFromRow(row), mapErr(err)
}
func (s *PgStore) RuntimeReleaseByID(ctx context.Context, id string) (RuntimeRelease, error) {
	row, err := sqlc.New().GetRuntimeRelease(ctx, s.pool, id)
	return runtimeReleaseFromRow(row), mapErr(err)
}
func (s *PgStore) FindRuntimeRelease(ctx context.Context, r RuntimeRelease) (RuntimeRelease, error) {
	row, err := sqlc.New().FindRuntimeRelease(ctx, s.pool, sqlc.FindRuntimeReleaseParams{Runtime: r.Runtime, Architecture: r.Architecture, SourceRef: r.SourceRef, GuestInitSha256: r.GuestInitSHA256, LayoutVersion: r.LayoutVersion})
	return runtimeReleaseFromRow(row), mapErr(err)
}
func (s *PgStore) ListRuntimeReleases(ctx context.Context, runtime, arch string) ([]RuntimeRelease, error) {
	rows, err := sqlc.New().ListRuntimeReleases(ctx, s.pool, sqlc.ListRuntimeReleasesParams{Runtime: runtime, Architecture: arch, Limit: int32(runtimeCatalogLimit())})
	if err != nil {
		return nil, err
	}
	out := make([]RuntimeRelease, 0, len(rows))
	for _, row := range rows {
		out = append(out, runtimeReleaseFromRow(row))
	}
	return out, nil
}
func (s *PgStore) BindDeploymentRuntimeRelease(ctx context.Context, depID, key, id string) error {
	dep, err := uuid.Parse(depID)
	if err != nil || key == "" || len(key) > api.RuntimeReleaseArtifactKeyMaxBytes {
		return ErrInvalidArgument
	}
	_, err = sqlc.New().BindDeploymentRuntimeRelease(ctx, s.pool, sqlc.BindDeploymentRuntimeReleaseParams{DeploymentID: NewPgtypeUUID(dep), RootfsKey: key, ReleaseID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	return mapErr(err)
}
func (s *PgStore) RuntimeReleaseForArtifact(ctx context.Context, account, key string) (RuntimeRelease, error) {
	acct, err := uuid.Parse(account)
	if err != nil {
		return RuntimeRelease{}, ErrInvalidArgument
	}
	row, err := sqlc.New().GetArtifactRuntimeRelease(ctx, s.pool, sqlc.GetArtifactRuntimeReleaseParams{AccountID: NewPgtypeUUID(acct), RootfsKey: key})
	return runtimeReleaseFromRow(row), mapErr(err)
}
func (s *PgStore) BuildRuntimeBaseRef(ctx context.Context, id string) (string, error) {
	build, err := uuid.Parse(id)
	if err != nil {
		return "", ErrInvalidArgument
	}
	ref, err := sqlc.New().GetBuildRuntimeBaseRef(ctx, s.pool, NewPgtypeUUID(build))
	return ref, mapErr(err)
}
