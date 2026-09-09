package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/api"
)

func (s *PgStore) CreateAppLogDrain(ctx context.Context, in AppLogDrain) (AppLogDrain, error) {
	row := s.pool.QueryRow(ctx, `
		insert into app_log_drains
			(app_id, account_id, kind, target_url, auth_header_sealed, enabled)
		values ($1, $2, $3, $4, $5, $6)
		returning id, app_id, account_id, kind, target_url,
		          auth_header_sealed, enabled, created_at, updated_at
	`, in.AppID, in.AccountID, string(in.Kind), in.TargetURL, in.AuthHeaderSealed, in.Enabled)
	drain, err := scanAppLogDrain(row)
	if err != nil {
		if isUniqueViolation(err) {
			return AppLogDrain{}, ErrConflict
		}
		return AppLogDrain{}, fmt.Errorf("state: insert app_log_drain: %w", err)
	}
	return drain, nil
}

func (s *PgStore) CreateAppLogDrainIfUnderQuota(ctx context.Context, in AppLogDrain, limits api.Limits) (AppLogDrain, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return AppLogDrain{}, fmt.Errorf("state: begin app_log_drain tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck

	var accountID string
	if err := tx.QueryRow(ctx, `
		select account_id from apps where id = $1 and status <> 'deleted' for update
	`, in.AppID).Scan(&accountID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AppLogDrain{}, ErrNotFound
		}
		return AppLogDrain{}, fmt.Errorf("state: lock app %s for log drain: %w", in.AppID, err)
	}
	if _, err := tx.Exec(ctx, `select 1 from accounts where id = $1 for update`, accountID); err != nil {
		return AppLogDrain{}, fmt.Errorf("state: lock account %s for log drain: %w", accountID, err)
	}

	var appCount int
	if err := tx.QueryRow(ctx, `select count(*) from app_log_drains where app_id = $1`, in.AppID).Scan(&appCount); err != nil {
		return AppLogDrain{}, fmt.Errorf("state: count app log drains for app %s: %w", in.AppID, err)
	}
	if appCount >= limits.LogDrainPerApp {
		return AppLogDrain{}, &AppLogDrainQuotaError{Scope: AppLogDrainQuotaScopeApp, Limit: limits.LogDrainPerApp, Observed: appCount}
	}

	var accountCount int
	if err := tx.QueryRow(ctx, `
		select count(*) from app_log_drains d
		join apps a on a.id = d.app_id
		where a.account_id = $1 and a.status <> 'deleted'
	`, accountID).Scan(&accountCount); err != nil {
		return AppLogDrain{}, fmt.Errorf("state: count app log drains for account %s: %w", accountID, err)
	}
	if accountCount >= limits.LogDrainPerAccount {
		return AppLogDrain{}, &AppLogDrainQuotaError{Scope: AppLogDrainQuotaScopeAccount, Limit: limits.LogDrainPerAccount, Observed: accountCount}
	}

	row := tx.QueryRow(ctx, `
		insert into app_log_drains
			(app_id, account_id, kind, target_url, auth_header_sealed, enabled)
		values ($1, $2, $3, $4, $5, $6)
		returning id, app_id, account_id, kind, target_url,
		          auth_header_sealed, enabled, created_at, updated_at
	`, in.AppID, accountID, string(in.Kind), in.TargetURL, in.AuthHeaderSealed, in.Enabled)
	drain, err := scanAppLogDrain(row)
	if err != nil {
		if isUniqueViolation(err) {
			return AppLogDrain{}, ErrConflict
		}
		return AppLogDrain{}, fmt.Errorf("state: insert app_log_drain: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return AppLogDrain{}, fmt.Errorf("state: commit app_log_drain: %w", err)
	}
	return drain, nil
}

func (s *PgStore) AppLogDrainByID(ctx context.Context, id string) (AppLogDrain, error) {
	row := s.pool.QueryRow(ctx, `
		select id, app_id, account_id, kind, target_url,
		       auth_header_sealed, enabled, created_at, updated_at
		from app_log_drains where id = $1
	`, id)
	drain, err := scanAppLogDrain(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AppLogDrain{}, ErrNotFound
		}
		return AppLogDrain{}, fmt.Errorf("state: read app_log_drain: %w", err)
	}
	return drain, nil
}

func (s *PgStore) UpdateAppLogDrain(ctx context.Context, id string, p UpdateAppLogDrainParams) (AppLogDrain, error) {
	current, err := s.AppLogDrainByID(ctx, id)
	if err != nil {
		return AppLogDrain{}, err
	}
	if p.Kind != nil {
		current.Kind = *p.Kind
	}
	if p.TargetURL != nil {
		current.TargetURL = *p.TargetURL
	}
	if p.AuthHeaderSealed != nil {
		current.AuthHeaderSealed = append([]byte(nil), (*p.AuthHeaderSealed)...)
	}
	if p.Enabled != nil {
		current.Enabled = *p.Enabled
	}
	row := s.pool.QueryRow(ctx, `
		update app_log_drains set
			kind = $2, target_url = $3, auth_header_sealed = $4,
			enabled = $5, updated_at = now()
		where id = $1
		returning id, app_id, account_id, kind, target_url,
		          auth_header_sealed, enabled, created_at, updated_at
	`, id, string(current.Kind), current.TargetURL, current.AuthHeaderSealed, current.Enabled)
	drain, err := scanAppLogDrain(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AppLogDrain{}, ErrNotFound
		}
		if isUniqueViolation(err) {
			return AppLogDrain{}, ErrConflict
		}
		return AppLogDrain{}, fmt.Errorf("state: update app_log_drain: %w", err)
	}
	return drain, nil
}

func (s *PgStore) DeleteAppLogDrain(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `delete from app_log_drains where id = $1`, id)
	if err != nil {
		return fmt.Errorf("state: delete app_log_drain: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PgStore) ListAppLogDrainsForApp(ctx context.Context, appID string) ([]AppLogDrain, error) {
	return s.listAppLogDrains(ctx, `
		select id, app_id, account_id, kind, target_url,
		       auth_header_sealed, enabled, created_at, updated_at
		from app_log_drains where app_id = $1 order by created_at, id
	`, appID)
}

func (s *PgStore) ListEnabledAppLogDrains(ctx context.Context) ([]AppLogDrain, error) {
	return s.listAppLogDrains(ctx, `
		select id, app_id, account_id, kind, target_url,
		       auth_header_sealed, enabled, created_at, updated_at
		from app_log_drains where enabled order by created_at, id
	`)
}

func (s *PgStore) listAppLogDrains(ctx context.Context, query string, args ...any) ([]AppLogDrain, error) {
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("state: list app log drains: %w", err)
	}
	defer rows.Close()
	out := make([]AppLogDrain, 0)
	for rows.Next() {
		drain, scanErr := scanAppLogDrain(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("state: scan app_log_drain: %w", scanErr)
		}
		out = append(out, drain)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: list app_log_drains rows: %w", err)
	}
	return out, nil
}

type appLogDrainRow interface {
	Scan(dest ...any) error
}

func scanAppLogDrain(row appLogDrainRow) (AppLogDrain, error) {
	var drain AppLogDrain
	var kind string
	if err := row.Scan(&drain.ID, &drain.AppID, &drain.AccountID, &kind,
		&drain.TargetURL, &drain.AuthHeaderSealed, &drain.Enabled,
		&drain.CreatedAt, &drain.UpdatedAt); err != nil {
		return AppLogDrain{}, err
	}
	drain.Kind = AppLogDrainKind(kind)
	return drain, nil
}
