package state

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ApplyPlatformTenant validates the complete bundle under the account lock,
// then writes all local links in one transaction. No certificate or DNS work
// occurs here; surface readiness remains visible through its existing state.
func (s *PgStore) ApplyPlatformTenant(ctx context.Context, in ApplyPlatformTenantParams) (ApplyPlatformTenantResult, error) {
	if err := validatePlatformTenantApply(in); err != nil {
		return ApplyPlatformTenantResult{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ApplyPlatformTenantResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var accountID string
	if err := tx.QueryRow(ctx, `select id from accounts where id = $1::uuid for update`, in.AccountID).Scan(&accountID); err != nil {
		return ApplyPlatformTenantResult{}, applyNoRows(err)
	}
	result, err := planPlatformTenantApply(ctx, tx, in)
	if err != nil || in.DryRun {
		return result, err
	}
	if err := commitPlatformTenantApply(ctx, tx, in, &result); err != nil {
		return ApplyPlatformTenantResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ApplyPlatformTenantResult{}, err
	}
	return result, nil
}

func planPlatformTenantApply(ctx context.Context, tx pgx.Tx, in ApplyPlatformTenantParams) (ApplyPlatformTenantResult, error) {
	result := ApplyPlatformTenantResult{
		Consumers: make([]ApplyPlatformTenantConsumerResult, 0, len(in.Consumers)),
		Surfaces:  make([]ApplyPlatformTenantSurfaceResult, 0, len(in.SurfaceIDs)),
	}
	var err error
	result.Tenant, err = scanPlatformTenant(tx.QueryRow(ctx, `select `+platformTenantCols+`
		from platform_tenants where account_id = $1::uuid and external_ref = $2 for update`, in.AccountID, in.ExternalRef))
	if errors.Is(err, ErrNotFound) {
		var count int
		if err := tx.QueryRow(ctx, `select count(*) from platform_tenants where account_id = $1::uuid`, in.AccountID).Scan(&count); err != nil {
			return ApplyPlatformTenantResult{}, err
		}
		if count >= in.TenantLimit {
			return ApplyPlatformTenantResult{}, &PlatformTenantQuotaError{Limit: in.TenantLimit, Observed: count}
		}
		result.Action = "create"
		result.Tenant = PlatformTenant{AccountID: in.AccountID, ExternalRef: in.ExternalRef,
			Name: in.Name, Status: PlatformTenantActive}
	} else if err != nil {
		return ApplyPlatformTenantResult{}, err
	} else if result.Tenant.Name != in.Name {
		return ApplyPlatformTenantResult{}, ErrConflict
	} else {
		result.Action = "unchanged"
	}
	for _, wanted := range in.Consumers {
		var appExists bool
		if err := tx.QueryRow(ctx, `select exists (select 1 from apps where id = $1::uuid and account_id = $2::uuid)`,
			wanted.AppID, in.AccountID).Scan(&appExists); err != nil {
			return ApplyPlatformTenantResult{}, err
		}
		if !appExists {
			return ApplyPlatformTenantResult{}, ErrNotFound
		}
		current, err := scanAPIConsumerRow(tx.QueryRow(ctx, `select `+apiConsumerSelectCols+`
			from api_consumers where app_id = $1::uuid and external_ref = $2 for update`, wanted.AppID, wanted.ExternalRef))
		item := ApplyPlatformTenantConsumerResult{Consumer: current, Action: "create"}
		if errors.Is(err, pgx.ErrNoRows) {
			item.Consumer = APIConsumer{AccountID: in.AccountID, AppID: wanted.AppID,
				ExternalRef: wanted.ExternalRef, Name: wanted.Name, Status: APIConsumerStatusActive}
		} else if err != nil {
			return ApplyPlatformTenantResult{}, err
		} else {
			if current.AccountID != in.AccountID || current.Name != wanted.Name || !current.Active() ||
				(current.PlatformTenantID != "" && current.PlatformTenantID != result.Tenant.ID) {
				return ApplyPlatformTenantResult{}, ErrConflict
			}
			item.Action = "link"
			if current.PlatformTenantID != "" && current.PlatformTenantID == result.Tenant.ID {
				item.Action = "unchanged"
			}
		}
		result.Consumers = append(result.Consumers, item)
	}
	for _, id := range in.SurfaceIDs {
		var linkedID *string
		if err := tx.QueryRow(ctx, `select platform_tenant_id from tenant_surfaces
			where id = $1::uuid and account_id = $2::uuid and status <> 'deleted' for update`,
			id, in.AccountID).Scan(&linkedID); err != nil {
			return ApplyPlatformTenantResult{}, applyNoRows(err)
		}
		if linkedID != nil && *linkedID != result.Tenant.ID {
			return ApplyPlatformTenantResult{}, ErrConflict
		}
		surface, err := scanTenantSurface(tx.QueryRow(ctx, `select `+tenantSurfaceCols+`
			from tenant_surfaces where id = $1::uuid`, id))
		if err != nil {
			return ApplyPlatformTenantResult{}, err
		}
		item := ApplyPlatformTenantSurfaceResult{Surface: surface, Action: "link"}
		if linkedID != nil {
			item.Action = "unchanged"
		}
		result.Surfaces = append(result.Surfaces, item)
	}
	return result, nil
}

func commitPlatformTenantApply(ctx context.Context, tx pgx.Tx, in ApplyPlatformTenantParams, result *ApplyPlatformTenantResult) error {
	if result.Action == "create" {
		tenant, err := scanPlatformTenant(tx.QueryRow(ctx, `insert into platform_tenants (account_id, external_ref, name)
			values ($1::uuid, $2, $3) returning `+platformTenantCols, in.AccountID, in.ExternalRef, in.Name))
		if err != nil {
			return applyWriteError(err)
		}
		result.Tenant = tenant
	}
	for i := range result.Consumers {
		item := &result.Consumers[i]
		switch item.Action {
		case "create":
			consumer, err := scanAPIConsumerRow(tx.QueryRow(ctx, `insert into api_consumers
				(account_id, app_id, external_ref, name, platform_tenant_id)
				values ($1::uuid, $2::uuid, $3, $4, $5::uuid) returning `+apiConsumerSelectCols,
				in.AccountID, item.Consumer.AppID, item.Consumer.ExternalRef, item.Consumer.Name, result.Tenant.ID))
			if err != nil {
				return applyWriteError(err)
			}
			item.Consumer = consumer
		case "link":
			consumer, err := scanAPIConsumerRow(tx.QueryRow(ctx, `update api_consumers
				set platform_tenant_id = $2::uuid where id = $1::uuid returning `+apiConsumerSelectCols,
				item.Consumer.ID, result.Tenant.ID))
			if err != nil {
				return applyWriteError(err)
			}
			item.Consumer = consumer
		}
	}
	for _, item := range result.Surfaces {
		if item.Action == "link" {
			if _, err := tx.Exec(ctx, `update tenant_surfaces set platform_tenant_id = $2::uuid
				where id = $1::uuid`, item.Surface.ID, result.Tenant.ID); err != nil {
				return applyWriteError(err)
			}
		}
	}
	return nil
}

func applyNoRows(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func applyWriteError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return ErrConflict
		case "23503":
			return ErrNotFound
		}
	}
	return err
}
