package globaldirectory

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) readRelease(ctx context.Context, db sqlc.DBTX, owner, slug, releaseID string) (Release, error) {
	q := &sqlc.Queries{}
	row, err := q.ReadGlobalImageRelease(ctx, db, sqlc.ReadGlobalImageReleaseParams{AccountID: idArg(owner), Slug: slug, ReleaseID: idArg(releaseID)})
	if err != nil {
		return Release{}, err
	}
	out := Release{GlobalAppID: row.GlobalAppID, AccountID: row.AccountID, ReleaseID: row.ReleaseID, Generation: row.Generation, DesiredGeneration: row.DesiredReleaseGeneration, Image: row.ImageRef, CreatedAt: row.CreatedAt.Time}
	rows, err := q.ListGlobalImageReleaseTargets(ctx, db, sqlc.ListGlobalImageReleaseTargetsParams{GlobalAppID: idArg(out.GlobalAppID), ReleaseID: idArg(releaseID)})
	if err != nil {
		return Release{}, err
	}
	out.Targets = make([]ReleaseTarget, 0, len(rows))
	for _, row := range rows {
		out.Targets = append(out.Targets, ReleaseTarget{Target: Target{Binding: Binding{CellID: row.CellID, Region: row.Region, CellGeneration: row.CellGeneration, RegionalAccountID: row.RegionalAccountID, RegionalAppID: row.RegionalAppID, RegionalAppSlug: row.RegionalAppSlug}, GlobalAppID: out.GlobalAppID, VerifiedAt: row.VerifiedAt.Time, APIBase: row.ApiBase, CellEnabled: true, RegistryGeneration: row.CellGeneration}, Outcome: row.Outcome, Diagnostic: row.Diagnostic, DeploymentID: row.DeploymentID, UpdatedAt: row.UpdatedAt.Time})
	}
	return out, nil
}

func (s *PgStore) PlanRelease(ctx context.Context, owner, slug, releaseID, image string) (Release, error) {
	if !ValidReleaseInput(releaseID, image) {
		return Release{}, ErrConflict
	}
	var out Release
	err := s.transaction(ctx, "plan release", func(tx pgx.Tx) error {
		_, err := (&sqlc.Queries{}).PlanGlobalImageRelease(ctx, tx, sqlc.PlanGlobalImageReleaseParams{AccountID: idArg(owner), Slug: slug, ReleaseID: idArg(releaseID), ImageRef: image})
		if err != nil {
			return err
		}
		out, err = s.readRelease(ctx, tx, owner, slug, releaseID)
		return err
	})
	return out, err
}

func (s *PgStore) GetRelease(ctx context.Context, owner, slug, releaseID string) (Release, error) {
	var out Release
	err := s.transaction(ctx, "read release", func(tx pgx.Tx) error {
		// Serialize the head, snapshot and results into one coherent response.
		_, err := (&sqlc.Queries{}).LockGlobalAppIdentity(ctx, tx, sqlc.LockGlobalAppIdentityParams{AccountID: idArg(owner), Slug: slug})
		if err != nil {
			return err
		}
		out, err = s.readRelease(ctx, tx, owner, slug, releaseID)
		return err
	})
	return out, err
}

func (s *PgStore) lockRelease(ctx context.Context, tx pgx.Tx, owner, slug, releaseID string) (Release, error) {
	_, err := (&sqlc.Queries{}).LockGlobalAppIdentity(ctx, tx, sqlc.LockGlobalAppIdentityParams{AccountID: idArg(owner), Slug: slug})
	if err != nil {
		return Release{}, err
	}
	r, err := s.readRelease(ctx, tx, owner, slug, releaseID)
	if err == nil && r.Generation != r.DesiredGeneration {
		return Release{}, ErrSuperseded
	}
	return r, err
}

func (s *PgStore) checkReleaseSnapshot(ctx context.Context, tx pgx.Tx, r Release, region string) error {
	q := &sqlc.Queries{}
	for _, target := range r.Targets {
		if target.Region != region {
			continue
		}
		cell, err := q.LockGlobalDirectoryCell(ctx, tx, sqlc.LockGlobalDirectoryCellParams{CellID: idArg(target.CellID), Generation: target.CellGeneration})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrStaleCell
		}
		if err != nil {
			return err
		}
		current, err := q.CurrentGlobalReleaseBinding(ctx, tx, sqlc.CurrentGlobalReleaseBindingParams{GlobalAppID: idArg(r.GlobalAppID), Region: region})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrStaleCell
		}
		if err != nil {
			return err
		}
		binding := Binding{CellID: current.CellID, Region: region, CellGeneration: current.CellGeneration, RegionalAccountID: current.RegionalAccountID, RegionalAppID: current.RegionalAppID, RegionalAppSlug: current.RegionalAppSlug}
		if binding != target.Binding || cell.ApiBase != target.APIBase {
			return ErrStaleCell
		}
		return nil
	}
	return ErrNotFound
}

func (s *PgStore) CheckReleaseTarget(ctx context.Context, owner, slug, releaseID, region string) error {
	return s.transaction(ctx, "check release target", func(tx pgx.Tx) error {
		r, err := s.lockRelease(ctx, tx, owner, slug, releaseID)
		if err != nil {
			return err
		}
		return s.checkReleaseSnapshot(ctx, tx, r, region)
	})
}

func (s *PgStore) RecordReleaseResult(ctx context.Context, owner, slug, releaseID, region string, result ReleaseResult) error {
	if !result.valid() {
		return ErrConflict
	}
	return s.transaction(ctx, "record release result", func(tx pgx.Tx) error {
		r, err := s.lockRelease(ctx, tx, owner, slug, releaseID)
		if err != nil {
			return err
		}
		if result.Outcome != ReleaseBlocked {
			if err := s.checkReleaseSnapshot(ctx, tx, r, region); err != nil {
				return err
			}
		}
		for _, target := range r.Targets {
			if target.Region != region {
				continue
			}
			if target.Outcome != ReleasePending {
				return nil
			}
			_, err := (&sqlc.Queries{}).UpdateGlobalImageReleaseTarget(ctx, tx, sqlc.UpdateGlobalImageReleaseTargetParams{GlobalAppID: idArg(r.GlobalAppID), ReleaseID: idArg(releaseID), Region: region, Outcome: result.Outcome, Diagnostic: result.Diagnostic, DeploymentID: idArg(result.DeploymentID)})
			return err
		}
		return ErrNotFound
	})
}
