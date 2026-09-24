package state

import (
	"context"
	"errors"
	"time"

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
		Surfaces:  make([]ApplyPlatformTenantSurfaceResult, 0, len(in.SurfaceIDs)+len(in.Surfaces)),
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
	if err := planPlatformTenantSurfaces(ctx, tx, in, &result); err != nil {
		return ApplyPlatformTenantResult{}, err
	}
	return result, nil
}

func planPlatformTenantSurfaces(ctx context.Context, tx pgx.Tx, in ApplyPlatformTenantParams, result *ApplyPlatformTenantResult) error {
	if len(in.Surfaces) == 0 {
		return nil
	}
	var surfaceCount int
	if err := tx.QueryRow(ctx, `select count(*) from tenant_surfaces where account_id = $1::uuid and status <> 'deleted'`, in.AccountID).Scan(&surfaceCount); err != nil {
		return err
	}
	for _, wanted := range in.Surfaces {
		var appExists bool
		if err := tx.QueryRow(ctx, `select exists(select 1 from apps where id = $1::uuid and account_id = $2::uuid)`, wanted.AppID, in.AccountID).Scan(&appExists); err != nil {
			return err
		}
		if !appExists {
			return ErrNotFound
		}
		item := ApplyPlatformTenantSurfaceResult{Action: "create", Hostnames: make([]ApplyPlatformTenantHostnameResult, 0, len(wanted.Hostnames))}
		current, err := scanTenantSurface(tx.QueryRow(ctx, `select `+tenantSurfaceCols+` from tenant_surfaces
			where account_id = $1::uuid and name = $2 and status <> 'deleted' for update`, in.AccountID, wanted.Name))
		if errors.Is(err, ErrNotFound) {
			surfaceCount++
			if surfaceCount > in.Limits.TenantSurfacesPerAccount {
				return &TenantSurfaceQuotaError{Limit: in.Limits.TenantSurfacesPerAccount, Observed: surfaceCount - 1}
			}
			item.Surface = TenantSurface{AccountID: in.AccountID, AppID: wanted.AppID, Name: wanted.Name,
				CertKind: wanted.CertKind, Status: SurfaceStatusPending, CertState: CertStateNone}
		} else if err != nil {
			return err
		} else {
			if current.AppID != wanted.AppID || current.CertKind != wanted.CertKind {
				return ErrConflict
			}
			for _, linked := range result.Surfaces {
				if linked.Surface.ID == current.ID {
					return ErrInvalidArgument
				}
			}
			var linkedID *string
			if err := tx.QueryRow(ctx, `select platform_tenant_id from tenant_surfaces where id = $1::uuid`, current.ID).Scan(&linkedID); err != nil {
				return err
			}
			if linkedID != nil && *linkedID != result.Tenant.ID {
				return ErrConflict
			}
			item.Surface = current
			item.Action = "link"
			if linkedID != nil {
				item.Action = "unchanged"
			}
		}
		var existingCount int
		if item.Surface.ID != "" {
			if err := tx.QueryRow(ctx, `select count(*) from tenant_hostnames where surface_id = $1::uuid`, item.Surface.ID).Scan(&existingCount); err != nil {
				return err
			}
		}
		for _, host := range wanted.Hostnames {
			var id, ownerID, hostname, challenge string
			var verifiedAt, lastCheckAt *time.Time
			var lastError *string
			err := tx.QueryRow(ctx, `select id, surface_id, hostname, challenge_token, verified_at, last_check_at, last_error
				from tenant_hostnames where hostname = $1 for update`, host.Hostname).
				Scan(&id, &ownerID, &hostname, &challenge, &verifiedAt, &lastCheckAt, &lastError)
			hostResult := ApplyPlatformTenantHostnameResult{Action: "create", Hostname: TenantHostname{
				Hostname: host.Hostname, ChallengeToken: host.ChallengeToken}}
			if errors.Is(err, pgx.ErrNoRows) {
				existingCount++
				if existingCount > in.Limits.TenantHostnamesPerSurface {
					return &TenantHostnameQuotaError{Limit: in.Limits.TenantHostnamesPerSurface, Observed: existingCount - 1, SurfaceID: item.Surface.ID}
				}
			} else if err != nil {
				return err
			} else {
				if ownerID != item.Surface.ID || item.Surface.ID == "" {
					return ErrConflict
				}
				hostResult.Action = "unchanged"
				hostResult.Hostname = TenantHostname{ID: id, SurfaceID: ownerID, Hostname: hostname, ChallengeToken: challenge}
				if verifiedAt != nil {
					hostResult.Hostname.VerifiedAt = *verifiedAt
				}
				if lastCheckAt != nil {
					hostResult.Hostname.LastCheckAt = *lastCheckAt
				}
				if lastError != nil {
					hostResult.Hostname.LastError = *lastError
				}
			}
			item.Hostnames = append(item.Hostnames, hostResult)
		}
		result.Surfaces = append(result.Surfaces, item)
	}
	return nil
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
	for i := range result.Surfaces {
		item := &result.Surfaces[i]
		if item.Action == "create" {
			surface, err := scanTenantSurface(tx.QueryRow(ctx, `insert into tenant_surfaces (account_id, app_id, name, cert_kind)
				values ($1::uuid, $2::uuid, $3, $4) returning `+tenantSurfaceCols,
				in.AccountID, item.Surface.AppID, item.Surface.Name, item.Surface.CertKind))
			if err != nil {
				return applyWriteError(err)
			}
			item.Surface = surface
		}
		if item.Action == "link" || item.Action == "create" {
			if _, err := tx.Exec(ctx, `update tenant_surfaces set platform_tenant_id = $2::uuid
				where id = $1::uuid`, item.Surface.ID, result.Tenant.ID); err != nil {
				return applyWriteError(err)
			}
		}
		for j := range item.Hostnames {
			host := &item.Hostnames[j]
			if host.Action != "create" {
				continue
			}
			created, err := scanTenantHostname(tx.QueryRow(ctx, `insert into tenant_hostnames (surface_id, hostname, challenge_token)
				values ($1::uuid, $2, $3) returning `+tenantHostnameCols,
				item.Surface.ID, host.Hostname.Hostname, host.Hostname.ChallengeToken))
			if err != nil {
				return applyWriteError(err)
			}
			host.Hostname = created
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
