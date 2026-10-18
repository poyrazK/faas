package state

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
)

var _ OutboundAIAccessTokenStore = (*PgStore)(nil)

// CreateOutboundAIAccessToken stores an app's access token (ADR-1431).
func (s *PgStore) CreateOutboundAIAccessToken(ctx context.Context, token OutboundAIAccessToken) (OutboundAIAccessToken, error) {
	if len(token.TokenHash) != 32 || len(token.IntegrationIDs) > api.OutboundAIAccessTokenIntegrationsMax {
		return OutboundAIAccessToken{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return OutboundAIAccessToken{}, mapErr(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var live int
	if err := tx.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM outbound_ai_access_tokens
		         WHERE app_id = app.id AND revoked_at IS NULL AND expires_at > now())
		  FROM apps app WHERE app.account_id = $1 AND app.id = $2 FOR UPDATE`,
		mustPgUUID(token.AccountID), mustPgUUID(token.AppID)).Scan(&live); errors.Is(err, pgx.ErrNoRows) {
		return OutboundAIAccessToken{}, ErrNotFound
	} else if err != nil {
		return OutboundAIAccessToken{}, mapErr(err)
	}
	if live >= api.OutboundAIAccessTokensPerApp {
		return OutboundAIAccessToken{}, ErrOutboundAIAccessTokenLimit
	}
	ids := make([]pgtype.UUID, 0, len(token.IntegrationIDs))
	for _, id := range token.IntegrationIDs {
		ids = append(ids, mustPgUUID(id))
	}
	if len(ids) > 0 {
		var allowed int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM outbound_integrations integration
			  JOIN outbound_app_bindings binding ON binding.integration_id = integration.id AND binding.app_id = $2
			 WHERE integration.account_id = $1 AND integration.id = ANY($3) AND integration.enabled
			   AND integration.ai_provider IS NOT NULL AND integration.owner_kind = 'customer'`,
			mustPgUUID(token.AccountID), mustPgUUID(token.AppID), ids).Scan(&allowed); err != nil {
			return OutboundAIAccessToken{}, mapErr(err)
		}
		if allowed != len(ids) {
			return OutboundAIAccessToken{}, ErrInvalidArgument
		}
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO outbound_ai_access_tokens (id, account_id, app_id, name, token_hash, token_prefix, integration_ids, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING created_at`, mustPgUUID(token.ID), mustPgUUID(token.AccountID), mustPgUUID(token.AppID), token.Name,
		token.TokenHash, token.Prefix, ids, token.ExpiresAt).Scan(&token.CreatedAt); err != nil {
		return OutboundAIAccessToken{}, mapErr(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return OutboundAIAccessToken{}, mapErr(err)
	}
	token.TokenHash = nil
	return token, nil
}

// ListOutboundAIAccessTokens lists an app's tokens, newest first, without
// their hashes.
func (s *PgStore) ListOutboundAIAccessTokens(ctx context.Context, accountID, appID string) ([]OutboundAIAccessToken, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, name, token_prefix, integration_ids, expires_at, created_at, last_used_at, revoked_at
		  FROM outbound_ai_access_tokens WHERE account_id = $1 AND app_id = $2
		 ORDER BY created_at DESC, id`, mustPgUUID(accountID), mustPgUUID(appID))
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := make([]OutboundAIAccessToken, 0)
	for rows.Next() {
		token := OutboundAIAccessToken{AccountID: accountID, AppID: appID}
		var ids []pgtype.UUID
		if err := rows.Scan(&token.ID, &token.Name, &token.Prefix, &ids, &token.ExpiresAt, &token.CreatedAt,
			&token.LastUsedAt, &token.RevokedAt); err != nil {
			return nil, mapErr(err)
		}
		token.IntegrationIDs = make([]string, 0, len(ids))
		for _, id := range ids {
			token.IntegrationIDs = append(token.IntegrationIDs, pgUUIDString(id))
		}
		out = append(out, token)
	}
	return out, mapErr(rows.Err())
}

// RevokeOutboundAIAccessToken revokes an app's token. outboundd's cache
// may accept it for OutboundAIAccessTokenCacheSeconds more.
func (s *PgStore) RevokeOutboundAIAccessToken(ctx context.Context, accountID, appID, tokenID string) error {
	command, err := s.pool.Exec(ctx, `
		UPDATE outbound_ai_access_tokens SET revoked_at = now()
		 WHERE account_id = $1 AND app_id = $2 AND id = $3 AND revoked_at IS NULL`,
		mustPgUUID(accountID), mustPgUUID(appID), mustPgUUID(tokenID))
	if err != nil {
		return mapErr(err)
	}
	if command.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}
