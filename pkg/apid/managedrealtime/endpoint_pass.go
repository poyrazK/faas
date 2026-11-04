package managedrealtime

import (
	"context"
	"crypto/sha256"
	"errors"
	"sync"

	"github.com/onebox-faas/faas/pkg/state"
)

// endpointPass holds what one endpoint reconcile pass reads once: each
// account, however many endpoints it has (ADR-1323, amendment of
// 2026-11-03).
type endpointPass struct {
	mu       sync.Mutex
	accounts map[string]accountLookup
}

type accountLookup struct {
	account state.Account
	err     error
}

type endpointPassKey struct{}

func withEndpointPass(ctx context.Context) context.Context {
	return context.WithValue(ctx, endpointPassKey{}, &endpointPass{accounts: map[string]accountLookup{}})
}

// realtimeAccount reads an account, once per endpoint pass when ctx carries
// one.
func (s *Service) realtimeAccount(ctx context.Context, accountID string) (state.Account, error) {
	pass, _ := ctx.Value(endpointPassKey{}).(*endpointPass)
	if pass != nil {
		pass.mu.Lock()
		hit, ok := pass.accounts[accountID]
		pass.mu.Unlock()
		if ok {
			return hit.account, hit.err
		}
	}
	acct, err := s.Host.Store().AccountByID(ctx, accountID)
	if pass != nil && (err == nil || errors.Is(err, state.ErrNotFound)) {
		pass.mu.Lock()
		pass.accounts[accountID] = accountLookup{account: acct, err: err}
		pass.mu.Unlock()
	}
	return acct, err
}

// unsealedCredentials keeps endpoint credentials apid already unsealed,
// keyed by their sealed bytes, so the reconcile does not decrypt three
// credentials per endpoint every 30 seconds. A rotated credential has new
// sealed bytes and is unsealed again.
type unsealedCredentials struct {
	mu      sync.Mutex
	entries map[[sha256.Size]byte][]byte
}

const unsealedCredentialsMax = 16384

func (u *unsealedCredentials) get(sealed []byte) ([]byte, bool) {
	key := sha256.Sum256(sealed)
	u.mu.Lock()
	defer u.mu.Unlock()
	plaintext, ok := u.entries[key]
	return plaintext, ok
}

func (u *unsealedCredentials) put(sealed, plaintext []byte) {
	key := sha256.Sum256(sealed)
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.entries == nil || len(u.entries) >= unsealedCredentialsMax {
		u.entries = map[[sha256.Size]byte][]byte{}
	}
	u.entries[key] = append([]byte(nil), plaintext...)
}
