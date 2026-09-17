package state

import (
	"context"
	"encoding/json"
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
	if in.AllowedOrigins == nil {
		in.AllowedOrigins = []string{}
	}
	if in.AuthMode == "" {
		in.AuthMode = "none"
	}
	if in.AuthAudience == nil {
		in.AuthAudience = []string{}
	}
	if in.AuthAlgorithms == nil {
		in.AuthAlgorithms = []string{}
	}
	if in.AuthTokenPreviousSealed == nil {
		in.AuthTokenPreviousSealed = []byte{}
	}
	authClaims, err := json.Marshal(in.AuthRequiredClaims)
	if err != nil {
		return ManagedRealtimeEndpoint{}, fmt.Errorf("state: encode realtime auth claims: %w", err)
	}
	if len(authClaims) == 0 || string(authClaims) == "null" {
		authClaims = []byte(`{}`)
	}
	row := tx.QueryRow(ctx, `
		insert into managed_realtime_endpoints
			(id, app_id, account_id, callback_url, connect_path, message_path,
			 disconnect_path, callback_auth_token_sealed, auth_token_sealed,
			 auth_token_previous_sealed, auth_token_previous_expires_at,
			 auth_mode, auth_issuer, auth_jwks_url, auth_audience,
			 auth_algorithms, auth_required_claims, allowed_origins, max_connections, max_message_bytes,
			 max_connection_age_seconds, enabled)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22)
		returning id, app_id, account_id, callback_url, connect_path, message_path,
		          disconnect_path, callback_auth_token_sealed, auth_token_sealed,
		          auth_token_previous_sealed, auth_token_previous_expires_at,
		          auth_mode, auth_issuer, auth_jwks_url, auth_audience,
		          auth_algorithms, auth_required_claims, allowed_origins,
		          max_connections, max_message_bytes, max_connection_age_seconds,
		          enabled, created_at, updated_at
	`, in.ID, in.AppID, in.AccountID, in.CallbackURL, in.ConnectPath, in.MessagePath,
		in.DisconnectPath, in.CallbackAuthTokenSealed, in.AuthTokenSealed,
		in.AuthTokenPreviousSealed, in.AuthTokenPreviousExpiresAt,
		in.AuthMode, in.AuthIssuer, in.AuthJWKSURL, in.AuthAudience,
		in.AuthAlgorithms, authClaims, in.AllowedOrigins, in.MaxConnections,
		in.MaxMessageBytes, in.MaxConnectionAgeSeconds, in.Enabled)
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
		       auth_token_previous_sealed, auth_token_previous_expires_at,
		       auth_mode, auth_issuer, auth_jwks_url, auth_audience,
		       auth_algorithms, auth_required_claims, allowed_origins,
		       max_connections, max_message_bytes,
		       max_connection_age_seconds, enabled, created_at, updated_at
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
	if p.AuthTokenPreviousSealed != nil {
		current.AuthTokenPreviousSealed = append([]byte(nil), (*p.AuthTokenPreviousSealed)...)
	}
	if p.AuthTokenPreviousExpiresAt != nil {
		current.AuthTokenPreviousExpiresAt = cloneManagedRealtimeTime(p.AuthTokenPreviousExpiresAt)
	}
	if p.ClearAuthTokenPreviousExpiresAt {
		current.AuthTokenPreviousExpiresAt = nil
	}
	if p.AuthMode != nil {
		current.AuthMode = *p.AuthMode
	}
	if p.AuthIssuer != nil {
		current.AuthIssuer = *p.AuthIssuer
	}
	if p.AuthJWKSURL != nil {
		current.AuthJWKSURL = *p.AuthJWKSURL
	}
	if p.AuthAudience != nil {
		current.AuthAudience = append([]string(nil), (*p.AuthAudience)...)
	}
	if p.AuthAlgorithms != nil {
		current.AuthAlgorithms = append([]string(nil), (*p.AuthAlgorithms)...)
	}
	if p.AuthRequiredClaims != nil {
		current.AuthRequiredClaims = cloneManagedRealtimeClaims(*p.AuthRequiredClaims)
	}
	if p.AllowedOrigins != nil {
		current.AllowedOrigins = append([]string(nil), (*p.AllowedOrigins)...)
	}
	if p.MaxConnections != nil {
		current.MaxConnections = *p.MaxConnections
	}
	if p.MaxMessageBytes != nil {
		current.MaxMessageBytes = *p.MaxMessageBytes
	}
	if p.MaxConnectionAgeSeconds != nil {
		current.MaxConnectionAgeSeconds = *p.MaxConnectionAgeSeconds
	}
	if p.Enabled != nil {
		current.Enabled = *p.Enabled
	}
	row := s.pool.QueryRow(ctx, `
		update managed_realtime_endpoints set
			callback_url = $2, connect_path = $3, message_path = $4,
			disconnect_path = $5, callback_auth_token_sealed = $6,
			auth_token_sealed = $7, auth_token_previous_sealed = $8,
			auth_token_previous_expires_at = $9, auth_mode = $10, auth_issuer = $11,
			auth_jwks_url = $12, auth_audience = $13, auth_algorithms = $14,
			auth_required_claims = $15, allowed_origins = $16,
			max_connections = $17, max_message_bytes = $18,
			max_connection_age_seconds = $19, enabled = $20, updated_at = now()
		where id = $1
		returning id, app_id, account_id, callback_url, connect_path, message_path,
		          disconnect_path, callback_auth_token_sealed, auth_token_sealed,
		          auth_token_previous_sealed, auth_token_previous_expires_at,
		          auth_mode, auth_issuer, auth_jwks_url, auth_audience,
		          auth_algorithms, auth_required_claims, allowed_origins,
		          max_connections, max_message_bytes, max_connection_age_seconds,
		          enabled, created_at, updated_at
	`, id, current.CallbackURL, current.ConnectPath, current.MessagePath,
		current.DisconnectPath, current.CallbackAuthTokenSealed, current.AuthTokenSealed,
		current.AuthTokenPreviousSealed, current.AuthTokenPreviousExpiresAt,
		current.AuthMode, current.AuthIssuer, current.AuthJWKSURL, current.AuthAudience,
		current.AuthAlgorithms, mustMarshalManagedRealtimeClaims(current.AuthRequiredClaims),
		current.AllowedOrigins, current.MaxConnections, current.MaxMessageBytes,
		current.MaxConnectionAgeSeconds, current.Enabled)
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
		       auth_token_previous_sealed, auth_token_previous_expires_at,
		       auth_mode, auth_issuer, auth_jwks_url, auth_audience,
		       auth_algorithms, auth_required_claims, allowed_origins,
		       max_connections, max_message_bytes,
		       max_connection_age_seconds, enabled, created_at, updated_at
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
		       auth_token_previous_sealed, auth_token_previous_expires_at,
		       auth_mode, auth_issuer, auth_jwks_url, auth_audience,
		       auth_algorithms, auth_required_claims, allowed_origins,
		       max_connections, max_message_bytes,
		       max_connection_age_seconds, enabled, created_at, updated_at
		  from managed_realtime_endpoints where account_id = $1 order by created_at desc
	`, accountID)
	if err != nil {
		return nil, fmt.Errorf("state: list realtime endpoints for account: %w", err)
	}
	defer rows.Close()
	return scanManagedRealtimeEndpoints(rows)
}

func (s *PgStore) ListManagedRealtimeEndpoints(ctx context.Context) ([]ManagedRealtimeEndpoint, error) {
	rows, err := s.pool.Query(ctx, `
		select id, app_id, account_id, callback_url, connect_path, message_path,
		       disconnect_path, callback_auth_token_sealed, auth_token_sealed,
		       auth_token_previous_sealed, auth_token_previous_expires_at,
		       auth_mode, auth_issuer, auth_jwks_url, auth_audience,
		       auth_algorithms, auth_required_claims, allowed_origins,
		       max_connections, max_message_bytes,
		       max_connection_age_seconds, enabled, created_at, updated_at
		  from managed_realtime_endpoints order by created_at desc
	`)
	if err != nil {
		return nil, fmt.Errorf("state: list realtime endpoints: %w", err)
	}
	defer rows.Close()
	return scanManagedRealtimeEndpoints(rows)
}

type managedRealtimeEndpointScanner interface{ Scan(dest ...any) error }

func scanManagedRealtimeEndpoint(s managedRealtimeEndpointScanner) (ManagedRealtimeEndpoint, error) {
	var e ManagedRealtimeEndpoint
	var authClaims []byte
	if err := s.Scan(&e.ID, &e.AppID, &e.AccountID, &e.CallbackURL, &e.ConnectPath, &e.MessagePath,
		&e.DisconnectPath, &e.CallbackAuthTokenSealed, &e.AuthTokenSealed,
		&e.AuthTokenPreviousSealed, &e.AuthTokenPreviousExpiresAt,
		&e.AuthMode, &e.AuthIssuer, &e.AuthJWKSURL, &e.AuthAudience,
		&e.AuthAlgorithms, &authClaims, &e.AllowedOrigins, &e.MaxConnections,
		&e.MaxMessageBytes, &e.MaxConnectionAgeSeconds,
		&e.Enabled, &e.CreatedAt, &e.UpdatedAt); err != nil {
		return ManagedRealtimeEndpoint{}, err
	}
	e.AuthRequiredClaims = map[string]string{}
	if len(authClaims) > 0 {
		if err := json.Unmarshal(authClaims, &e.AuthRequiredClaims); err != nil {
			return ManagedRealtimeEndpoint{}, fmt.Errorf("state: decode realtime auth claims: %w", err)
		}
	}
	return e, nil
}

func mustMarshalManagedRealtimeClaims(values map[string]string) []byte {
	encoded, err := json.Marshal(values)
	if err != nil || len(encoded) == 0 || string(encoded) == "null" {
		return []byte(`{}`)
	}
	return encoded
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
