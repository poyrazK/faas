package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// AccountDeployRateWindow is the fixed deploy-admission window shared by the
// API, dashboard, and both state implementations.
const AccountDeployRateWindow = time.Hour

// AccountDeployRateSnapshot is the durable state for one account's current
// deploy window. Allowed reports whether ConsumeAccountDeployRate admitted the
// requested deploy. ReadAccountDeployRate always sets Allowed true.
type AccountDeployRateSnapshot struct {
	Used           int
	Limit          int
	Remaining      int
	WindowStart    time.Time
	WindowResetsAt time.Time
	Allowed        bool
}

// AccountDeployRateStore is implemented by production Postgres and the test
// memstore. It is separate from Store so existing narrow test doubles remain
// source compatible.
type AccountDeployRateStore interface {
	ConsumeAccountDeployRate(context.Context, string, int, time.Time) (AccountDeployRateSnapshot, error)
	ReadAccountDeployRate(context.Context, string, int, time.Time) (AccountDeployRateSnapshot, error)
}

type accountDeployRateRow struct {
	Used        int
	WindowStart time.Time
}

func deployRateSnapshot(row accountDeployRateRow, limit int, allowed bool) AccountDeployRateSnapshot {
	remaining := limit - row.Used
	if remaining < 0 {
		remaining = 0
	}
	return AccountDeployRateSnapshot{
		Used:           row.Used,
		Limit:          limit,
		Remaining:      remaining,
		WindowStart:    row.WindowStart,
		WindowResetsAt: row.WindowStart.Add(AccountDeployRateWindow),
		Allowed:        allowed,
	}
}

func validateDeployRateInput(accountID string, limit int, now time.Time) (time.Time, error) {
	if accountID == "" {
		return time.Time{}, errors.New("state: account deploy rate requires account id")
	}
	if limit <= 0 {
		return time.Time{}, fmt.Errorf("state: account deploy rate requires a positive limit, got %d", limit)
	}
	if now.IsZero() {
		return time.Time{}, errors.New("state: account deploy rate requires current time")
	}
	return now.UTC(), nil
}

func currentDeployRateRow(row accountDeployRateRow, now time.Time) accountDeployRateRow {
	if row.WindowStart.IsZero() || !now.Before(row.WindowStart.Add(AccountDeployRateWindow)) {
		return accountDeployRateRow{WindowStart: now}
	}
	return row
}

func (s *MemStore) ConsumeAccountDeployRate(_ context.Context, accountID string, limit int, now time.Time) (AccountDeployRateSnapshot, error) {
	now, err := validateDeployRateInput(accountID, limit, now)
	if err != nil {
		return AccountDeployRateSnapshot{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.accounts[accountID]; !ok {
		return AccountDeployRateSnapshot{}, ErrNotFound
	}
	row := currentDeployRateRow(s.accountDeployRates[accountID], now)
	if row.Used >= limit {
		s.accountDeployRates[accountID] = row
		return deployRateSnapshot(row, limit, false), nil
	}
	row.Used++
	s.accountDeployRates[accountID] = row
	return deployRateSnapshot(row, limit, true), nil
}

func (s *MemStore) ReadAccountDeployRate(_ context.Context, accountID string, limit int, now time.Time) (AccountDeployRateSnapshot, error) {
	now, err := validateDeployRateInput(accountID, limit, now)
	if err != nil {
		return AccountDeployRateSnapshot{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.accounts[accountID]; !ok {
		return AccountDeployRateSnapshot{}, ErrNotFound
	}
	row := currentDeployRateRow(s.accountDeployRates[accountID], now)
	return deployRateSnapshot(row, limit, true), nil
}

func (s *PgStore) ConsumeAccountDeployRate(ctx context.Context, accountID string, limit int, now time.Time) (snapshot AccountDeployRateSnapshot, err error) {
	now, err = validateDeployRateInput(accountID, limit, now)
	if err != nil {
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
	allowed := row.Used < limit
	if allowed {
		row.Used++
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

func (s *PgStore) ReadAccountDeployRate(ctx context.Context, accountID string, limit int, now time.Time) (AccountDeployRateSnapshot, error) {
	now, err := validateDeployRateInput(accountID, limit, now)
	if err != nil {
		return AccountDeployRateSnapshot{}, err
	}
	row := accountDeployRateRow{}
	err = s.pool.QueryRow(ctx, `
		SELECT deploys_used, deploys_window_start
		FROM account_deploy_rate_limits
		WHERE account_id = $1`, accountID).Scan(&row.Used, &row.WindowStart)
	if errors.Is(err, pgx.ErrNoRows) {
		return deployRateSnapshot(accountDeployRateRow{WindowStart: now}, limit, true), nil
	}
	if err != nil {
		return AccountDeployRateSnapshot{}, mapErr(err)
	}
	row = currentDeployRateRow(row, now)
	return deployRateSnapshot(row, limit, true), nil
}

var _ AccountDeployRateStore = (*MemStore)(nil)
var _ AccountDeployRateStore = (*PgStore)(nil)
