package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) PutSpendCapHold(ctx context.Context, hold SpendCapHold) error {
	if !validSpendCapHold(hold) {
		return ErrInvalidArgument
	}
	account, err := parsePgUUID(hold.AccountID)
	if err != nil {
		return ErrInvalidArgument
	}
	id, _ := parsePgUUID(spendCapNoticeID(hold))
	err = sqlc.New().PutSpendCapHoldWithNotice(ctx, s.pool, sqlc.PutSpendCapHoldWithNoticeParams{ID: id, AccountID: account,
		Month: pgtype.Date{Time: hold.Month.UTC(), Valid: true}, CapCents: hold.CapCents, ObservedCents: hold.ObservedCents})
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
