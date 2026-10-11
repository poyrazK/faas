package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const apiConsumerPlanCols = `id, account_id, app_id, name, max_requests_per_minute, max_units_per_month, alert_thresholds_percent, created_at, updated_at`

func scanAPIConsumerPlan(row pgx.Row) (APIConsumerPlan, error) {
	var plan APIConsumerPlan
	err := row.Scan(&plan.ID, &plan.AccountID, &plan.AppID, &plan.Name,
		&plan.MaxRequestsPerMinute, &plan.MaxUnitsPerMonth, &plan.AlertThresholdsPercent, &plan.CreatedAt, &plan.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return APIConsumerPlan{}, ErrNotFound
	}
	plan.CreatedAt, plan.UpdatedAt = plan.CreatedAt.UTC(), plan.UpdatedAt.UTC()
	return plan, err
}

func (s *PgStore) CreateAPIConsumerPlan(ctx context.Context, plan APIConsumerPlan) (APIConsumerPlan, error) {
	if err := ValidateAPIConsumerPlan(plan); err != nil {
		return APIConsumerPlan{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return APIConsumerPlan{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Serialize plan creation per app so the count bound holds.
	var count int
	if err := tx.QueryRow(ctx, `select count(*) from api_consumer_plans
		where app_id = (select id from apps where id = $1::uuid and account_id = $2::uuid for update)`,
		plan.AppID, plan.AccountID).Scan(&count); err != nil {
		return APIConsumerPlan{}, err
	}
	if count >= MaxAPIConsumerPlansPerApp {
		return APIConsumerPlan{}, ErrConflict
	}
	out, err := scanAPIConsumerPlan(tx.QueryRow(ctx, `insert into api_consumer_plans
		(account_id, app_id, name, max_requests_per_minute, max_units_per_month, alert_thresholds_percent)
		values ($1::uuid, $2::uuid, $3, $4, $5, $6) returning `+apiConsumerPlanCols,
		plan.AccountID, plan.AppID, plan.Name, plan.MaxRequestsPerMinute, plan.MaxUnitsPerMonth,
		normalizeAPIConsumerPlanAlerts(plan.AlertThresholdsPercent)))
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return APIConsumerPlan{}, ErrConflict
		}
		return APIConsumerPlan{}, err
	}
	return out, tx.Commit(ctx)
}

func (s *PgStore) UpdateAPIConsumerPlanLimits(ctx context.Context, accountID, appID, planID string, perMinute, perMonth int64, alertThresholds []int32) (APIConsumerPlan, error) {
	if perMinute < 0 || perMonth < 0 {
		return APIConsumerPlan{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return APIConsumerPlan{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	plan, err := scanAPIConsumerPlan(tx.QueryRow(ctx, `select `+apiConsumerPlanCols+` from api_consumer_plans
		where id = $1::uuid and account_id = $2::uuid and app_id = $3::uuid for update`, planID, accountID, appID))
	if err != nil {
		return APIConsumerPlan{}, err
	}
	plan.MaxRequestsPerMinute, plan.MaxUnitsPerMonth = perMinute, perMonth
	if alertThresholds != nil {
		plan.AlertThresholdsPercent = normalizeAPIConsumerPlanAlerts(alertThresholds)
	}
	if err := ValidateAPIConsumerPlan(plan); err != nil {
		return APIConsumerPlan{}, fmt.Errorf("%w: %w", ErrInvalidArgument, err)
	}
	out, err := scanAPIConsumerPlan(tx.QueryRow(ctx, `update api_consumer_plans
		set max_requests_per_minute = $2, max_units_per_month = $3, alert_thresholds_percent = $4, updated_at = now()
		where id = $1::uuid returning `+apiConsumerPlanCols, planID, perMinute, perMonth, plan.AlertThresholdsPercent))
	if err != nil {
		return APIConsumerPlan{}, err
	}
	return out, tx.Commit(ctx)
}

func (s *PgStore) GetAPIConsumerPlan(ctx context.Context, accountID, appID, planID string) (APIConsumerPlan, error) {
	return scanAPIConsumerPlan(s.pool.QueryRow(ctx, `select `+apiConsumerPlanCols+` from api_consumer_plans
		where id = $1::uuid and account_id = $2::uuid and app_id = $3::uuid`, planID, accountID, appID))
}

func (s *PgStore) ListAPIConsumerPlans(ctx context.Context, accountID, appID string) ([]APIConsumerPlan, error) {
	rows, err := s.pool.Query(ctx, `select `+apiConsumerPlanCols+` from api_consumer_plans
		where account_id = $1::uuid and app_id = $2::uuid order by name`, accountID, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []APIConsumerPlan{}
	for rows.Next() {
		plan, err := scanAPIConsumerPlan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, plan)
	}
	return out, rows.Err()
}

const apiConsumerPlanAssignmentCols = `id, account_id, app_id, consumer_id, coalesce(plan_id::text, ''), effective_from, created_at`

func scanAPIConsumerPlanAssignment(row pgx.Row) (APIConsumerPlanAssignment, error) {
	var a APIConsumerPlanAssignment
	err := row.Scan(&a.ID, &a.AccountID, &a.AppID, &a.ConsumerID, &a.PlanID, &a.EffectiveFrom, &a.CreatedAt)
	a.EffectiveFrom, a.CreatedAt = a.EffectiveFrom.UTC(), a.CreatedAt.UTC()
	return a, err
}

func (s *PgStore) AssignAPIConsumerPlan(ctx context.Context, a APIConsumerPlanAssignment) (APIConsumerPlanAssignment, error) {
	if err := validateAPIConsumerPlanAssignment(a); err != nil {
		return APIConsumerPlanAssignment{}, err
	}
	// Selecting from api_consumers scopes the consumer to the account and
	// app; the composite plan foreign key scopes the plan to the same app.
	out, err := scanAPIConsumerPlanAssignment(s.pool.QueryRow(ctx, `insert into api_consumer_plan_assignments
		(account_id, app_id, consumer_id, plan_id, effective_from)
		select c.account_id, c.app_id, c.id, nullif($4::text, '')::uuid, $5
		  from api_consumers c
		 where c.id = $3::uuid and c.account_id = $1::uuid and c.app_id = $2::uuid
		returning `+apiConsumerPlanAssignmentCols, a.AccountID, a.AppID, a.ConsumerID, a.PlanID, a.EffectiveFrom))
	if errors.Is(err, pgx.ErrNoRows) {
		return APIConsumerPlanAssignment{}, ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return APIConsumerPlanAssignment{}, ErrConflict
		case "23503":
			return APIConsumerPlanAssignment{}, ErrNotFound
		}
	}
	return out, err
}

func (s *PgStore) ListAPIConsumerPlanAssignments(ctx context.Context, accountID, appID, consumerID string) ([]APIConsumerPlanAssignment, error) {
	rows, err := s.pool.Query(ctx, `select `+apiConsumerPlanAssignmentCols+` from api_consumer_plan_assignments
		where account_id = $1::uuid and app_id = $2::uuid and consumer_id = $3::uuid
		order by effective_from`, accountID, appID, consumerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []APIConsumerPlanAssignment{}
	for rows.Next() {
		a, err := scanAPIConsumerPlanAssignment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *PgStore) GetAPIConsumerPlanPolicy(ctx context.Context, accountID, appID, consumerID string, at time.Time) (APIConsumerPlanPolicy, error) {
	var policy APIConsumerPlanPolicy
	var weights []byte
	err := s.pool.QueryRow(ctx, `
		with current_assignment as (
			select plan_id from api_consumer_plan_assignments
			 where account_id = $1::uuid and app_id = $2::uuid and consumer_id = $3::uuid and effective_from <= $4
			 order by effective_from desc limit 1)
		select p.id, p.app_id, p.max_requests_per_minute, p.max_units_per_month, p.alert_thresholds_percent,
		       coalesce((select c.route_weights from api_consumer_rate_cards c
		                  where c.plan_id = p.id and c.effective_from <= $4
		                  order by c.effective_from desc limit 1), '{}'::jsonb)
		  from current_assignment a join api_consumer_plans p on p.id = a.plan_id`,
		accountID, appID, consumerID, at.UTC()).Scan(&policy.PlanID, &policy.AppID, &policy.MaxRequestsPerMinute, &policy.MaxUnitsPerMonth, &policy.AlertThresholdsPercent, &weights)
	if errors.Is(err, pgx.ErrNoRows) {
		return APIConsumerPlanPolicy{}, nil // default plan: no limits
	}
	if err != nil {
		return APIConsumerPlanPolicy{}, err
	}
	if err := json.Unmarshal(weights, &policy.RouteWeights); err != nil {
		return APIConsumerPlanPolicy{}, err
	}
	return policy, nil
}

// AdmitAPIConsumerPlanRequest serializes on the consumer's counter row,
// like platform tenant request budgets: no replica-local fallback.
func (s *PgStore) AdmitAPIConsumerPlanRequest(ctx context.Context, accountID, consumerID string, policy APIConsumerPlanPolicy, units int64) (APIConsumerPlanDecision, error) {
	if !policy.Limited() {
		return APIConsumerPlanDecision{Allowed: true}, nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return APIConsumerPlanDecision{}, fmt.Errorf("begin consumer plan admission: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var now time.Time
	if err := tx.QueryRow(ctx, `select clock_timestamp()`).Scan(&now); err != nil {
		return APIConsumerPlanDecision{}, fmt.Errorf("read consumer plan admission clock: %w", err)
	}
	if _, err := tx.Exec(ctx, `insert into api_consumer_plan_admissions (consumer_id, account_id, minute_start, month_start)
		values ($1::uuid, $2::uuid, $3, $3) on conflict (consumer_id) do nothing`, consumerID, accountID, now.UTC()); err != nil {
		return APIConsumerPlanDecision{}, fmt.Errorf("create consumer plan admission row: %w", err)
	}
	var counter planAdmissionCounter
	if err := tx.QueryRow(ctx, `select minute_start, minute_used, month_start, month_used
		from api_consumer_plan_admissions where consumer_id = $1::uuid and account_id = $2::uuid for update`,
		consumerID, accountID).Scan(&counter.MinuteStart, &counter.MinuteUsed, &counter.MonthStart, &counter.MonthUsed); err != nil {
		return APIConsumerPlanDecision{}, fmt.Errorf("lock consumer plan admission row: %w", err)
	}
	counter.MinuteStart, counter.MonthStart = counter.MinuteStart.UTC(), counter.MonthStart.UTC()
	counter, decision, crossed := decidePlanAdmission(counter, policy, units, now)
	if len(crossed) > 0 {
		if err := recordAPIConsumerUsageAlertsTx(ctx, tx, accountID, consumerID, policy, counter, crossed); err != nil {
			return APIConsumerPlanDecision{}, err
		}
	}
	if !decision.Allowed {
		if len(crossed) > 0 {
			if err := tx.Commit(ctx); err != nil {
				return APIConsumerPlanDecision{}, fmt.Errorf("commit consumer usage alert: %w", err)
			}
		}
		return decision, nil
	}
	if _, err := tx.Exec(ctx, `update api_consumer_plan_admissions
		set minute_start = $2, minute_used = $3, month_start = $4, month_used = $5 where consumer_id = $1::uuid`,
		consumerID, counter.MinuteStart, counter.MinuteUsed, counter.MonthStart, counter.MonthUsed); err != nil {
		return APIConsumerPlanDecision{}, fmt.Errorf("consume consumer plan admission: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return APIConsumerPlanDecision{}, fmt.Errorf("commit consumer plan admission: %w", err)
	}
	return decision, nil
}
