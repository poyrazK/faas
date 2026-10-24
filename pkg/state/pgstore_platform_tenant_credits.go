package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/onebox-faas/faas/pkg/api"
)

const platformTenantCreditGrantCols = `id, account_id, platform_tenant_id, currency, amount_millicents,
	remaining_millicents, note, expires_at, voided_at, created_at`

func scanPlatformTenantCreditGrant(row pgx.Row) (PlatformTenantCreditGrant, error) {
	var g PlatformTenantCreditGrant
	err := row.Scan(&g.ID, &g.AccountID, &g.TenantID, &g.Currency, &g.AmountMillicents,
		&g.RemainingMillicents, &g.Note, &g.ExpiresAt, &g.VoidedAt, &g.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return PlatformTenantCreditGrant{}, ErrNotFound
	}
	if err != nil {
		return PlatformTenantCreditGrant{}, err
	}
	g.CreatedAt = g.CreatedAt.UTC()
	for _, t := range []*time.Time{g.ExpiresAt, g.VoidedAt} {
		if t != nil {
			*t = t.UTC()
		}
	}
	return g, nil
}

// CreatePlatformTenantCreditGrant locks the tenant row so concurrent grants
// cannot both pass the open-grant limit.
func (s *PgStore) CreatePlatformTenantCreditGrant(ctx context.Context, g PlatformTenantCreditGrant) (PlatformTenantCreditGrant, error) {
	if err := validatePlatformTenantCreditGrant(g); err != nil {
		return PlatformTenantCreditGrant{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PlatformTenantCreditGrant{}, fmt.Errorf("begin tenant credit grant: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var open int
	err = tx.QueryRow(ctx, `select (select count(*) from platform_tenant_credit_grants
			where platform_tenant_id = t.id and remaining_millicents > 0)
		from platform_tenants t where t.id = $1::uuid and t.account_id = $2::uuid for update`,
		g.TenantID, g.AccountID).Scan(&open)
	if errors.Is(err, pgx.ErrNoRows) {
		return PlatformTenantCreditGrant{}, ErrNotFound
	}
	if err != nil {
		return PlatformTenantCreditGrant{}, fmt.Errorf("lock tenant for credit grant: %w", err)
	}
	if open >= api.ConsumerCreditOpenGrantsMax {
		return PlatformTenantCreditGrant{}, ErrConflict
	}
	created, err := scanPlatformTenantCreditGrant(tx.QueryRow(ctx, `insert into platform_tenant_credit_grants
			(account_id, platform_tenant_id, currency, amount_millicents, remaining_millicents, note, expires_at)
		values ($1::uuid, $2::uuid, $3, $4, $4, $5, $6)
		returning `+platformTenantCreditGrantCols,
		g.AccountID, g.TenantID, g.Currency, g.AmountMillicents, g.Note, g.ExpiresAt))
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgerrcode.CheckViolation {
		return PlatformTenantCreditGrant{}, fmt.Errorf("%w: %s", ErrInvalidArgument, pgErr.ConstraintName)
	}
	if err != nil {
		return PlatformTenantCreditGrant{}, fmt.Errorf("insert tenant credit grant: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return PlatformTenantCreditGrant{}, fmt.Errorf("commit tenant credit grant: %w", err)
	}
	return created, nil
}

func (s *PgStore) ListPlatformTenantCreditGrants(ctx context.Context, accountID, tenantID string) ([]PlatformTenantCreditGrant, error) {
	rows, err := s.pool.Query(ctx, `select `+platformTenantCreditGrantCols+` from platform_tenant_credit_grants
		where account_id = $1::uuid and platform_tenant_id = $2::uuid
		order by created_at desc, id desc`, accountID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list tenant credit grants: %w", err)
	}
	defer rows.Close()
	out := []PlatformTenantCreditGrant{}
	for rows.Next() {
		g, err := scanPlatformTenantCreditGrant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *PgStore) VoidPlatformTenantCreditGrant(ctx context.Context, accountID, tenantID, grantID string) (PlatformTenantCreditGrant, error) {
	g, err := scanPlatformTenantCreditGrant(s.pool.QueryRow(ctx, `update platform_tenant_credit_grants
		   set remaining_millicents = 0, voided_at = coalesce(voided_at, now())
		 where id = $1::uuid and account_id = $2::uuid and platform_tenant_id = $3::uuid
		returning `+platformTenantCreditGrantCols, grantID, accountID, tenantID))
	if err != nil && !errors.Is(err, ErrNotFound) {
		return PlatformTenantCreditGrant{}, fmt.Errorf("void tenant credit grant: %w", err)
	}
	return g, err
}

func (s *PgStore) PlatformTenantCreditPosition(ctx context.Context, accountID, tenantID, currency string, at time.Time) (APIConsumerCreditPosition, error) {
	monthStart, monthEnd := creditMonth(at)
	var pos APIConsumerCreditPosition
	err := s.pool.QueryRow(ctx, `select
		coalesce((select sum(remaining_millicents) from platform_tenant_credit_grants
		           where account_id = $1::uuid and platform_tenant_id = $2::uuid and currency = $3
		             and remaining_millicents > 0 and voided_at is null and (expires_at is null or expires_at > $4)), 0)::bigint,
		coalesce((select sum(`+creditAppliedToUsageSQL+`) from platform_tenant_statements st
		           where st.account_id = $1::uuid and st.platform_tenant_id = $2::uuid and st.currency = $3
		             and st.credit_applied_millicents > 0 and st.period_start >= $5 and st.period_start < $6), 0)::bigint`,
		accountID, tenantID, currency, at.UTC(), monthStart, monthEnd).Scan(&pos.AvailableMillicents, &pos.AppliedToUsageInMonthMillicents)
	if err != nil {
		return APIConsumerCreditPosition{}, fmt.Errorf("tenant credit position: %w", err)
	}
	return pos, nil
}

// applyPlatformTenantCredits draws the tenant's usable grants down against a
// statement inside its finalize transaction, oldest-expiring first, and
// returns the credit applied.
func applyPlatformTenantCredits(ctx context.Context, tx pgx.Tx, statement PlatformTenantStatement) (int64, error) {
	if statement.AmountMillicents <= 0 || statement.Currency == "" {
		return 0, nil
	}
	rows, err := tx.Query(ctx, `select id, remaining_millicents from platform_tenant_credit_grants
		where account_id = $1::uuid and platform_tenant_id = $2::uuid and currency = $3
		  and remaining_millicents > 0 and voided_at is null and (expires_at is null or expires_at > now())
		order by expires_at asc nulls last, created_at, id
		for update`, statement.AccountID, statement.TenantID, statement.Currency)
	if err != nil {
		return 0, fmt.Errorf("state: lock tenant credit grants: %w", err)
	}
	type draw struct {
		id   string
		take int64
	}
	var draws []draw
	left := statement.AmountMillicents
	for rows.Next() {
		var id string
		var remaining int64
		if err := rows.Scan(&id, &remaining); err != nil {
			rows.Close()
			return 0, err
		}
		if left > 0 {
			take := min(left, remaining)
			draws = append(draws, draw{id: id, take: take})
			left -= take
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, d := range draws {
		if _, err := tx.Exec(ctx, `update platform_tenant_credit_grants set remaining_millicents = remaining_millicents - $2 where id = $1::uuid`,
			d.id, d.take); err != nil {
			return 0, fmt.Errorf("state: draw tenant credit grant: %w", err)
		}
		if _, err := tx.Exec(ctx, `insert into platform_tenant_credit_applications (grant_id, statement_id, account_id, amount_millicents)
			values ($1::uuid, $2::uuid, $3::uuid, $4)`, d.id, statement.ID, statement.AccountID, d.take); err != nil {
			return 0, fmt.Errorf("state: record tenant credit application: %w", err)
		}
	}
	return statement.AmountMillicents - left, nil
}

const platformTenantSpendCapCols = `platform_tenant_id, account_id, currency, monthly_cap_millicents, capped,
	month_start, amount_due_millicents, checked_at, next_check_at, created_at, updated_at`

func scanPlatformTenantSpendCap(row pgx.Row) (PlatformTenantSpendCap, error) {
	var c PlatformTenantSpendCap
	err := row.Scan(&c.TenantID, &c.AccountID, &c.Currency, &c.MonthlyCapMillicents, &c.Capped,
		&c.MonthStart, &c.AmountDueMillicents, &c.CheckedAt, &c.NextCheckAt, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return PlatformTenantSpendCap{}, ErrNotFound
	}
	if err != nil {
		return PlatformTenantSpendCap{}, err
	}
	c.NextCheckAt, c.CreatedAt, c.UpdatedAt = c.NextCheckAt.UTC(), c.CreatedAt.UTC(), c.UpdatedAt.UTC()
	for _, t := range []*time.Time{c.MonthStart, c.CheckedAt} {
		if t != nil {
			*t = t.UTC()
		}
	}
	return c, nil
}

func (s *PgStore) PutPlatformTenantSpendCap(ctx context.Context, c PlatformTenantSpendCap) (PlatformTenantSpendCap, error) {
	if err := validatePlatformTenantSpendCap(c); err != nil {
		return PlatformTenantSpendCap{}, err
	}
	out, err := scanPlatformTenantSpendCap(s.pool.QueryRow(ctx, `insert into platform_tenant_spend_caps
			(platform_tenant_id, account_id, currency, monthly_cap_millicents)
		select t.id, t.account_id, $3, $4 from platform_tenants t
		 where t.id = $1::uuid and t.account_id = $2::uuid
		on conflict (platform_tenant_id) do update
		   set currency = excluded.currency, monthly_cap_millicents = excluded.monthly_cap_millicents,
		       next_check_at = now(), updated_at = now()
		returning `+platformTenantSpendCapCols, c.TenantID, c.AccountID, c.Currency, c.MonthlyCapMillicents))
	if err != nil && !errors.Is(err, ErrNotFound) {
		return PlatformTenantSpendCap{}, fmt.Errorf("put tenant spend cap: %w", err)
	}
	return out, err
}

func (s *PgStore) GetPlatformTenantSpendCap(ctx context.Context, accountID, tenantID string) (PlatformTenantSpendCap, error) {
	c, err := scanPlatformTenantSpendCap(s.pool.QueryRow(ctx, `select `+platformTenantSpendCapCols+`
		from platform_tenant_spend_caps where platform_tenant_id = $1::uuid and account_id = $2::uuid`, tenantID, accountID))
	if err != nil && !errors.Is(err, ErrNotFound) {
		return PlatformTenantSpendCap{}, fmt.Errorf("get tenant spend cap: %w", err)
	}
	return c, err
}

func (s *PgStore) DeletePlatformTenantSpendCap(ctx context.Context, accountID, tenantID string) error {
	tag, err := s.pool.Exec(ctx, `delete from platform_tenant_spend_caps where platform_tenant_id = $1::uuid and account_id = $2::uuid`,
		tenantID, accountID)
	if err != nil {
		return fmt.Errorf("delete tenant spend cap: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PgStore) ClaimPlatformTenantSpendCapChecks(ctx context.Context, now time.Time, lease time.Duration, limit int) ([]PlatformTenantSpendCap, error) {
	rows, err := s.pool.Query(ctx, `update platform_tenant_spend_caps
		   set next_check_at = $2
		 where platform_tenant_id in (select platform_tenant_id from platform_tenant_spend_caps
		                        where next_check_at <= $1 order by next_check_at limit $3 for update skip locked)
		returning `+platformTenantSpendCapCols, now.UTC(), now.Add(lease).UTC(), limit)
	if err != nil {
		return nil, fmt.Errorf("claim tenant spend cap checks: %w", err)
	}
	defer rows.Close()
	var out []PlatformTenantSpendCap
	for rows.Next() {
		c, err := scanPlatformTenantSpendCap(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *PgStore) RecordPlatformTenantSpendCheck(ctx context.Context, tenantID string, monthStart time.Time, amountDue int64, capped bool,
	checkedAt, next time.Time) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin tenant spend check: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var was bool
	err = tx.QueryRow(ctx, `select capped and month_start is not null and month_start = $2
		from platform_tenant_spend_caps where platform_tenant_id = $1::uuid for update`, tenantID, monthStart.UTC()).Scan(&was)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, fmt.Errorf("read tenant spend check: %w", err)
	}
	if _, err := tx.Exec(ctx, `update platform_tenant_spend_caps
		   set capped = $3, month_start = $2, amount_due_millicents = $4, checked_at = $5, next_check_at = $6
		 where platform_tenant_id = $1::uuid`, tenantID, monthStart.UTC(), capped, amountDue, checkedAt.UTC(), next.UTC()); err != nil {
		return false, fmt.Errorf("record tenant spend check: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit tenant spend check: %w", err)
	}
	return was, nil
}
