package state

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// AccountDeployRateReserver takes several deploys of an account's hourly
// window at once, all or none. A project deploy reserves one per service: it
// used to read the remaining budget and then consume it service by service,
// so a concurrent deploy (or a failed read, which let it through) could run
// the window out part-way and leave the project on mixed releases
// (deploy-path review P17).
type AccountDeployRateReserver interface {
	ReserveAccountDeployRate(ctx context.Context, accountID string, limit, n int, now time.Time) (AccountDeployRateSnapshot, error)
}

var (
	_ AccountDeployRateReserver = (*MemStore)(nil)
	_ AccountDeployRateReserver = (*PgStore)(nil)
)

func validateDeployRateReservation(n int) error {
	if n <= 0 {
		return fmt.Errorf("state: account deploy rate reservation requires a positive count, got %d", n)
	}
	return nil
}

func (s *MemStore) ReserveAccountDeployRate(ctx context.Context, accountID string, limit, n int, now time.Time) (AccountDeployRateSnapshot, error) {
	now, err := validateDeployRateInput(accountID, limit, now)
	if err != nil {
		return AccountDeployRateSnapshot{}, err
	}
	if err := validateDeployRateReservation(n); err != nil {
		return AccountDeployRateSnapshot{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkOrgAccessLocked(ctx); err != nil {
		return AccountDeployRateSnapshot{}, err
	}
	if _, ok := s.billingResourceAccountLocked(accountID); !ok {
		return AccountDeployRateSnapshot{}, ErrNotFound
	}
	row := currentDeployRateRow(s.accountDeployRates[accountID], now)
	allowed := row.Used+n <= limit
	if allowed {
		row.Used += n
	}
	s.accountDeployRates[accountID] = row
	return deployRateSnapshot(row, limit, allowed), nil
}

func (s *PgStore) ReserveAccountDeployRate(ctx context.Context, accountID string, limit, n int, now time.Time) (snapshot AccountDeployRateSnapshot, err error) {
	now, err = validateDeployRateInput(accountID, limit, now)
	if err != nil {
		return AccountDeployRateSnapshot{}, err
	}
	if err = validateDeployRateReservation(n); err != nil {
		return AccountDeployRateSnapshot{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return AccountDeployRateSnapshot{}, mapErr(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `
		INSERT INTO account_deploy_rate_limits (account_id, deploys_used, deploys_window_start, updated_at)
		VALUES ($1, 0, $2, $2)
		ON CONFLICT (account_id) DO NOTHING`, accountID, now); err != nil {
		return AccountDeployRateSnapshot{}, mapErr(err)
	}
	row := accountDeployRateRow{}
	if err = tx.QueryRow(ctx, `
		SELECT deploys_used, deploys_window_start
		FROM account_deploy_rate_limits
		WHERE account_id = $1
		FOR UPDATE`, accountID).Scan(&row.Used, &row.WindowStart); err != nil {
		return AccountDeployRateSnapshot{}, mapErr(err)
	}
	row = currentDeployRateRow(row, now)
	allowed := row.Used+n <= limit
	if allowed {
		row.Used += n
	}
	if _, err = tx.Exec(ctx, `
		UPDATE account_deploy_rate_limits
		SET deploys_used = $2, deploys_window_start = $3, updated_at = $4
		WHERE account_id = $1`, accountID, row.Used, row.WindowStart, now); err != nil {
		return AccountDeployRateSnapshot{}, mapErr(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return AccountDeployRateSnapshot{}, mapErr(err)
	}
	return deployRateSnapshot(row, limit, allowed), nil
}
