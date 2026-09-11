package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const prewarmSelect = `id, app_id, account_id, count, wake_at, expires_at,
       trigger, status, created_at, claimed_at, fired_at, admitted_count, outcome, last_error`

func (s *PgStore) CreatePrewarmIntent(ctx context.Context, appID, accountID string, count int, wakeAt, expiresAt time.Time, trigger string) (PrewarmIntent, error) {
	if err := ValidatePrewarmIntent(count, wakeAt, expiresAt, time.Now(), trigger); err != nil {
		return PrewarmIntent{}, err
	}
	row := s.pool.QueryRow(ctx, `
		insert into prewarm_intents (app_id, account_id, count, wake_at, expires_at, trigger)
		select $1, $2, $3, $4, $5, $6
		where exists (select 1 from apps where id = $1 and account_id = $2 and status <> 'deleted')
		returning `+prewarmSelect, appID, accountID, count, wakeAt.UTC(), expiresAt.UTC(), trigger)
	intent, err := scanPrewarmRow(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PrewarmIntent{}, ErrNotFound
		}
		return PrewarmIntent{}, mapErr(err)
	}
	return intent, nil
}

func (s *PgStore) PrewarmIntentByID(ctx context.Context, id string) (PrewarmIntent, error) {
	intent, err := scanPrewarmRow(s.pool.QueryRow(ctx, `select `+prewarmSelect+` from prewarm_intents where id = $1`, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PrewarmIntent{}, ErrNotFound
		}
		return PrewarmIntent{}, mapErr(err)
	}
	return intent, nil
}

func (s *PgStore) ListPrewarmIntentsForApp(ctx context.Context, appID string, limit int) ([]PrewarmIntent, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `select `+prewarmSelect+` from prewarm_intents where app_id = $1 order by wake_at, created_at limit $2`, appID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPrewarmRows(rows)
}

func (s *PgStore) ListDuePrewarmIntents(ctx context.Context, before, now time.Time, limit int) ([]PrewarmIntent, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `select `+prewarmSelect+` from prewarm_intents
		where status = 'pending' and wake_at <= $1 and expires_at > $2
		order by wake_at, created_at limit $3`, before.UTC(), now.UTC(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPrewarmRows(rows)
}

func (s *PgStore) ListExpiredPrewarmIntents(ctx context.Context, now time.Time, limit int) ([]PrewarmIntent, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `select `+prewarmSelect+` from prewarm_intents
		where status = 'pending' and expires_at <= $1
		order by expires_at, created_at limit $2`, now.UTC(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPrewarmRows(rows)
}

func (s *PgStore) ActivePrewarmFloor(ctx context.Context, appID string, now time.Time) (int, error) {
	var floor int
	if err := s.pool.QueryRow(ctx, `select coalesce(max(case when status = 'succeeded' then admitted_count else count end), 0) from prewarm_intents
		where app_id = $1 and expires_at > $2 and status in ('running', 'succeeded')`, appID, now.UTC()).Scan(&floor); err != nil {
		return 0, err
	}
	return floor, nil
}

func (s *PgStore) ClaimPrewarmIntent(ctx context.Context, id string, claimedAt time.Time) (PrewarmIntent, bool, error) {
	intent, err := scanPrewarmRow(s.pool.QueryRow(ctx, `update prewarm_intents
		set status = 'running', claimed_at = $2
		where id = $1 and status = 'pending' and expires_at > $2
		returning `+prewarmSelect, id, claimedAt.UTC()))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// A missing id and a row claimed by another schedd are distinct
			// to callers, so preserve ErrNotFound for the former.
			if _, lookupErr := s.PrewarmIntentByID(ctx, id); lookupErr != nil {
				return PrewarmIntent{}, false, lookupErr
			}
			return PrewarmIntent{}, false, nil
		}
		return PrewarmIntent{}, false, mapErr(err)
	}
	return intent, true, nil
}

func (s *PgStore) CompletePrewarmIntent(ctx context.Context, id string, firedAt time.Time, admittedCount int, outcome string) error {
	tag, err := s.pool.Exec(ctx, `update prewarm_intents
		set status = 'succeeded', fired_at = $2, admitted_count = $3, outcome = $4
		where id = $1 and status = 'running'`, id, firedAt.UTC(), admittedCount, outcome)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		if _, lookupErr := s.PrewarmIntentByID(ctx, id); lookupErr != nil {
			return lookupErr
		}
	}
	return nil
}

func (s *PgStore) FailPrewarmIntent(ctx context.Context, id string, firedAt time.Time, cause string) error {
	if len(cause) > 2048 {
		cause = cause[:2048]
	}
	tag, err := s.pool.Exec(ctx, `update prewarm_intents
		set status = 'failed', fired_at = $2, last_error = $3
		where id = $1 and status = 'running'`, id, firedAt.UTC(), cause)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		if _, lookupErr := s.PrewarmIntentByID(ctx, id); lookupErr != nil {
			return lookupErr
		}
	}
	return nil
}

func (s *PgStore) ExpirePrewarmIntent(ctx context.Context, id string, expiredAt time.Time) (bool, error) {
	tag, err := s.pool.Exec(ctx, `update prewarm_intents
		set status = 'failed', fired_at = $2, outcome = 'expired', last_error = 'expired'
		where id = $1 and status = 'pending' and expires_at <= $2`, id, expiredAt.UTC())
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() > 0 {
		return true, nil
	}
	if _, lookupErr := s.PrewarmIntentByID(ctx, id); lookupErr != nil {
		return false, lookupErr
	}
	return false, nil
}

func (s *PgStore) CancelPrewarmIntent(ctx context.Context, id, accountID string) error {
	tag, err := s.pool.Exec(ctx, `update prewarm_intents set status = 'cancelled'
		where id = $1 and account_id = $2 and status = 'pending'`, id, accountID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func scanPrewarmRows(rows pgx.Rows) ([]PrewarmIntent, error) {
	out := make([]PrewarmIntent, 0)
	for rows.Next() {
		intent, err := scanPrewarmRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, intent)
	}
	return out, rows.Err()
}

func scanPrewarmRow(row interface{ Scan(...any) error }) (PrewarmIntent, error) {
	var intent PrewarmIntent
	var claimedAt, firedAt pgtype.Timestamptz
	if err := row.Scan(&intent.ID, &intent.AppID, &intent.AccountID, &intent.Count,
		&intent.WakeAt, &intent.ExpiresAt, &intent.Trigger, &intent.Status,
		&intent.CreatedAt, &claimedAt, &firedAt, &intent.AdmittedCount, &intent.Outcome, &intent.LastError); err != nil {
		return PrewarmIntent{}, fmt.Errorf("scan prewarm intent: %w", err)
	}
	if claimedAt.Valid {
		v := claimedAt.Time
		intent.ClaimedAt = &v
	}
	if firedAt.Valid {
		v := firedAt.Time
		intent.FiredAt = &v
	}
	return intent, nil
}
