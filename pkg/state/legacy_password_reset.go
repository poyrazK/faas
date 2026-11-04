package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// LegacyPasswordResetStore commits a legacy link's use and the password change
// together. A failed or expired attempt leaves both credentials unchanged.
type LegacyPasswordResetStore interface {
	ResetLegacyPassword(context.Context, []byte, string) (string, error)
}

var _ LegacyPasswordResetStore = (*MemStore)(nil)
var _ LegacyPasswordResetStore = (*PgStore)(nil)

func (s *PgStore) ResetLegacyPassword(ctx context.Context, hash []byte, phc string) (string, error) {
	if phc == "" {
		return "", ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer rollbackOrgAccess(ctx, tx)
	q := sqlc.New()
	account, storedHash, err := firstCredentialMatch(ctx, credentialLookups(credTagLoginToken, hash), func(ctx context.Context, value []byte) (string, error) {
		id, err := q.FindInlineResetAccount(ctx, tx, value)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return pgUUIDString(id), err
	})
	if err != nil {
		return "", err
	}
	id := mustPgUUID(account)
	if _, err = q.LockInlineResetAccount(ctx, tx, id); err != nil {
		return "", mapErr(err)
	}
	token, err := q.LockInlineResetToken(ctx, tx, sqlc.LockInlineResetTokenParams{TokenHash: storedHash, AccountID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if token.ConsumedAt.Valid {
		return "", ErrNotFound
	}
	n, err := q.SetAccountPassword(ctx, tx, sqlc.SetAccountPasswordParams{ID: id, Hash: phc})
	if err != nil {
		return "", fmt.Errorf("state: reset legacy password: %w", mapErr(err))
	}
	if n != 1 {
		return "", ErrNotFound
	}
	if _, err = q.ConsumeInlineResetToken(ctx, tx, sqlc.ConsumeInlineResetTokenParams{TokenHash: storedHash, AccountID: id}); errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	} else if err != nil {
		return "", err
	}
	if err = tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("state: commit legacy password reset: %w", err)
	}
	return account, nil
}

func (m *MemStore) ResetLegacyPassword(ctx context.Context, hash []byte, phc string) (string, error) {
	if phc == "" {
		return "", ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	key, _ := rawCredentialKey(m.loginTokens, credTagLoginToken, hash)
	token, ok := m.loginTokens[key]
	if !ok || token.ConsumedAt != nil || !token.ExpiresAt.After(time.Now()) {
		return "", ErrNotFound
	}
	if _, exists := m.accounts[token.AccountID]; !exists {
		return "", ErrNotFound
	}
	if err := m.setAccountPasswordLocked(token.AccountID, phc); err != nil {
		return "", err
	}
	now := time.Now()
	token.ConsumedAt = &now
	m.loginTokens[key] = token
	return token.AccountID, nil
}
