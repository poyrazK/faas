package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

const platformTenantBudgetCols = `max_requests_per_minute, max_requests_per_day,
	minute_start, minute_used, day_start, day_used, updated_at`

func scanPlatformTenantBudget(row pgx.Row, tenantID string) (platformTenantBudgetRow, error) {
	var out platformTenantBudgetRow
	var minuteStart, dayStart *time.Time
	err := row.Scan(&out.MaxRequestsPerMinute, &out.MaxRequestsPerDay,
		&minuteStart, &out.MinuteUsed, &dayStart, &out.DayUsed, &out.UpdatedAt)
	if err != nil {
		return platformTenantBudgetRow{}, err
	}
	out.TenantID = tenantID
	if minuteStart != nil {
		out.MinuteStart = minuteStart.UTC()
	}
	if dayStart != nil {
		out.DayStart = dayStart.UTC()
	}
	return out, nil
}

func (s *PgStore) GetPlatformTenantRequestBudget(ctx context.Context, accountID, tenantID string) (PlatformTenantRequestBudget, error) {
	if !validPlatformTenantBudget(accountID, tenantID, 0, 0) {
		return PlatformTenantRequestBudget{}, ErrInvalidArgument
	}
	if _, err := s.GetPlatformTenant(ctx, accountID, tenantID); err != nil {
		return PlatformTenantRequestBudget{}, err
	}
	row, err := scanPlatformTenantBudget(s.pool.QueryRow(ctx, `select `+platformTenantBudgetCols+`
		from platform_tenant_request_budgets where account_id = $1::uuid and tenant_id = $2::uuid`,
		accountID, tenantID), tenantID)
	if errors.Is(err, pgx.ErrNoRows) {
		return (platformTenantBudgetRow{TenantID: tenantID}).snapshot(time.Now().UTC()), nil
	}
	if err != nil {
		return PlatformTenantRequestBudget{}, fmt.Errorf("read platform tenant request budget: %w", err)
	}
	return row.snapshot(time.Now().UTC()), nil
}

func (s *PgStore) SetPlatformTenantRequestBudget(ctx context.Context, accountID, tenantID string, minute, day int64) (PlatformTenantRequestBudget, error) {
	if !validPlatformTenantBudget(accountID, tenantID, minute, day) {
		return PlatformTenantRequestBudget{}, ErrInvalidArgument
	}
	_, err := scanPlatformTenantBudget(s.pool.QueryRow(ctx, `
		insert into platform_tenant_request_budgets
		       (account_id, tenant_id, max_requests_per_minute, max_requests_per_day)
		select account_id, id, $3, $4 from platform_tenants
		where account_id = $1::uuid and id = $2::uuid
		on conflict (tenant_id) do update
		set max_requests_per_minute = excluded.max_requests_per_minute,
		    max_requests_per_day = excluded.max_requests_per_day,
		    updated_at = now()
		where (platform_tenant_request_budgets.max_requests_per_minute,
		       platform_tenant_request_budgets.max_requests_per_day)
		  is distinct from (excluded.max_requests_per_minute, excluded.max_requests_per_day)
		returning `+platformTenantBudgetCols, accountID, tenantID, minute, day), tenantID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return PlatformTenantRequestBudget{}, fmt.Errorf("set platform tenant request budget: %w", err)
	}
	// A no-op replay returns no row from the guarded upsert. Reading back also
	// maps an unknown or cross-account tenant to the usual ErrNotFound.
	return s.GetPlatformTenantRequestBudget(ctx, accountID, tenantID)
}

func (s *PgStore) AdmitPlatformTenantRequest(ctx context.Context, accountID, tenantID string) (PlatformTenantBudgetDecision, error) {
	if !validPlatformTenantBudget(accountID, tenantID, 0, 0) {
		return PlatformTenantBudgetDecision{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PlatformTenantBudgetDecision{}, fmt.Errorf("begin platform tenant request admission: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// This single tenant row is the cross-replica serialization point. Policy
	// changes lock the same row; we never fall back to a process-local bucket.
	row, err := scanPlatformTenantBudget(tx.QueryRow(ctx, `select `+platformTenantBudgetCols+`
		from platform_tenant_request_budgets
		where account_id = $1::uuid and tenant_id = $2::uuid for update`, accountID, tenantID), tenantID)
	if errors.Is(err, pgx.ErrNoRows) {
		return PlatformTenantBudgetDecision{Allowed: true}, nil
	}
	if err != nil {
		return PlatformTenantBudgetDecision{}, fmt.Errorf("lock platform tenant request budget: %w", err)
	}
	if row.MaxRequestsPerMinute == 0 && row.MaxRequestsPerDay == 0 {
		return PlatformTenantBudgetDecision{Allowed: true}, nil
	}
	var at time.Time
	if err := tx.QueryRow(ctx, `select clock_timestamp()`).Scan(&at); err != nil {
		return PlatformTenantBudgetDecision{}, fmt.Errorf("read platform tenant admission clock: %w", err)
	}
	row, decision := row.decide(at)
	if !decision.Allowed {
		return decision, nil
	}
	if _, err := tx.Exec(ctx, `update platform_tenant_request_budgets
		set minute_start = $3, minute_used = $4, day_start = $5, day_used = $6
		where account_id = $1::uuid and tenant_id = $2::uuid`,
		accountID, tenantID, row.MinuteStart, row.MinuteUsed, row.DayStart, row.DayUsed); err != nil {
		return PlatformTenantBudgetDecision{}, fmt.Errorf("consume platform tenant request budget: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return PlatformTenantBudgetDecision{}, fmt.Errorf("commit platform tenant request admission: %w", err)
	}
	return decision, nil
}
