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
// Only the app's creator, as a company owner or admin, can mint one (the
// route has no reviewed company action), so a token of a company app works
// only while that creator still holds that standing, the company is active
// and the app is not deleted, as fork tokens do (#5589). A creator who leaves
// takes the app's tokens out of service with them.
func (b *PostgresBackend) AIAccessToken(ctx context.Context, hash [32]byte) (AIAccessToken, error) {
	var token AIAccessToken
	var integrations []uuid.UUID
	err := b.pool.QueryRow(ctx, `
		SELECT t.id::text, t.account_id::text, t.app_id::text, t.integration_ids, t.expires_at
		  FROM outbound_ai_access_tokens t
		  JOIN apps a ON a.id = t.app_id
		 WHERE t.token_hash = $1 AND t.revoked_at IS NULL AND t.expires_at > now()
		   AND a.status <> 'deleted'
		   AND (a.org_id IS NULL OR EXISTS (
		        SELECT 1 FROM orgs o
		         WHERE o.id = a.org_id
		           AND ((o.personal_org AND o.personal_owner_account_id = a.account_id)
		                OR (NOT o.personal_org AND NOT o.deleted_pending
		                    AND o.status NOT IN ('deleted_pending', 'suspended', 'past_due')
		                    AND EXISTS (SELECT 1 FROM org_memberships m
		                                 WHERE m.org_id = o.id AND m.account_id = a.account_id
		                                   AND m.removed_at IS NULL AND m.role IN ('owner', 'admin'))))))`, hash[:]).
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
