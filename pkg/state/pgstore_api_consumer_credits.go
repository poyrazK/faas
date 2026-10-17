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

const apiConsumerCreditGrantCols = `id, account_id, app_id, consumer_id, currency, amount_millicents,
	remaining_millicents, note, expires_at, voided_at, created_at`

func scanAPIConsumerCreditGrant(row pgx.Row) (APIConsumerCreditGrant, error) {
	var g APIConsumerCreditGrant
	err := row.Scan(&g.ID, &g.AccountID, &g.AppID, &g.ConsumerID, &g.Currency, &g.AmountMillicents,
		&g.RemainingMillicents, &g.Note, &g.ExpiresAt, &g.VoidedAt, &g.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return APIConsumerCreditGrant{}, ErrNotFound
	}
	if err != nil {
		return APIConsumerCreditGrant{}, err
	}
	g.CreatedAt = g.CreatedAt.UTC()
	for _, t := range []*time.Time{g.ExpiresAt, g.VoidedAt} {
		if t != nil {
			*t = t.UTC()
		}
	}
	return g, nil
}

// CreateAPIConsumerCreditGrant locks the consumer row so concurrent grants
// cannot both pass the open-grant limit.
func (s *PgStore) CreateAPIConsumerCreditGrant(ctx context.Context, g APIConsumerCreditGrant) (APIConsumerCreditGrant, error) {
	if err := validateAPIConsumerCreditGrant(g); err != nil {
		return APIConsumerCreditGrant{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return APIConsumerCreditGrant{}, fmt.Errorf("begin credit grant: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var open int
	err = tx.QueryRow(ctx, `select (select count(*) from api_consumer_credit_grants
			where consumer_id = c.id and remaining_millicents > 0)
		from api_consumers c where c.id = $1::uuid and c.account_id = $2::uuid and c.app_id = $3::uuid for update`,
		g.ConsumerID, g.AccountID, g.AppID).Scan(&open)
	if errors.Is(err, pgx.ErrNoRows) {
		return APIConsumerCreditGrant{}, ErrNotFound
	}
	if err != nil {
		return APIConsumerCreditGrant{}, fmt.Errorf("lock consumer for credit grant: %w", err)
	}
	if open >= api.ConsumerCreditOpenGrantsMax {
		return APIConsumerCreditGrant{}, ErrConflict
	}
	created, err := scanAPIConsumerCreditGrant(tx.QueryRow(ctx, `insert into api_consumer_credit_grants
			(account_id, app_id, consumer_id, currency, amount_millicents, remaining_millicents, note, expires_at)
		values ($1::uuid, $2::uuid, $3::uuid, $4, $5, $5, $6, $7)
		returning `+apiConsumerCreditGrantCols,
		g.AccountID, g.AppID, g.ConsumerID, g.Currency, g.AmountMillicents, g.Note, g.ExpiresAt))
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgerrcode.CheckViolation {
		return APIConsumerCreditGrant{}, fmt.Errorf("%w: %s", ErrInvalidArgument, pgErr.ConstraintName)
	}
	if err != nil {
		return APIConsumerCreditGrant{}, fmt.Errorf("insert credit grant: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return APIConsumerCreditGrant{}, fmt.Errorf("commit credit grant: %w", err)
	}
	return created, nil
}

func (s *PgStore) ListAPIConsumerCreditGrants(ctx context.Context, accountID, appID, consumerID string) ([]APIConsumerCreditGrant, error) {
	rows, err := s.pool.Query(ctx, `select `+apiConsumerCreditGrantCols+` from api_consumer_credit_grants
		where account_id = $1::uuid and app_id = $2::uuid and consumer_id = $3::uuid
		order by created_at desc, id desc`, accountID, appID, consumerID)
	if err != nil {
		return nil, fmt.Errorf("list credit grants: %w", err)
	}
	defer rows.Close()
	out := []APIConsumerCreditGrant{}
	for rows.Next() {
		g, err := scanAPIConsumerCreditGrant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *PgStore) VoidAPIConsumerCreditGrant(ctx context.Context, accountID, appID, consumerID, grantID string) (APIConsumerCreditGrant, error) {
	g, err := scanAPIConsumerCreditGrant(s.pool.QueryRow(ctx, `update api_consumer_credit_grants
		   set remaining_millicents = 0, voided_at = coalesce(voided_at, now())
		 where id = $1::uuid and account_id = $2::uuid and app_id = $3::uuid and consumer_id = $4::uuid
		returning `+apiConsumerCreditGrantCols, grantID, accountID, appID, consumerID))
	if err != nil && !errors.Is(err, ErrNotFound) {
		return APIConsumerCreditGrant{}, fmt.Errorf("void credit grant: %w", err)
	}
	return g, err
}

func (s *PgStore) APIConsumerCreditPosition(ctx context.Context, accountID, appID, consumerID, currency string, at time.Time) (APIConsumerCreditPosition, error) {
	monthStart, monthEnd := creditMonth(at)
	var pos APIConsumerCreditPosition
	err := s.pool.QueryRow(ctx, `select
		coalesce((select sum(remaining_millicents) from api_consumer_credit_grants
		           where account_id = $1::uuid and app_id = $2::uuid and consumer_id = $3::uuid and currency = $4
		             and remaining_millicents > 0 and voided_at is null and (expires_at is null or expires_at > $5)), 0)::bigint,
		coalesce((select sum(a.amount_millicents) from api_consumer_credit_applications a
		            join api_consumer_credit_grants g on g.id = a.grant_id
		            join api_consumer_usage_statements st on st.id = a.statement_id
		           where g.account_id = $1::uuid and g.app_id = $2::uuid and g.consumer_id = $3::uuid and g.currency = $4
		             and st.period_start >= $6 and st.period_start < $7), 0)::bigint`,
		accountID, appID, consumerID, currency, at.UTC(), monthStart, monthEnd).Scan(&pos.AvailableMillicents, &pos.AppliedInMonthMillicents)
	if err != nil {
		return APIConsumerCreditPosition{}, fmt.Errorf("credit position: %w", err)
	}
	return pos, nil
}

// applyAPIConsumerCredits draws the consumer's usable grants down against a
// statement inside its finalize transaction, oldest-expiring first.
func applyAPIConsumerCredits(ctx context.Context, tx pgx.Tx, statement APIConsumerUsageStatement) (APIConsumerUsageStatement, error) {
	if statement.AmountMillicents <= 0 || statement.Currency == "" {
		return statement, nil
	}
	rows, err := tx.Query(ctx, `select id, remaining_millicents from api_consumer_credit_grants
		where account_id = $1::uuid and app_id = $2::uuid and consumer_id = $3::uuid and currency = $4
		  and remaining_millicents > 0 and voided_at is null and (expires_at is null or expires_at > now())
		order by expires_at asc nulls last, created_at, id
		for update`, statement.AccountID, statement.AppID, statement.ConsumerID, statement.Currency)
	if err != nil {
		return statement, fmt.Errorf("state: lock credit grants: %w", err)
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
			return statement, err
		}
		if left > 0 {
			take := min(left, remaining)
			draws = append(draws, draw{id: id, take: take})
			left -= take
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return statement, err
	}
	for _, d := range draws {
		if _, err := tx.Exec(ctx, `update api_consumer_credit_grants set remaining_millicents = remaining_millicents - $2 where id = $1::uuid`, d.id, d.take); err != nil {
			return statement, fmt.Errorf("state: draw credit grant: %w", err)
		}
		if _, err := tx.Exec(ctx, `insert into api_consumer_credit_applications (grant_id, statement_id, account_id, amount_millicents)
			values ($1::uuid, $2::uuid, $3::uuid, $4)`, d.id, statement.ID, statement.AccountID, d.take); err != nil {
			return statement, fmt.Errorf("state: record credit application: %w", err)
		}
	}
	applied := statement.AmountMillicents - left
	if applied == 0 {
		return statement, nil
	}
	if _, err := tx.Exec(ctx, `update api_consumer_usage_statements set credit_applied_millicents = $2 where id = $1::uuid`, statement.ID, applied); err != nil {
		return statement, fmt.Errorf("state: record statement credit: %w", err)
	}
	statement.CreditAppliedMillicents = applied
	return statement, nil
}

const apiConsumerSpendCapCols = `consumer_id, account_id, app_id, currency, monthly_cap_millicents, capped,
	month_start, amount_due_millicents, checked_at, next_check_at, created_at, updated_at`

func scanAPIConsumerSpendCap(row pgx.Row) (APIConsumerSpendCap, error) {
	var c APIConsumerSpendCap
	err := row.Scan(&c.ConsumerID, &c.AccountID, &c.AppID, &c.Currency, &c.MonthlyCapMillicents, &c.Capped,
		&c.MonthStart, &c.AmountDueMillicents, &c.CheckedAt, &c.NextCheckAt, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return APIConsumerSpendCap{}, ErrNotFound
	}
	if err != nil {
		return APIConsumerSpendCap{}, err
	}
	c.NextCheckAt, c.CreatedAt, c.UpdatedAt = c.NextCheckAt.UTC(), c.CreatedAt.UTC(), c.UpdatedAt.UTC()
	for _, t := range []*time.Time{c.MonthStart, c.CheckedAt} {
		if t != nil {
			*t = t.UTC()
		}
	}
	return c, nil
}

func (s *PgStore) PutAPIConsumerSpendCap(ctx context.Context, c APIConsumerSpendCap) (APIConsumerSpendCap, error) {
	if err := validateAPIConsumerSpendCap(c); err != nil {
		return APIConsumerSpendCap{}, err
	}
	out, err := scanAPIConsumerSpendCap(s.pool.QueryRow(ctx, `insert into api_consumer_spend_caps
			(consumer_id, account_id, app_id, currency, monthly_cap_millicents)
		select c.id, c.account_id, c.app_id, $4, $5 from api_consumers c
		 where c.id = $1::uuid and c.account_id = $2::uuid and c.app_id = $3::uuid
		on conflict (consumer_id) do update
		   set currency = excluded.currency, monthly_cap_millicents = excluded.monthly_cap_millicents,
		       next_check_at = now(), updated_at = now()
		returning `+apiConsumerSpendCapCols, c.ConsumerID, c.AccountID, c.AppID, c.Currency, c.MonthlyCapMillicents))
	if err != nil && !errors.Is(err, ErrNotFound) {
		return APIConsumerSpendCap{}, fmt.Errorf("put spend cap: %w", err)
	}
	return out, err
}

func (s *PgStore) GetAPIConsumerSpendCap(ctx context.Context, accountID, consumerID string) (APIConsumerSpendCap, error) {
	c, err := scanAPIConsumerSpendCap(s.pool.QueryRow(ctx, `select `+apiConsumerSpendCapCols+`
		from api_consumer_spend_caps where consumer_id = $1::uuid and account_id = $2::uuid`, consumerID, accountID))
	if err != nil && !errors.Is(err, ErrNotFound) {
		return APIConsumerSpendCap{}, fmt.Errorf("get spend cap: %w", err)
	}
	return c, err
}

func (s *PgStore) DeleteAPIConsumerSpendCap(ctx context.Context, accountID, consumerID string) error {
	tag, err := s.pool.Exec(ctx, `delete from api_consumer_spend_caps where consumer_id = $1::uuid and account_id = $2::uuid`, consumerID, accountID)
	if err != nil {
		return fmt.Errorf("delete spend cap: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PgStore) ClaimAPIConsumerSpendCapChecks(ctx context.Context, now time.Time, lease time.Duration, limit int) ([]APIConsumerSpendCap, error) {
	rows, err := s.pool.Query(ctx, `update api_consumer_spend_caps
		   set next_check_at = $2
		 where consumer_id in (select consumer_id from api_consumer_spend_caps
		                        where next_check_at <= $1 order by next_check_at limit $3 for update skip locked)
		returning `+apiConsumerSpendCapCols, now.UTC(), now.Add(lease).UTC(), limit)
	if err != nil {
		return nil, fmt.Errorf("claim spend cap checks: %w", err)
	}
	defer rows.Close()
	var out []APIConsumerSpendCap
	for rows.Next() {
		c, err := scanAPIConsumerSpendCap(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *PgStore) RecordAPIConsumerSpendCheck(ctx context.Context, consumerID string, monthStart time.Time, amountDue int64, capped bool,
	checkedAt, next time.Time) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin spend check: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var was bool
	err = tx.QueryRow(ctx, `select capped and month_start is not null and month_start = $2
		from api_consumer_spend_caps where consumer_id = $1::uuid for update`, consumerID, monthStart.UTC()).Scan(&was)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, fmt.Errorf("read spend check: %w", err)
	}
	if _, err := tx.Exec(ctx, `update api_consumer_spend_caps
		   set capped = $3, month_start = $2, amount_due_millicents = $4, checked_at = $5, next_check_at = $6
		 where consumer_id = $1::uuid`, consumerID, monthStart.UTC(), capped, amountDue, checkedAt.UTC(), next.UTC()); err != nil {
		return false, fmt.Errorf("record spend check: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit spend check: %w", err)
	}
	return was, nil
}
