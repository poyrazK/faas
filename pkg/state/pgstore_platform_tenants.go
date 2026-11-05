package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const platformTenantCols = `id, account_id, external_ref, name, status, created_at, updated_at`

func scanPlatformTenant(row pgx.Row) (PlatformTenant, error) {
	var tenant PlatformTenant
	err := row.Scan(&tenant.ID, &tenant.AccountID, &tenant.ExternalRef, &tenant.Name,
		&tenant.Status, &tenant.CreatedAt, &tenant.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return PlatformTenant{}, ErrNotFound
	}
	if err != nil {
		return PlatformTenant{}, err
	}
	if !validPlatformTenantStatus(tenant.Status) {
		return PlatformTenant{}, fmt.Errorf("platform tenant %s has invalid status %q", tenant.ID, tenant.Status)
	}
	return tenant, nil
}

// lockPlatformTenantAccount serializes tenant membership mutations with bundle
// and reconciliation applies, which take the same lock before reading links.
func lockPlatformTenantAccount(ctx context.Context, tx pgx.Tx, accountID string) error {
	var locked string
	if err := tx.QueryRow(ctx, `select id from accounts where id = $1::uuid for update`, accountID).Scan(&locked); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

func (s *PgStore) CreatePlatformTenant(ctx context.Context, accountID, externalRef, name string, limit int) (PlatformTenant, bool, error) {
	if err := validatePlatformTenantInput(accountID, externalRef, name); err != nil {
		return PlatformTenant{}, false, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PlatformTenant{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockPlatformTenantAccount(ctx, tx, accountID); err != nil {
		return PlatformTenant{}, false, err
	}
	tenant, err := scanPlatformTenant(tx.QueryRow(ctx, `
		select `+platformTenantCols+` from platform_tenants
		where account_id = $1::uuid and external_ref = $2`, accountID, externalRef))
	if err == nil {
		if tenant.Name != name {
			return PlatformTenant{}, false, ErrConflict
		}
		return tenant, false, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return PlatformTenant{}, false, err
	}
	var count int
	if err := tx.QueryRow(ctx, `select count(*) from platform_tenants where account_id = $1::uuid`, accountID).Scan(&count); err != nil {
		return PlatformTenant{}, false, err
	}
	if count >= limit {
		return PlatformTenant{}, false, &PlatformTenantQuotaError{Limit: limit, Observed: count}
	}
	tenant, err = scanPlatformTenant(tx.QueryRow(ctx, `
		insert into platform_tenants (account_id, external_ref, name)
		values ($1::uuid, $2, $3) returning `+platformTenantCols, accountID, externalRef, name))
	if err != nil {
		return PlatformTenant{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return PlatformTenant{}, false, err
	}
	return tenant, true, nil
}

func (s *PgStore) GetPlatformTenant(ctx context.Context, accountID, tenantID string) (PlatformTenant, error) {
	if accountID == "" || tenantID == "" {
		return PlatformTenant{}, ErrNotFound
	}
	return scanPlatformTenant(s.pool.QueryRow(ctx, `
		select `+platformTenantCols+` from platform_tenants
		where account_id = $1::uuid and id = $2::uuid`, accountID, tenantID))
}

func (s *PgStore) ResolvePlatformTenantExternalRef(ctx context.Context, accountID, externalRef string) (PlatformTenant, error) {
	if accountID == "" || externalRef == "" || len(externalRef) > 256 {
		return PlatformTenant{}, ErrNotFound
	}
	return scanPlatformTenant(s.pool.QueryRow(ctx, `
		select `+platformTenantCols+` from platform_tenants
		where account_id = $1::uuid and external_ref = $2`, accountID, externalRef))
}

func (s *PgStore) ListPlatformTenants(ctx context.Context, accountID string, limit, offset int) ([]PlatformTenant, error) {
	page, _, err := s.ListPlatformTenantsPage(ctx, accountID, limit, offset, "")
	return page, err
}

// ListPlatformTenantsPage returns a newest-first page. Supplying pageToken
// switches from legacy offset pagination to a stable (created_at, id) cursor.
func (s *PgStore) ListPlatformTenantsPage(ctx context.Context, accountID string, limit, offset int, pageToken string) ([]PlatformTenant, string, error) {
	if accountID == "" {
		return nil, "", ErrNotFound
	}
	if limit < 1 || limit > 100 || offset < 0 {
		return nil, "", ErrInvalidArgument
	}
	var rows pgx.Rows
	var err error
	if pageToken != "" {
		if offset != 0 {
			return nil, "", ErrInvalidArgument
		}
		cursorTime, cursorID, valid := decodePageToken(pageToken)
		if !valid {
			return nil, "", ErrInvalidArgument
		}
		parsedID, parseErr := uuid.Parse(cursorID)
		if parseErr != nil {
			return nil, "", ErrInvalidArgument
		}
		rows, err = s.pool.Query(ctx, `select `+platformTenantCols+` from platform_tenants
			where account_id = $1::uuid and (created_at, id) < ($2::timestamptz, $3::uuid)
			order by created_at desc, id desc limit $4`, accountID, cursorTime, parsedID, limit+1)
	} else {
		rows, err = s.pool.Query(ctx, `select `+platformTenantCols+` from platform_tenants
			where account_id = $1::uuid order by created_at desc, id desc limit $2 offset $3`, accountID, limit+1, offset)
	}
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	out := []PlatformTenant{}
	for rows.Next() {
		tenant, err := scanPlatformTenant(rows)
		if err != nil {
			return nil, "", err
		}
		out = append(out, tenant)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	if len(out) <= limit {
		return out, "", nil
	}
	out = out[:limit]
	last := out[len(out)-1]
	return out, encodePageToken(last.CreatedAt, last.ID), nil
}

func (s *PgStore) SetPlatformTenantStatus(ctx context.Context, accountID, tenantID, status string) (PlatformTenant, error) {
	if !validPlatformTenantStatus(status) {
		return PlatformTenant{}, ErrInvalidArgument
	}
	if status == PlatformTenantActive {
		return s.activateTrafficPlatformTenant(ctx, accountID, tenantID)
	}
	return scanPlatformTenant(s.pool.QueryRow(ctx, `
		update platform_tenants set status = $3, updated_at = now()
		where account_id = $1::uuid and id = $2::uuid
		returning `+platformTenantCols, accountID, tenantID, status))
}

func (s *PgStore) LinkPlatformTenantConsumer(ctx context.Context, accountID, tenantID, consumerID string) (APIConsumer, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return APIConsumer{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockPlatformTenantAccount(ctx, tx, accountID); err != nil {
		return APIConsumer{}, err
	}
	c, err := scanAPIConsumerRow(tx.QueryRow(ctx, `
		update api_consumers c set platform_tenant_id = $2::uuid
		where c.account_id = $1::uuid and c.id = $3::uuid
		  and (c.platform_tenant_id is null or c.platform_tenant_id = $2::uuid)
		  and exists (select 1 from platform_tenants t where t.account_id = $1::uuid and t.id = $2::uuid)
		returning `+apiConsumerSelectCols, accountID, tenantID, consumerID))
	if errors.Is(err, pgx.ErrNoRows) {
		if _, getErr := scanPlatformTenant(tx.QueryRow(ctx, `select `+platformTenantCols+`
			from platform_tenants where account_id = $1::uuid and id = $2::uuid`, accountID, tenantID)); getErr != nil {
			return APIConsumer{}, getErr
		}
		if _, getErr := scanAPIConsumerRow(tx.QueryRow(ctx, `select `+apiConsumerSelectCols+`
			from api_consumers where account_id = $1::uuid and id = $2::uuid`, accountID, consumerID)); getErr != nil {
			return APIConsumer{}, applyNoRows(getErr)
		}
		return APIConsumer{}, ErrConflict
	}
	if err != nil {
		return APIConsumer{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return APIConsumer{}, err
	}
	return c, nil
}

func (s *PgStore) LinkPlatformTenantSurface(ctx context.Context, accountID, tenantID, surfaceID string) (TenantSurface, error) {
	tx, err := s.beginTrafficTenantBinding(ctx, accountID, nil)
	if err != nil {
		return TenantSurface{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := lockPlatformTenantAccount(ctx, tx, accountID); err != nil {
		return TenantSurface{}, err
	}
	surface, err := scanTenantSurface(tx.QueryRow(ctx, `
		update tenant_surfaces s set platform_tenant_id = $2::uuid
		where s.account_id = $1::uuid and s.id = $3::uuid
		  and s.status <> 'deleted'
		  and (s.platform_tenant_id is null or s.platform_tenant_id = $2::uuid)
		  and exists (select 1 from platform_tenants t where t.account_id = $1::uuid and t.id = $2::uuid)
		returning `+tenantSurfaceCols, accountID, tenantID, surfaceID))
	if errors.Is(err, ErrNotFound) {
		if _, getErr := scanPlatformTenant(tx.QueryRow(ctx, `select `+platformTenantCols+`
			from platform_tenants where account_id = $1::uuid and id = $2::uuid`, accountID, tenantID)); getErr != nil {
			return TenantSurface{}, getErr
		}
		existing, getErr := scanTenantSurface(tx.QueryRow(ctx, `select `+tenantSurfaceCols+`
			from tenant_surfaces where id = $1::uuid`, surfaceID))
		if getErr != nil || existing.AccountID != accountID || existing.Status == SurfaceStatusDeleted {
			return TenantSurface{}, ErrNotFound
		}
		return TenantSurface{}, ErrConflict
	}
	if err != nil {
		return TenantSurface{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return TenantSurface{}, err
	}
	return surface, nil
}

func (s *PgStore) ListPlatformTenantConsumers(ctx context.Context, accountID, tenantID string) ([]APIConsumer, error) {
	if _, err := s.GetPlatformTenant(ctx, accountID, tenantID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `select `+apiConsumerSelectCols+` from api_consumers
		where account_id = $1::uuid and platform_tenant_id = $2::uuid
		order by app_id, id`, accountID, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []APIConsumer{}
	for rows.Next() {
		c, err := scanAPIConsumerRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *PgStore) ListPlatformTenantSurfaces(ctx context.Context, accountID, tenantID string) ([]TenantSurface, error) {
	if _, err := s.GetPlatformTenant(ctx, accountID, tenantID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `select `+tenantSurfaceCols+` from tenant_surfaces
		where account_id = $1::uuid and platform_tenant_id = $2::uuid and status <> 'deleted'
		order by app_id, id`, accountID, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TenantSurface{}
	for rows.Next() {
		surface, err := scanTenantSurface(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, surface)
	}
	return out, rows.Err()
}

func (s *PgStore) ListPlatformTenantUsage(ctx context.Context, accountID, tenantID string, since, until time.Time) ([]APIConsumerUsageBucket, error) {
	if !until.After(since) {
		return nil, ErrInvalidArgument
	}
	if _, err := s.GetPlatformTenant(ctx, accountID, tenantID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		select u.account_id, u.app_id,
		       case when u.source_kind = 'consumer' then u.consumer_key else '' end,
		       case when u.source_kind = 'surface' then u.consumer_key else '' end,
		       case when u.source_kind = 'jwt' then u.consumer_key else '' end,
		       date_trunc('day', u.window_start, 'UTC') as usage_day,
		       sum(u.request_count)::bigint, sum(u.error_count)::bigint, sum(u.billable_units)::bigint
		from platform_tenant_usage_minutes u
		where u.platform_tenant_id = $2::uuid and u.account_id = $1::uuid
		  and u.window_start >= $3 and u.window_start < $4
		group by u.account_id, u.app_id, u.source_kind, u.consumer_key, usage_day
		order by usage_day, u.app_id, u.source_kind, u.consumer_key`, accountID, tenantID, since.UTC(), until.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []APIConsumerUsageBucket{}
	for rows.Next() {
		var bucket APIConsumerUsageBucket
		if err := rows.Scan(&bucket.AccountID, &bucket.AppID, &bucket.ConsumerKey, &bucket.SurfaceID, &bucket.JWTAuthorizationRuleID,
			&bucket.WindowStart, &bucket.RequestCount, &bucket.ErrorCount, &bucket.BillableUnits); err != nil {
			return nil, err
		}
		out = append(out, bucket)
	}
	return out, rows.Err()
}

func (s *PgStore) PlatformTenantSurfaceSuspended(ctx context.Context, surfaceID string) (bool, error) {
	if surfaceID == "" {
		return false, ErrNotFound
	}
	var suspended bool
	err := s.pool.QueryRow(ctx, `select coalesce(t.status = 'suspended', false)
		from tenant_surfaces s left join platform_tenants t on t.id = s.platform_tenant_id
		where s.id = $1::uuid`, surfaceID).Scan(&suspended)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrNotFound
	}
	return suspended, err
}

// PlatformTenantHostBinding is a single indexed read used to revalidate warm
// gateway routes. In particular, suspending a tenant must take effect without
// waiting for a hostname cache eviction or an asynchronous notification.
func (s *PgStore) PlatformTenantHostBinding(ctx context.Context, host string) (PlatformTenantHostBinding, error) {
	var b PlatformTenantHostBinding
	err := s.pool.QueryRow(ctx, `select s.id, s.app_id, s.account_id,
		coalesce(s.platform_tenant_id::text, ''), s.status = 'active',
		h.verified_at is not null, coalesce(t.status = 'suspended', false)
		from tenant_hostnames h join tenant_surfaces s on s.id = h.surface_id
		left join platform_tenants t on t.id = s.platform_tenant_id
		where h.hostname = $1 and s.status <> 'deleted'`, host).Scan(
		&b.SurfaceID, &b.AppID, &b.AccountID, &b.TenantID,
		&b.Active, &b.Verified, &b.Suspended)
	if errors.Is(err, pgx.ErrNoRows) {
		return PlatformTenantHostBinding{}, ErrNotFound
	}
	return b, err
}
