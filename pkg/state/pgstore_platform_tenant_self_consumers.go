package state

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

func (s *PgStore) CreatePlatformTenantSelfConsumer(ctx context.Context, in CreatePlatformTenantSelfConsumerParams) (APIConsumer, bool, error) {
	if err := validatePlatformTenantSelfConsumerInput(in); err != nil {
		return APIConsumer{}, false, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return APIConsumer{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var tenantStatus string
	if err := tx.QueryRow(ctx, `select status from platform_tenants
		where id = $1::uuid and account_id = $2::uuid for update`, in.TenantID, in.AccountID).Scan(&tenantStatus); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return APIConsumer{}, false, ErrNotFound
		}
		return APIConsumer{}, false, err
	}
	if tenantStatus != PlatformTenantActive {
		return APIConsumer{}, false, ErrConflict
	}

	var appID, surfaceStatus string
	var surfaceTenantID *string
	err = tx.QueryRow(ctx, `select s.app_id::text, s.platform_tenant_id::text, s.status
		from tenant_surfaces s join apps a on a.id = s.app_id and a.account_id = s.account_id
		where s.id = $1::uuid and s.account_id = $2::uuid for update of s`, in.SurfaceID, in.AccountID).
		Scan(&appID, &surfaceTenantID, &surfaceStatus)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return APIConsumer{}, false, ErrNotFound
		}
		return APIConsumer{}, false, err
	}
	if surfaceTenantID == nil || *surfaceTenantID != in.TenantID || SurfaceStatus(surfaceStatus) != SurfaceStatusActive {
		return APIConsumer{}, false, ErrNotFound
	}

	// The unique identity is per app. A matching, already-linked active row is
	// a safe replay and does not need provisioning to remain enabled.
	existing, err := scanAPIConsumerRow(tx.QueryRow(ctx, `select `+apiConsumerSelectCols+`
		from api_consumers where account_id = $1::uuid and app_id = $2::uuid and external_ref = $3 for update`,
		in.AccountID, appID, in.ExternalRef))
	if err == nil {
		if !samePlatformTenantSelfConsumer(existing, in.TenantID, in.Name) {
			return APIConsumer{}, false, ErrConflict
		}
		if err := tx.Commit(ctx); err != nil {
			return APIConsumer{}, false, err
		}
		return existing, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return APIConsumer{}, false, err
	}

	var enabled bool
	var limit int
	err = tx.QueryRow(ctx, `select enabled, max_consumers
		from platform_tenant_consumer_provisioning_policies
		where account_id = $1::uuid and tenant_id = $2::uuid for update`, in.AccountID, in.TenantID).
		Scan(&enabled, &limit)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !enabled {
		return APIConsumer{}, false, ErrPlatformTenantConsumerProvisioningDisabled
	}
	if err != nil {
		return APIConsumer{}, false, err
	}

	var active int
	if err := tx.QueryRow(ctx, `select count(*) from api_consumers
		where account_id = $1::uuid and platform_tenant_id = $2::uuid and status = 'active' and revoked_at is null`,
		in.AccountID, in.TenantID).Scan(&active); err != nil {
		return APIConsumer{}, false, err
	}
	if active >= limit {
		return APIConsumer{}, false, &PlatformTenantConsumerProvisioningQuotaError{Limit: limit, Observed: active}
	}

	created, err := scanAPIConsumerRow(tx.QueryRow(ctx, `insert into api_consumers
		(account_id, app_id, external_ref, name, platform_tenant_id)
		values ($1::uuid, $2::uuid, $3, $4, $5::uuid)
		on conflict (app_id, external_ref) do nothing
		returning `+apiConsumerSelectCols, in.AccountID, appID, in.ExternalRef, in.Name, in.TenantID))
	if errors.Is(err, pgx.ErrNoRows) {
		// An owner-side create can race this transaction without taking the
		// tenant row lock. Resolve only an exact same-tenant replay; never adopt
		// an unlinked or differently named consumer implicitly.
		existing, readErr := scanAPIConsumerRow(tx.QueryRow(ctx, `select `+apiConsumerSelectCols+`
			from api_consumers where account_id = $1::uuid and app_id = $2::uuid and external_ref = $3 for update`,
			in.AccountID, appID, in.ExternalRef))
		if readErr != nil {
			return APIConsumer{}, false, readErr
		}
		if !samePlatformTenantSelfConsumer(existing, in.TenantID, in.Name) {
			return APIConsumer{}, false, ErrConflict
		}
		if err := tx.Commit(ctx); err != nil {
			return APIConsumer{}, false, err
		}
		return existing, false, nil
	}
	if err != nil {
		return APIConsumer{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return APIConsumer{}, false, err
	}
	return created, true, nil
}
