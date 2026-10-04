package globaldirectory

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type PgStore struct{ pool *pgxpool.Pool }

func NewPgStore(pool *pgxpool.Pool) *PgStore { return &PgStore{pool: pool} }

func idArg(raw string) pgtype.UUID {
	id, err := uuid.Parse(raw)
	return pgtype.UUID{Bytes: id, Valid: err == nil && id != uuid.Nil}
}

func directoryError(op string, err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	} else if errors.As(err, &pgErr) {
		switch pgErr.ConstraintName {
		case "global_directory_owner_inactive":
			err = ErrOwnerInactive
		case "global_directory_owner_forbidden":
			err = ErrOwnerForbidden
		case "global_directory_owner_plan_changed":
			err = ErrOwnerPlanChanged
		case "global_directory_org_targets_unavailable":
			err = ErrOrgTargetsUnavailable
		case "global_directory_scope":
			err = ErrBoundCell
		case "global_directory_limit":
			err = ErrLimit
		case "global_directory_stale_cell":
			err = ErrStaleCell
		case "global_release_superseded":
			err = ErrSuperseded
		case "global_release_no_targets":
			err = ErrNoTargets
		case "global_release_limit":
			err = releaseLimitFromDetail(pgErr.Detail)
		case "global_runtime_quota_limit":
			err = runtimeQuotaLimitFromDetail(pgErr.Detail)
		case "global_runtime_quota_retired":
			err = ErrRuntimeQuotaRetired
		case "global_runtime_quota_ack_identity":
			err = ErrRuntimeQuotaIdentity
		case "global_runtime_quota_identity":
			err = ErrConflict
		case "global_directory_identity", "global_release_identity":
			err = ErrConflict
		default:
			if pgErr.Code == "23505" || pgErr.Code == "23503" || pgErr.Code == "23514" {
				err = ErrConflict
			}
		}
	}
	return fmt.Errorf("global directory %s: %w", op, err)
}

func (s *PgStore) transaction(ctx context.Context, op string, change func(pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return directoryError(op, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := &sqlc.Queries{}
	if err := queries.AssertGlobalDirectoryScope(ctx, tx); err != nil {
		return directoryError(op, err)
	}
	if err := change(tx); err != nil {
		return directoryError(op, err)
	}
	return directoryError(op, tx.Commit(ctx))
}

func (s *PgStore) CheckScope(ctx context.Context) error {
	unbound, err := (&sqlc.Queries{}).GlobalDirectoryUnbound(ctx, s.pool)
	if err != nil {
		return directoryError("inspect scope", err)
	}
	if !unbound {
		return ErrBoundCell
	}
	return nil
}

func (s *PgStore) PutCell(ctx context.Context, cell Cell) (Cell, error) {
	if err := cell.Validate(); err != nil {
		return Cell{}, err
	}
	var result Cell
	err := s.transaction(ctx, "register cell", func(tx pgx.Tx) error {
		row, err := (&sqlc.Queries{}).UpsertGlobalDirectoryCell(ctx, tx, sqlc.UpsertGlobalDirectoryCellParams{CellID: idArg(cell.ID), Region: cell.Region, ApiBase: cell.APIBase, Enabled: cell.Enabled})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrConflict
		}
		result = Cell{ID: row.CellID, Region: row.Region, APIBase: row.ApiBase, Enabled: row.Enabled, Generation: row.Generation}
		return err
	})
	return result, err
}

func (s *PgStore) ListCells(ctx context.Context) ([]Cell, error) {
	rows, err := (&sqlc.Queries{}).ListGlobalDirectoryCells(ctx, s.pool)
	if err != nil {
		return nil, directoryError("list cells", err)
	}
	out := make([]Cell, 0, len(rows))
	for _, row := range rows {
		out = append(out, Cell{ID: row.CellID, Region: row.Region, APIBase: row.ApiBase, Enabled: row.Enabled, Generation: row.Generation})
	}
	return out, nil
}

func (s *PgStore) CellByRegion(ctx context.Context, region string) (Cell, error) {
	row, err := (&sqlc.Queries{}).GlobalDirectoryCellByRegion(ctx, s.pool, region)
	return Cell{ID: row.CellID, Region: row.Region, APIBase: row.ApiBase, Enabled: row.Enabled, Generation: row.Generation}, directoryError("read cell", err)
}

func (s *PgStore) CreateApp(ctx context.Context, accountID, slug string, limit int) (App, error) {
	if !validOwnerID(accountID) || !ValidSlug(slug) {
		return App{}, ErrConflict
	}
	if limit < math.MinInt32 || limit > math.MaxInt32 {
		return App{}, ErrLimit
	}
	var out App
	queries := &sqlc.Queries{}
	err := s.transaction(ctx, "create app", func(tx pgx.Tx) error {
		_, err := queries.CreateGlobalAppIdentity(ctx, tx, sqlc.CreateGlobalAppIdentityParams{OwnerID: idArg(accountID), Slug: slug, AppLimit: int32(limit)})
		if err != nil {
			return err
		}
		row, err := queries.GlobalAppIdentity(ctx, tx, sqlc.GlobalAppIdentityParams{OwnerID: idArg(accountID), Slug: slug})
		out = App{ID: row.GlobalAppID, OwnerID: row.OwnerID, Slug: row.Slug, RoutingPolicy: row.RoutingPolicy, CreatedAt: row.CreatedAt.Time}
		return err
	})
	return out, err
}

func (s *PgStore) AppBySlug(ctx context.Context, accountID, slug string) (App, error) {
	row, err := (&sqlc.Queries{}).GlobalAppIdentity(ctx, s.pool, sqlc.GlobalAppIdentityParams{OwnerID: idArg(accountID), Slug: slug})
	return App{ID: row.GlobalAppID, OwnerID: row.OwnerID, Slug: row.Slug, RoutingPolicy: row.RoutingPolicy, CreatedAt: row.CreatedAt.Time}, directoryError("read app", err)
}

func (s *PgStore) ListApps(ctx context.Context, accountID string) ([]App, error) {
	rows, err := (&sqlc.Queries{}).ListGlobalAppIdentities(ctx, s.pool, idArg(accountID))
	if err != nil {
		return nil, directoryError("list apps", err)
	}
	out := make([]App, 0, len(rows))
	for _, row := range rows {
		out = append(out, App{ID: row.GlobalAppID, OwnerID: row.OwnerID, Slug: row.Slug, RoutingPolicy: row.RoutingPolicy, CreatedAt: row.CreatedAt.Time})
	}
	return out, nil
}

func (s *PgStore) AttachTarget(ctx context.Context, accountID, slug string, binding Binding) (Target, error) {
	if err := binding.Validate(); err != nil {
		return Target{}, err
	}
	owner, err := s.OwnerByID(ctx, accountID)
	if err != nil {
		return Target{}, err
	}
	if owner.Shared() {
		return Target{}, ErrOrgTargetsUnavailable
	}
	if binding.RegionalOrgID != "" {
		return Target{}, ErrConflict
	}
	return s.attachTarget(ctx, accountID, "", slug, binding)
}

func (s *PgStore) attachTarget(ctx context.Context, accountID, actor, slug string, binding Binding) (Target, error) {
	queries := &sqlc.Queries{}
	var out Target
	err := s.transaction(ctx, "attach target", func(tx pgx.Tx) error {
		if actor != "" {
			if _, err := queries.LockGlobalOrgTargetOwner(ctx, tx, sqlc.LockGlobalOrgTargetOwnerParams{OwnerID: idArg(accountID), ActorID: idArg(actor)}); err != nil {
				return err
			}
		}
		app, err := queries.LockGlobalAppIdentity(ctx, tx, sqlc.LockGlobalAppIdentityParams{OwnerID: idArg(accountID), Slug: slug})
		if err != nil {
			return err
		}
		cell, err := queries.LockGlobalDirectoryCell(ctx, tx, sqlc.LockGlobalDirectoryCellParams{CellID: idArg(binding.CellID), Generation: binding.CellGeneration})
		if errors.Is(err, pgx.ErrNoRows) || err == nil && cell.Region != binding.Region {
			return ErrStaleCell
		}
		if err != nil {
			return err
		}
		mapArgs := sqlc.InsertGlobalOwnerCellBindingParams{OwnerID: idArg(accountID), CellID: idArg(binding.CellID), RegionalAccountID: idArg(binding.RegionalAccountID), RegionalOrgID: idArg(binding.RegionalOrgID)}
		if err := queries.InsertGlobalOwnerCellBinding(ctx, tx, mapArgs); err != nil {
			return err
		}
		mapped, err := queries.ReadGlobalOwnerCellBinding(ctx, tx, sqlc.ReadGlobalOwnerCellBindingParams{OwnerID: idArg(accountID), CellID: idArg(binding.CellID)})
		if err != nil {
			return err
		}
		if mapped.RegionalOrgID != binding.RegionalOrgID || (binding.RegionalOrgID == "" && mapped.RegionalAccountID != binding.RegionalAccountID) {
			return ErrConflict
		}
		id, err := queries.AttachGlobalAppTarget(ctx, tx, sqlc.AttachGlobalAppTargetParams{GlobalAppID: idArg(app.GlobalAppID), OwnerID: idArg(accountID), CellID: idArg(binding.CellID), Region: binding.Region, CellGeneration: binding.CellGeneration, RegionalAccountID: idArg(binding.RegionalAccountID), RegionalOrgID: idArg(binding.RegionalOrgID), RegionalAppID: idArg(binding.RegionalAppID), RegionalAppSlug: binding.RegionalAppSlug})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		rows, err := queries.ListGlobalAppTargets(ctx, tx, sqlc.ListGlobalAppTargetsParams{OwnerID: idArg(accountID), Slug: slug})
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row.GlobalAppID == id && row.Region == binding.Region {
				out = targetFromRow(row)
				return nil
			}
		}
		return ErrNotFound
	})
	return out, err
}

func targetFromRow(row sqlc.ListGlobalAppTargetsRow) Target {
	return Target{Binding: Binding{CellID: row.CellID, Region: row.Region, CellGeneration: row.CellGeneration, RegionalAccountID: row.RegionalAccountID, RegionalOrgID: row.RegionalOrgID, RegionalAppID: row.RegionalAppID, RegionalAppSlug: row.RegionalAppSlug}, GlobalAppID: row.GlobalAppID, VerifiedAt: row.VerifiedAt.Time, APIBase: row.ApiBase, CellEnabled: row.CellEnabled, RegistryGeneration: row.RegistryGeneration}
}

func (s *PgStore) ListTargets(ctx context.Context, accountID, slug string) ([]Target, error) {
	if _, err := s.AppBySlug(ctx, accountID, slug); err != nil {
		return nil, err
	}
	rows, err := (&sqlc.Queries{}).ListGlobalAppTargets(ctx, s.pool, sqlc.ListGlobalAppTargetsParams{OwnerID: idArg(accountID), Slug: slug})
	if err != nil {
		return nil, directoryError("list targets", err)
	}
	out := make([]Target, 0, len(rows))
	for _, row := range rows {
		out = append(out, targetFromRow(row))
	}
	return out, nil
}

func (s *PgStore) DetachTarget(ctx context.Context, accountID, slug, region string) error {
	owner, err := s.OwnerByID(ctx, accountID)
	if err != nil {
		return err
	}
	if owner.Shared() {
		return ErrOrgTargetsUnavailable
	}
	return s.detachTarget(ctx, accountID, "", slug, region)
}

func (s *PgStore) detachTarget(ctx context.Context, accountID, actor, slug, region string) error {
	return s.transaction(ctx, "detach target", func(tx pgx.Tx) error {
		if actor != "" {
			if _, err := (&sqlc.Queries{}).LockGlobalOrgTargetOwner(ctx, tx, sqlc.LockGlobalOrgTargetOwnerParams{OwnerID: idArg(accountID), ActorID: idArg(actor)}); err != nil {
				return err
			}
		}
		// Match attachment/allocation lock order before DELETE locks a target row.
		_, err := (&sqlc.Queries{}).LockGlobalAppIdentity(ctx, tx, sqlc.LockGlobalAppIdentityParams{OwnerID: idArg(accountID), Slug: slug})
		if err != nil {
			return err
		}
		count, err := (&sqlc.Queries{}).DeleteGlobalAppTarget(ctx, tx, sqlc.DeleteGlobalAppTargetParams{OwnerID: idArg(accountID), Slug: slug, Region: region})
		if err != nil {
			return err
		}
		if count == 0 {
			return ErrNotFound
		}
		return nil
	})
}

var _ Store = (*PgStore)(nil)
