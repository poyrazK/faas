package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

func (s *PgStore) CreateManagedRealtimeEndpointIfUnderQuota(ctx context.Context, in ManagedRealtimeEndpoint, perApp, perAccount int) (ManagedRealtimeEndpoint, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return ManagedRealtimeEndpoint{}, fmt.Errorf("state: begin realtime endpoint tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck

	var accountID string
	if err := tx.QueryRow(ctx, `
		select account_id from apps where id = $1 and status <> 'deleted' for update
	`, in.AppID).Scan(&accountID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ManagedRealtimeEndpoint{}, ErrNotFound
		}
		return ManagedRealtimeEndpoint{}, fmt.Errorf("state: lock app for realtime endpoint: %w", err)
	}
	if accountID != in.AccountID {
		return ManagedRealtimeEndpoint{}, ErrNotFound
	}
	var appCount int
	if err := tx.QueryRow(ctx, `select count(*) from managed_realtime_endpoints where app_id = $1`, in.AppID).Scan(&appCount); err != nil {
		return ManagedRealtimeEndpoint{}, fmt.Errorf("state: count realtime endpoints for app: %w", err)
	}
	if appCount >= perApp {
		return ManagedRealtimeEndpoint{}, &ManagedRealtimeEndpointQuotaError{Scope: ManagedRealtimeEndpointQuotaScopeApp, Limit: perApp, Observed: appCount}
	}
	var accountCount int
	if err := tx.QueryRow(ctx, `
		select count(*) from managed_realtime_endpoints e
		join apps a on a.id = e.app_id
		where a.account_id = $1 and a.status <> 'deleted'
	`, accountID).Scan(&accountCount); err != nil {
		return ManagedRealtimeEndpoint{}, fmt.Errorf("state: count realtime endpoints for account: %w", err)
	}
	if accountCount >= perAccount {
		return ManagedRealtimeEndpoint{}, &ManagedRealtimeEndpointQuotaError{Scope: ManagedRealtimeEndpointQuotaScopeAccount, Limit: perAccount, Observed: accountCount}
	}
	if in.ID == "" {
		in.ID = newID()
	}
	row := tx.QueryRow(ctx, `
		insert into managed_realtime_endpoints
			(id, app_id, account_id, callback_url, connect_path, message_path,
			 disconnect_path, callback_auth_token_sealed, auth_token_sealed, enabled)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		returning id, app_id, account_id, callback_url, connect_path, message_path,
		          disconnect_path, callback_auth_token_sealed, auth_token_sealed,
		          enabled, created_at, updated_at
	`, in.ID, in.AppID, in.AccountID, in.CallbackURL, in.ConnectPath, in.MessagePath,
		in.DisconnectPath, in.CallbackAuthTokenSealed, in.AuthTokenSealed, in.Enabled)
	endpoint, err := scanManagedRealtimeEndpoint(row)
	if err != nil {
		if isUniqueViolation(err) {
			return ManagedRealtimeEndpoint{}, ErrConflict
		}
		return ManagedRealtimeEndpoint{}, fmt.Errorf("state: insert realtime endpoint: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return ManagedRealtimeEndpoint{}, fmt.Errorf("state: commit realtime endpoint: %w", err)
	}
	return endpoint, nil
}

func (s *PgStore) ManagedRealtimeEndpointByID(ctx context.Context, id string) (ManagedRealtimeEndpoint, error) {
	row := s.pool.QueryRow(ctx, `
		select id, app_id, account_id, callback_url, connect_path, message_path,
		       disconnect_path, callback_auth_token_sealed, auth_token_sealed,
		       enabled, created_at, updated_at
		  from managed_realtime_endpoints where id = $1
	`, id)
	e, err := scanManagedRealtimeEndpoint(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ManagedRealtimeEndpoint{}, ErrNotFound
		}
		return ManagedRealtimeEndpoint{}, fmt.Errorf("state: read realtime endpoint: %w", err)
	}
	return e, nil
}

func (s *PgStore) UpdateManagedRealtimeEndpoint(ctx context.Context, id string, p UpdateManagedRealtimeEndpointParams) (ManagedRealtimeEndpoint, error) {
	current, err := s.ManagedRealtimeEndpointByID(ctx, id)
	if err != nil {
		return ManagedRealtimeEndpoint{}, err
	}
	if p.CallbackURL != nil {
		current.CallbackURL = *p.CallbackURL
	}
	if p.ConnectPath != nil {
		current.ConnectPath = *p.ConnectPath
	}
	if p.MessagePath != nil {
		current.MessagePath = *p.MessagePath
	}
	if p.DisconnectPath != nil {
		current.DisconnectPath = *p.DisconnectPath
	}
	if p.CallbackAuthTokenSealed != nil {
		current.CallbackAuthTokenSealed = append([]byte(nil), (*p.CallbackAuthTokenSealed)...)
	}
	if p.AuthTokenSealed != nil {
		current.AuthTokenSealed = append([]byte(nil), (*p.AuthTokenSealed)...)
	}
	if p.Enabled != nil {
		current.Enabled = *p.Enabled
	}
	row := s.pool.QueryRow(ctx, `
		update managed_realtime_endpoints set
			callback_url = $2, connect_path = $3, message_path = $4,
			disconnect_path = $5, callback_auth_token_sealed = $6,
			auth_token_sealed = $7, enabled = $8, updated_at = now()
		where id = $1
		returning id, app_id, account_id, callback_url, connect_path, message_path,
		          disconnect_path, callback_auth_token_sealed, auth_token_sealed,
		          enabled, created_at, updated_at
	`, id, current.CallbackURL, current.ConnectPath, current.MessagePath,
		current.DisconnectPath, current.CallbackAuthTokenSealed, current.AuthTokenSealed, current.Enabled)
	e, err := scanManagedRealtimeEndpoint(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ManagedRealtimeEndpoint{}, ErrNotFound
		}
		return ManagedRealtimeEndpoint{}, fmt.Errorf("state: update realtime endpoint: %w", err)
	}
	return e, nil
}

func (s *PgStore) DeleteManagedRealtimeEndpoint(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `delete from managed_realtime_endpoints where id = $1`, id)
	if err != nil {
		return fmt.Errorf("state: delete realtime endpoint: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PgStore) ListManagedRealtimeEndpointsForApp(ctx context.Context, appID string) ([]ManagedRealtimeEndpoint, error) {
	rows, err := s.pool.Query(ctx, `
		select id, app_id, account_id, callback_url, connect_path, message_path,
		       disconnect_path, callback_auth_token_sealed, auth_token_sealed,
		       enabled, created_at, updated_at
		  from managed_realtime_endpoints where app_id = $1 order by created_at desc
	`, appID)
	if err != nil {
		return nil, fmt.Errorf("state: list realtime endpoints for app: %w", err)
	}
	defer rows.Close()
	return scanManagedRealtimeEndpoints(rows)
}

func (s *PgStore) ListManagedRealtimeEndpointsForAccount(ctx context.Context, accountID string) ([]ManagedRealtimeEndpoint, error) {
	rows, err := s.pool.Query(ctx, `
		select id, app_id, account_id, callback_url, connect_path, message_path,
		       disconnect_path, callback_auth_token_sealed, auth_token_sealed,
		       enabled, created_at, updated_at
		  from managed_realtime_endpoints where account_id = $1 order by created_at desc
	`, accountID)
	if err != nil {
		return nil, fmt.Errorf("state: list realtime endpoints for account: %w", err)
	}
	defer rows.Close()
	return scanManagedRealtimeEndpoints(rows)
}

type managedRealtimeEndpointScanner interface{ Scan(dest ...any) error }

func scanManagedRealtimeEndpoint(s managedRealtimeEndpointScanner) (ManagedRealtimeEndpoint, error) {
	var e ManagedRealtimeEndpoint
	if err := s.Scan(&e.ID, &e.AppID, &e.AccountID, &e.CallbackURL, &e.ConnectPath, &e.MessagePath,
		&e.DisconnectPath, &e.CallbackAuthTokenSealed, &e.AuthTokenSealed, &e.Enabled, &e.CreatedAt, &e.UpdatedAt); err != nil {
		return ManagedRealtimeEndpoint{}, err
	}
	return e, nil
}

func scanManagedRealtimeEndpoints(rows pgx.Rows) ([]ManagedRealtimeEndpoint, error) {
	out := make([]ManagedRealtimeEndpoint, 0)
	for rows.Next() {
		e, err := scanManagedRealtimeEndpoint(rows)
		if err != nil {
			return nil, fmt.Errorf("state: scan realtime endpoint: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: realtime endpoint rows: %w", err)
	}
	return out, nil
}
