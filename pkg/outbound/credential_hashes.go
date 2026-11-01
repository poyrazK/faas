package outbound

import (
	"bytes"
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/credhash"
)

// Outbound integration and AI access tokens belong to the outbound
// credential family (ADR 20261029202403 amendment 1): outboundd and apid
// hold its ring. Each column binds its digests.
const (
	IntegrationTokenColumn credhash.Tag = "outbound_integrations.token_hash"
	AIAccessTokenColumn    credhash.Tag = "outbound_ai_access_tokens.token_hash"
)

// storedIntegrationToken is the token_hash to write for an operator
// integration whose token hashes to sum. When the row already holds the same
// token under another form (plain, or an older key), the rewrite is marked
// for the transaction so the promotion-revision trigger ignores it
// (amendment 1, decision 5); a new token still bumps bound apps' revisions.
func storedIntegrationToken(ctx context.Context, tx pgx.Tx, id uuid.UUID, sum [32]byte) ([]byte, error) {
	policy := credhash.For(credhash.FamilyOutbound)
	next := policy.Store(IntegrationTokenColumn, sum[:])
	var current []byte
	err := tx.QueryRow(ctx, `SELECT token_hash FROM outbound_integrations WHERE id = $1 FOR UPDATE`, id).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		return next, nil
	}
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(current, next) && policy.Matches(IntegrationTokenColumn, sum[:], current) {
		if _, err := tx.Exec(ctx, `SELECT set_config('faas.credential_rekey', 'on', true)`); err != nil {
			return nil, err
		}
	}
	return next, nil
}
