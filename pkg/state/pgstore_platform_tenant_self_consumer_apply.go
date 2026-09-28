package state

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

func (s *PgStore) ApplyPlatformTenantSelfConsumers(ctx context.Context, in ApplyPlatformTenantSelfConsumersParams) (PlatformTenantSelfConsumersResult, error) {
	in, err := normalizePlatformTenantSelfConsumerApply(in)
	if err != nil {
		return PlatformTenantSelfConsumersResult{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PlatformTenantSelfConsumersResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var tenantStatus string
	if err := tx.QueryRow(ctx, `select status from platform_tenants
		where id = $1::uuid and account_id = $2::uuid for update`, in.TenantID, in.AccountID).Scan(&tenantStatus); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PlatformTenantSelfConsumersResult{}, ErrNotFound
		}
		return PlatformTenantSelfConsumersResult{}, err
	}
	if tenantStatus != PlatformTenantActive {
		return PlatformTenantSelfConsumersResult{}, ErrConflict
	}

	rows, err := tx.Query(ctx, `select s.id::text, s.app_id::text, s.platform_tenant_id::text, s.status
		from tenant_surfaces s join apps a on a.id = s.app_id and a.account_id = s.account_id
		where s.account_id = $1::uuid and s.id = any($2::uuid[])
		order by s.id for update of s`, in.AccountID, in.SurfaceIDs)
	if err != nil {
		return PlatformTenantSelfConsumersResult{}, err
	}
	type selectedSurface struct {
		id     string
		appID  string
		tenant *string
		status string
	}
	surfaces := make([]selectedSurface, 0, len(in.SurfaceIDs))
	for rows.Next() {
		var surface selectedSurface
		if err := rows.Scan(&surface.id, &surface.appID, &surface.tenant, &surface.status); err != nil {
			rows.Close()
			return PlatformTenantSelfConsumersResult{}, err
		}
		surfaces = append(surfaces, surface)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return PlatformTenantSelfConsumersResult{}, err
	}
	rows.Close()
	if len(surfaces) != len(in.SurfaceIDs) {
		return PlatformTenantSelfConsumersResult{}, ErrNotFound
	}
	seenApps := make(map[string]struct{}, len(surfaces))
	for _, surface := range surfaces {
		if surface.tenant == nil || *surface.tenant != in.TenantID || SurfaceStatus(surface.status) != SurfaceStatusActive {
			return PlatformTenantSelfConsumersResult{}, ErrNotFound
		}
		if _, duplicate := seenApps[surface.appID]; duplicate {
			return PlatformTenantSelfConsumersResult{}, ErrInvalidArgument
		}
		seenApps[surface.appID] = struct{}{}
	}

	result := PlatformTenantSelfConsumersResult{Consumers: make([]PlatformTenantSelfConsumerApplyItem, 0, len(surfaces))}
	newItems := make([]int, 0, len(surfaces))
	for _, surface := range surfaces {
		item := PlatformTenantSelfConsumerApplyItem{SurfaceID: surface.id}
		consumer, err := scanAPIConsumerRow(tx.QueryRow(ctx, `select `+apiConsumerSelectCols+`
			from api_consumers where account_id = $1::uuid and app_id = $2::uuid and external_ref = $3 for update`,
			in.AccountID, surface.appID, in.ExternalRef))
		if err == nil {
			if !samePlatformTenantSelfConsumer(consumer, in.TenantID, in.Name) {
				return PlatformTenantSelfConsumersResult{}, ErrConflict
			}
			item.Consumer = consumer
		} else if errors.Is(err, pgx.ErrNoRows) {
			item.Consumer = APIConsumer{AccountID: in.AccountID, AppID: surface.appID,
				PlatformTenantID: in.TenantID, ExternalRef: in.ExternalRef, Name: in.Name,
				Status: APIConsumerStatusActive}
			item.Created = true
			newItems = append(newItems, len(result.Consumers))
		} else {
			return PlatformTenantSelfConsumersResult{}, err
		}
		result.Consumers = append(result.Consumers, item)
	}
	if len(newItems) == 0 {
		if err := tx.Commit(ctx); err != nil {
			return PlatformTenantSelfConsumersResult{}, err
		}
		return result, nil
	}

	var enabled bool
	var limit int
	err = tx.QueryRow(ctx, `select enabled, max_consumers
		from platform_tenant_consumer_provisioning_policies
		where account_id = $1::uuid and tenant_id = $2::uuid for update`, in.AccountID, in.TenantID).
		Scan(&enabled, &limit)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !enabled {
		return PlatformTenantSelfConsumersResult{}, ErrPlatformTenantConsumerProvisioningDisabled
	}
	if err != nil {
		return PlatformTenantSelfConsumersResult{}, err
	}
	var active int
	if err := tx.QueryRow(ctx, `select count(*) from api_consumers
		where account_id = $1::uuid and platform_tenant_id = $2::uuid and status = 'active' and revoked_at is null`,
		in.AccountID, in.TenantID).Scan(&active); err != nil {
		return PlatformTenantSelfConsumersResult{}, err
	}
	if active+len(newItems) > limit {
		return PlatformTenantSelfConsumersResult{}, &PlatformTenantConsumerProvisioningQuotaError{
			Limit: limit, Observed: active + len(newItems),
		}
	}
	if in.DryRun {
		if err := tx.Commit(ctx); err != nil {
			return PlatformTenantSelfConsumersResult{}, err
		}
		return result, nil
	}

	for _, index := range newItems {
		consumer, err := scanAPIConsumerRow(tx.QueryRow(ctx, `insert into api_consumers
			(account_id, app_id, external_ref, name, platform_tenant_id)
			values ($1::uuid, $2::uuid, $3, $4, $5::uuid)
			on conflict (app_id, external_ref) do nothing
			returning `+apiConsumerSelectCols, in.AccountID, result.Consumers[index].Consumer.AppID,
			in.ExternalRef, in.Name, in.TenantID))
		if errors.Is(err, pgx.ErrNoRows) {
			// Resolve an account-owner create that raced this bundle, but never
			// adopt a different name, revoked row, or other tenant's identity.
			consumer, err = scanAPIConsumerRow(tx.QueryRow(ctx, `select `+apiConsumerSelectCols+`
				from api_consumers where account_id = $1::uuid and app_id = $2::uuid and external_ref = $3 for update`,
				in.AccountID, result.Consumers[index].Consumer.AppID, in.ExternalRef))
			if err != nil {
				return PlatformTenantSelfConsumersResult{}, err
			}
			if !samePlatformTenantSelfConsumer(consumer, in.TenantID, in.Name) {
				return PlatformTenantSelfConsumersResult{}, ErrConflict
			}
			result.Consumers[index].Created = false
		} else if err != nil {
			return PlatformTenantSelfConsumersResult{}, err
		}
		result.Consumers[index].Consumer = consumer
	}
	if err := tx.Commit(ctx); err != nil {
		return PlatformTenantSelfConsumersResult{}, err
	}
	for _, item := range result.Consumers {
		result.Changed = result.Changed || item.Created
	}
	return result, nil
}
