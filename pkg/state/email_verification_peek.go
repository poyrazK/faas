package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// EmailVerificationPeekStore reports which account a verification link
// belongs to without consuming it (ADR 20261109120000). apid consumes the
// link only from a browser signed in to that account: the link proves the
// mailbox, the session proves the password holder is the mailbox owner.
type EmailVerificationPeekStore interface {
	// EmailVerificationTokenAccount returns the account of an unused,
	// unexpired link, or ErrNotFound.
	EmailVerificationTokenAccount(ctx context.Context, tokenHash []byte) (string, error)
}

var (
	_ EmailVerificationPeekStore = (*MemStore)(nil)
	_ EmailVerificationPeekStore = (*PgStore)(nil)
)

func (m *MemStore) EmailVerificationTokenAccount(_ context.Context, tokenHash []byte) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key, _ := rawCredentialKey(m.emailVerificationTokens, credTagEmailVerification, tokenHash)
	tok, ok := m.emailVerificationTokens[key]
	if !ok || tok.ConsumedAt != nil || !tok.ExpiresAt.After(time.Now()) {
		return "", ErrNotFound
	}
	return tok.AccountID, nil
}

func (s *PgStore) EmailVerificationTokenAccount(ctx context.Context, tokenHash []byte) (string, error) {
	return firstCredential(ctx, credentialLookups(credTagEmailVerification, tokenHash), func(ctx context.Context, value []byte) (string, error) {
		account, err := sqlc.New().EmailVerificationTokenAccount(ctx, s.pool, value)
		if err != nil {
			return "", mapErr(err)
		}
		return uuidString(account), nil
	})
}
