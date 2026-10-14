package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/onebox-faas/faas/pkg/api"
)

const platformTenantPlanCols = `id, account_id, name, currency, price_millicents_per_unit, included_units_per_month,
	tiers, route_weights, max_requests_per_minute, max_requests_per_day, created_at, updated_at, unit, tier_mode`

func scanPlatformTenantPlan(row pgx.Row) (PlatformTenantPlan, error) {
	var p PlatformTenantPlan
	var tiers, weights []byte
	err := row.Scan(&p.ID, &p.AccountID, &p.Name, &p.Currency, &p.PriceMillicentsPerUnit, &p.IncludedUnitsPerMonth,
		&tiers, &weights, &p.MaxRequestsPerMinute, &p.MaxRequestsPerDay, &p.CreatedAt, &p.UpdatedAt, &p.Unit, &p.TierMode)
	if errors.Is(err, pgx.ErrNoRows) {
		return PlatformTenantPlan{}, ErrNotFound
	}
	if err != nil {
		return PlatformTenantPlan{}, err
	}
	if err := json.Unmarshal(tiers, &p.Tiers); err != nil {
		return PlatformTenantPlan{}, fmt.Errorf("decode plan tiers: %w", err)
	}
	if err := json.Unmarshal(weights, &p.RouteWeights); err != nil {
		return PlatformTenantPlan{}, fmt.Errorf("decode plan route weights: %w", err)
	}
	if len(p.Tiers) == 0 {
		p.Tiers = nil
	}
	if len(p.RouteWeights) == 0 {
		p.RouteWeights = nil
	}
	p.CreatedAt, p.UpdatedAt = p.CreatedAt.UTC(), p.UpdatedAt.UTC()
	return p, nil
}

func platformTenantPlanJSON(p PlatformTenantPlan) (string, string, error) {
	tiers, weights := []byte("[]"), []byte("{}")
	var err error
	if len(p.Tiers) > 0 {
		if tiers, err = json.Marshal(p.Tiers); err != nil {
			return "", "", err
		}
	}
	if len(p.RouteWeights) > 0 {
		if weights, err = json.Marshal(p.RouteWeights); err != nil {
			return "", "", err
		}
	}
	return string(tiers), string(weights), nil
}

func uniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgerrcode.UniqueViolation
}

// CreatePlatformTenantPlan locks the account row so concurrent creates
// cannot both pass the plan limit.
func (s *PgStore) CreatePlatformTenantPlan(ctx context.Context, p PlatformTenantPlan) (PlatformTenantPlan, error) {
	if err := ValidatePlatformTenantPlan(p); err != nil {
		return PlatformTenantPlan{}, err
	}
	p = normalizePlatformTenantPlan(p)
	tiers, weights, err := platformTenantPlanJSON(p)
	if err != nil {
		return PlatformTenantPlan{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PlatformTenantPlan{}, fmt.Errorf("begin tenant plan: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var count int
	if err := tx.QueryRow(ctx, `select (select count(*) from platform_tenant_plans where account_id = a.id)
		from accounts a where a.id = $1::uuid for update`, p.AccountID).Scan(&count); err != nil {
		return PlatformTenantPlan{}, fmt.Errorf("lock account for tenant plan: %w", err)
	}
	if count >= api.PlatformTenantPlansMax {
		return PlatformTenantPlan{}, ErrPlatformTenantPlanLimit
	}
	created, err := scanPlatformTenantPlan(tx.QueryRow(ctx, `insert into platform_tenant_plans
			(account_id, name, currency, price_millicents_per_unit, included_units_per_month, tiers, route_weights,
			 max_requests_per_minute, max_requests_per_day, unit, tier_mode)
		values ($1::uuid, $2, $3, $4, $5, $6::jsonb, $7::jsonb, $8, $9, $10, $11)
		returning `+platformTenantPlanCols, p.AccountID, p.Name, p.Currency, p.PriceMillicentsPerUnit, p.IncludedUnitsPerMonth,
		tiers, weights, p.MaxRequestsPerMinute, p.MaxRequestsPerDay, p.Unit, p.TierMode))
	if uniqueViolation(err) {
		return PlatformTenantPlan{}, ErrConflict
	}
	if err != nil {
		return PlatformTenantPlan{}, fmt.Errorf("insert tenant plan: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return PlatformTenantPlan{}, fmt.Errorf("commit tenant plan: %w", err)
	}
	return created, nil
}

func (s *PgStore) ListPlatformTenantPlans(ctx context.Context, accountID string) ([]PlatformTenantPlan, error) {
	rows, err := s.pool.Query(ctx, `select `+platformTenantPlanCols+` from platform_tenant_plans
		where account_id = $1::uuid order by name`, accountID)
	if err != nil {
		return nil, fmt.Errorf("list tenant plans: %w", err)
	}
	defer rows.Close()
	out := []PlatformTenantPlan{}
	for rows.Next() {
		p, err := scanPlatformTenantPlan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *PgStore) GetPlatformTenantPlan(ctx context.Context, accountID, planID string) (PlatformTenantPlan, error) {
	p, err := scanPlatformTenantPlan(s.pool.QueryRow(ctx, `select `+platformTenantPlanCols+` from platform_tenant_plans
		where account_id = $1::uuid and id = $2::uuid`, accountID, planID))
	if err != nil && !errors.Is(err, ErrNotFound) {
		return PlatformTenantPlan{}, fmt.Errorf("get tenant plan: %w", err)
	}
	return p, err
}

func (s *PgStore) UpdatePlatformTenantPlan(ctx context.Context, p PlatformTenantPlan) (PlatformTenantPlan, error) {
	if err := ValidatePlatformTenantPlan(p); err != nil {
		return PlatformTenantPlan{}, err
	}
	p = normalizePlatformTenantPlan(p)
	tiers, weights, err := platformTenantPlanJSON(p)
	if err != nil {
		return PlatformTenantPlan{}, err
	}
	updated, err := scanPlatformTenantPlan(s.pool.QueryRow(ctx, `update platform_tenant_plans
		   set name = $3, currency = $4, price_millicents_per_unit = $5, included_units_per_month = $6,
		       tiers = $7::jsonb, route_weights = $8::jsonb, max_requests_per_minute = $9, max_requests_per_day = $10,
		       unit = $11, tier_mode = $12, updated_at = now()
		 where account_id = $1::uuid and id = $2::uuid
		returning `+platformTenantPlanCols, p.AccountID, p.ID, p.Name, p.Currency, p.PriceMillicentsPerUnit,
		p.IncludedUnitsPerMonth, tiers, weights, p.MaxRequestsPerMinute, p.MaxRequestsPerDay, p.Unit, p.TierMode))
	if uniqueViolation(err) {
		return PlatformTenantPlan{}, ErrConflict
	}
	if err != nil && !errors.Is(err, ErrNotFound) {
		return PlatformTenantPlan{}, fmt.Errorf("update tenant plan: %w", err)
	}
	return updated, err
}

const platformTenantPlanAssignmentCols = `id, account_id, tenant_id, plan_id, rate_card_id, effective_from, created_at`

func scanPlatformTenantPlanAssignment(row pgx.Row) (PlatformTenantPlanAssignment, error) {
	var a PlatformTenantPlanAssignment
	err := row.Scan(&a.ID, &a.AccountID, &a.TenantID, &a.PlanID, &a.RateCardID, &a.EffectiveFrom, &a.CreatedAt)
	if err != nil {
		return PlatformTenantPlanAssignment{}, err
	}
	a.EffectiveFrom, a.CreatedAt = a.EffectiveFrom.UTC(), a.CreatedAt.UTC()
	return a, nil
}

func (s *PgStore) RecordPlatformTenantPlanAssignment(ctx context.Context, a PlatformTenantPlanAssignment) (PlatformTenantPlanAssignment, error) {
	out, err := scanPlatformTenantPlanAssignment(s.pool.QueryRow(ctx, `insert into platform_tenant_plan_assignments
			(account_id, tenant_id, plan_id, rate_card_id, effective_from)
		select p.account_id, $3::uuid, p.id, $4::uuid, $5 from platform_tenant_plans p
		 where p.account_id = $1::uuid and p.id = $2::uuid
		returning `+platformTenantPlanAssignmentCols, a.AccountID, a.PlanID, a.TenantID, a.RateCardID, a.EffectiveFrom.UTC()))
	if errors.Is(err, pgx.ErrNoRows) {
		return PlatformTenantPlanAssignment{}, ErrNotFound
	}
	if err != nil {
		return PlatformTenantPlanAssignment{}, fmt.Errorf("record tenant plan assignment: %w", err)
	}
	return out, nil
}

func (s *PgStore) ListPlatformTenantPlanAssignments(ctx context.Context, accountID, tenantID string) ([]PlatformTenantPlanAssignment, error) {
	rows, err := s.pool.Query(ctx, `select `+platformTenantPlanAssignmentCols+` from platform_tenant_plan_assignments
		where account_id = $1::uuid and tenant_id = $2::uuid order by created_at desc, id desc`, accountID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list tenant plan assignments: %w", err)
	}
	defer rows.Close()
	out := []PlatformTenantPlanAssignment{}
	for rows.Next() {
		a, err := scanPlatformTenantPlanAssignment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
