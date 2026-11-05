package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

var _ LogSavedSearchStore = (*PgStore)(nil)

const logSavedSearchColumns = `id::text, account_id::text, app_id::text, name, query, created_at, updated_at`

// PutLogSavedSearch upserts under a per-app transaction lock, so two
// concurrent creates cannot both take the last slot.
func (s *PgStore) PutLogSavedSearch(ctx context.Context, search LogSavedSearch, limit int) (LogSavedSearch, error) {
	if err := validateLogSavedSearch(search); err != nil {
		return LogSavedSearch{}, err
	}
	var out LogSavedSearch
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtextextended('log_saved_searches:' || $1, 0))`, search.AppID); err != nil {
			return err
		}
		var count int
		var exists bool
		if err := tx.QueryRow(ctx,
			`select count(*), coalesce(bool_or(name = $2), false) from log_saved_searches where app_id = $1`,
			search.AppID, search.Name).Scan(&count, &exists); err != nil {
			return err
		}
		if !exists && count >= limit {
			return &LogSavedSearchQuotaError{Limit: limit, Observed: count}
		}
		return scanLogSavedSearch(tx.QueryRow(ctx,
			`insert into log_saved_searches (account_id, app_id, name, query) values ($1, $2, $3, $4)
			 on conflict (app_id, name) do update set query = excluded.query, updated_at = now()
			 returning `+logSavedSearchColumns,
			search.AccountID, search.AppID, search.Name, search.Query), &out)
	})
	var quota *LogSavedSearchQuotaError
	if errors.As(err, &quota) {
		return LogSavedSearch{}, quota
	}
	if err != nil {
		return LogSavedSearch{}, fmt.Errorf("state: put saved log search: %w", err)
	}
	return out, nil
}

// ListLogSavedSearches implements LogSavedSearchStore.
func (s *PgStore) ListLogSavedSearches(ctx context.Context, accountID, appID string) ([]LogSavedSearch, error) {
	rows, err := s.pool.Query(ctx,
		`select `+logSavedSearchColumns+` from log_saved_searches where account_id = $1 and app_id = $2 order by name`,
		accountID, appID)
	if err != nil {
		return nil, fmt.Errorf("state: list saved log searches: %w", err)
	}
	defer rows.Close()
	var out []LogSavedSearch
	for rows.Next() {
		var search LogSavedSearch
		if err := scanLogSavedSearch(rows, &search); err != nil {
			return nil, fmt.Errorf("state: scan saved log search: %w", err)
		}
		out = append(out, search)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: list saved log searches: %w", err)
	}
	return out, nil
}

// LogSavedSearchByName implements LogSavedSearchStore.
func (s *PgStore) LogSavedSearchByName(ctx context.Context, accountID, appID, name string) (LogSavedSearch, error) {
	var out LogSavedSearch
	err := scanLogSavedSearch(s.pool.QueryRow(ctx,
		`select `+logSavedSearchColumns+` from log_saved_searches where account_id = $1 and app_id = $2 and name = $3`,
		accountID, appID, name), &out)
	if errors.Is(err, pgx.ErrNoRows) {
		return LogSavedSearch{}, ErrNotFound
	}
	if err != nil {
		return LogSavedSearch{}, fmt.Errorf("state: read saved log search: %w", err)
	}
	return out, nil
}

// DeleteLogSavedSearch implements LogSavedSearchStore.
func (s *PgStore) DeleteLogSavedSearch(ctx context.Context, accountID, appID, name string) error {
	tag, err := s.pool.Exec(ctx,
		`delete from log_saved_searches where account_id = $1 and app_id = $2 and name = $3`, accountID, appID, name)
	if err != nil {
		return fmt.Errorf("state: delete saved log search: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func scanLogSavedSearch(row pgx.Row, out *LogSavedSearch) error {
	if err := row.Scan(&out.ID, &out.AccountID, &out.AppID, &out.Name, &out.Query, &out.CreatedAt, &out.UpdatedAt); err != nil {
		return err
	}
	out.CreatedAt, out.UpdatedAt = out.CreatedAt.UTC(), out.UpdatedAt.UTC()
	return nil
}
