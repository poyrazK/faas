package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Break-glass credential kinds (ADR 20261022140000): every credential a
// customer or operator presents to Gregale's control plane that a bulk
// revoke can end at once. Customer application credentials (consumer keys,
// realtime and data credentials) are not here: revoking them would take
// customers' own apps down.
const (
	BreakGlassSessions           = "sessions"
	BreakGlassAPIKeys            = "api_keys"
	BreakGlassAIAccessTokens     = "outbound_ai_access_tokens"
	BreakGlassTenantAccessTokens = "platform_tenant_access_tokens"
)

// BreakGlassCredentialKinds lists every kind in the order they are revoked.
var BreakGlassCredentialKinds = []string{BreakGlassSessions, BreakGlassAPIKeys, BreakGlassAIAccessTokens, BreakGlassTenantAccessTokens}

// breakGlassQueries maps a kind to the count of its live rows and the
// statement that revokes them. Static SQL only.
var breakGlassQueries = map[string]struct{ count, revoke string }{
	BreakGlassSessions: {
		count:  `SELECT count(*) FROM sessions WHERE revoked_at IS NULL`,
		revoke: `UPDATE sessions SET revoked_at = now() WHERE revoked_at IS NULL`,
	},
	BreakGlassAPIKeys: {
		count:  `SELECT count(*) FROM api_keys WHERE status <> 'revoked'`,
		revoke: `UPDATE api_keys SET status = 'revoked', revoked_at = coalesce(revoked_at, now()) WHERE status <> 'revoked'`,
	},
	BreakGlassAIAccessTokens: {
		count:  `SELECT count(*) FROM outbound_ai_access_tokens WHERE revoked_at IS NULL AND expires_at > now()`,
		revoke: `UPDATE outbound_ai_access_tokens SET revoked_at = now() WHERE revoked_at IS NULL AND expires_at > now()`,
	},
	BreakGlassTenantAccessTokens: {
		count:  `SELECT count(*) FROM platform_tenant_access_tokens WHERE revoked_at IS NULL AND expires_at > now()`,
		revoke: `UPDATE platform_tenant_access_tokens SET revoked_at = now() WHERE revoked_at IS NULL AND expires_at > now()`,
	},
}

// BreakGlassRevocation is one kind's outcome.
type BreakGlassRevocation struct {
	Kind  string `json:"kind"`
	Count int64  `json:"count"`
}

// RevokeAllCredentials revokes every live credential of the given kinds in
// one transaction and records one audit event, or with dryRun only counts
// them. The operator running it is signed out too.
func (s *PgStore) RevokeAllCredentials(ctx context.Context, kinds []string, actor, reason string, dryRun bool) ([]BreakGlassRevocation, error) {
	if len(kinds) == 0 {
		return nil, errors.New("state: break-glass revoke needs at least one credential kind")
	}
	for _, k := range kinds {
		if _, ok := breakGlassQueries[k]; !ok {
			return nil, fmt.Errorf("state: unknown break-glass credential kind %q", k)
		}
	}
	if !dryRun && (actor == "" || reason == "") {
		return nil, errors.New("state: break-glass revoke needs an actor and a reason")
	}
	var out []BreakGlassRevocation
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		for _, k := range kinds {
			q := breakGlassQueries[k]
			r := BreakGlassRevocation{Kind: k}
			if dryRun {
				if err := tx.QueryRow(ctx, q.count).Scan(&r.Count); err != nil {
					return fmt.Errorf("count %s: %w", k, err)
				}
			} else {
				tag, err := tx.Exec(ctx, q.revoke)
				if err != nil {
					return fmt.Errorf("revoke %s: %w", k, err)
				}
				r.Count = tag.RowsAffected()
			}
			out = append(out, r)
		}
		if dryRun {
			return nil
		}
		data, err := json.Marshal(map[string]any{"revoked": out, "reason": reason})
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO events (actor, kind, data) VALUES ($1, 'break_glass.credentials_revoked', $2)`, actor, data)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("state: break-glass revoke: %w", err)
	}
	return out, nil
}
