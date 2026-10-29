package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

func (s *PgStore) PutSpendCapHold(ctx context.Context, hold SpendCapHold) error {
	if !validSpendCapHold(hold) {
		return ErrInvalidArgument
	}
	_, err := s.pool.Exec(ctx, `
		insert into account_spend_cap_holds (account_id, month, cap_cents, observed_cents)
		values ($1, $2::date, $3, $4)
		on conflict (account_id) do update
		   set month = excluded.month, cap_cents = excluded.cap_cents,
		       observed_cents = excluded.observed_cents,
		       reached_at = case
		           when account_spend_cap_holds.month = excluded.month
		            and account_spend_cap_holds.cap_cents = excluded.cap_cents
		           then account_spend_cap_holds.reached_at else now() end`,
		hold.AccountID, hold.Month.UTC(), hold.CapCents, hold.ObservedCents)
	if err != nil {
		return fmt.Errorf("state: put spend cap hold: %w", mapErr(err))
	}
	return nil
}

func (s *PgStore) ClearSpendCapHold(ctx context.Context, accountID string) error {
	if _, err := s.pool.Exec(ctx, `delete from account_spend_cap_holds where account_id = $1`, accountID); err != nil {
		return fmt.Errorf("state: clear spend cap hold: %w", mapErr(err))
	}
	return nil
}

func (s *PgStore) GetSpendCapHold(ctx context.Context, accountID string) (SpendCapHold, bool, error) {
	hold := SpendCapHold{AccountID: accountID}
	err := s.pool.QueryRow(ctx, `
		select month, cap_cents, observed_cents, reached_at
		  from account_spend_cap_holds where account_id = $1`, accountID).
		Scan(&hold.Month, &hold.CapCents, &hold.ObservedCents, &hold.ReachedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return SpendCapHold{}, false, nil
	}
	if err != nil {
		return SpendCapHold{}, false, fmt.Errorf("state: read spend cap hold: %w", err)
	}
	return hold, true, nil
}
