// Package main (cmd/githubd) holds the state-SecretStore adapter
// (PR-D / ADR-012 §7 amendment). The adapter bridges
// pkg/state.PgStore.GetGithubWebhookSecret to the
// pkg/githubd.SecretStore interface that PGWebhookSecretResolver
// consumes. Lives in cmd/githubd (not pkg/githubd) because
// pgxpool.Pool is the one piece already in scope here — keeping
// the adapter next to the wiring makes the seam obvious.
package main

import (
	"context"
	"errors"

	"filippo.io/age"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/sealdomain"
	"github.com/onebox-faas/faas/pkg/state"
)

// stateSecretStoreAdapter is the production SecretStore. It opens the
// sealed copy of a row's secret (ADR 20261022111812) and falls back to the
// plaintext column only for a row written before the copy existed.
type stateSecretStoreAdapter struct {
	store      *state.PgStore
	identities []*age.X25519Identity
}

func newStateSecretStoreAdapter(pool *pgxpool.Pool, identities []*age.X25519Identity) *stateSecretStoreAdapter {
	return &stateSecretStoreAdapter{store: state.NewPgStore(pool), identities: identities}
}

// GetGithubWebhookSecret reads github_webhook_secrets. Missing row returns
// state.ErrNotFound, which the resolver treats as a per-tenant miss (and the
// server handler translates to fail-closed + the metric spike).
func (a *stateSecretStoreAdapter) GetGithubWebhookSecret(ctx context.Context, installationID int64) ([]byte, error) {
	row, err := a.store.GetGithubWebhookSecret(ctx, installationID)
	if err != nil {
		// state.ErrNotFound and the resolver's errSecretNotFound
		// are distinct (the resolver avoids importing pkg/state
		// to keep the import graph clean). The resolver's
		// isNotFound helper matches the sentinel string, so the
		// pass-through is safe.
		return nil, err
	}
	return webhookSecretFromRow(row, a.identities)
}

// webhookSecretFromRow prefers the sealed copy. A sealed copy that does
// not open is an error, never a silent fall back to the plaintext column:
// it means the identity is missing or the row was tampered with.
func webhookSecretFromRow(row state.GithubWebhookSecret, identities []*age.X25519Identity) ([]byte, error) {
	if len(row.Sealed) > 0 {
		if len(identities) == 0 {
			return nil, errors.New("githubd: sealed webhook secret but no identity loaded")
		}
		return sealdomain.OpenGithubWebhookSecret(identities, row.Sealed)
	}
	if len(row.Value) == 0 {
		// Defensive: a row with empty secret_value is a write
		// bug. Surface as ErrNotFound so the resolver falls back
		// to the platform secret rather than HMACing with [].
		return nil, errors.New("state: not found")
	}
	return row.Value, nil
}
