package outbound

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// AIAccessToken reads a live token by the hash of its secret (ADR-1431).
func (b *PostgresBackend) AIAccessToken(ctx context.Context, hash [32]byte) (AIAccessToken, error) {
	var token AIAccessToken
	var integrations []uuid.UUID
	err := b.pool.QueryRow(ctx, `
		SELECT id::text, account_id::text, app_id::text, integration_ids, expires_at
		  FROM outbound_ai_access_tokens
		 WHERE token_hash = $1 AND revoked_at IS NULL AND expires_at > now()`, hash[:]).
		Scan(&token.ID, &token.AccountID, &token.AppID, &integrations, &token.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return AIAccessToken{}, ErrAIAccessTokenNotFound
	}
	if err != nil {
		return AIAccessToken{}, err
	}
	token.IntegrationIDs = make(map[string]struct{}, len(integrations))
	for _, id := range integrations {
		token.IntegrationIDs[id.String()] = struct{}{}
	}
	return token, nil
}

// TouchAIAccessToken records a token's last use.
func (b *PostgresBackend) TouchAIAccessToken(ctx context.Context, id string) error {
	tokenID, err := uuid.Parse(id)
	if err != nil {
		return err
	}
	_, err = b.pool.Exec(ctx, `UPDATE outbound_ai_access_tokens SET last_used_at = now() WHERE id = $1`, tokenID)
	return err
}

var _ AIAccessTokenStore = (*PostgresBackend)(nil)

// StaticAccessTokens serves fixed tokens, keyed by their secret, for tests
// and development.
type StaticAccessTokens map[string]AIAccessToken

func (s StaticAccessTokens) AIAccessToken(_ context.Context, hash [32]byte) (AIAccessToken, error) {
	for secret, token := range s {
		if sha256.Sum256([]byte(secret)) == hash && time.Now().Before(token.ExpiresAt) {
			return token, nil
		}
	}
	return AIAccessToken{}, ErrAIAccessTokenNotFound
}

func (s StaticAccessTokens) TouchAIAccessToken(context.Context, string) error { return nil }
