package state

import (
	"context"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// AccountEmailRevertStore undoes a confirmed email change for the operator
// runbook docs/runbooks/account-recovery-email-changed.md
// (ADR 20261110100000). Implemented by MemStore and PgStore.
type AccountEmailRevertStore interface {
	// RevertLastEmailChange moves the account back from the address its
	// most recent confirmed change (within the last `within`) moved it to,
	// marks the restored address verified and spends account-bound links
	// mailed meanwhile. It returns that change: the account went from
	// NewEmail back to OldEmail. ErrNotFound when there is no such change or
	// the account no longer has its NewEmail; ErrConflict when OldEmail now
	// belongs to another account.
	RevertLastEmailChange(ctx context.Context, accountID string, within time.Duration) (EmailChange, error)
}

var (
	_ AccountEmailRevertStore = (*MemStore)(nil)
	_ AccountEmailRevertStore = (*PgStore)(nil)
)

func (m *MemStore) RevertLastEmailChange(_ context.Context, accountID string, within time.Duration) (EmailChange, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	var last *memEmailChange
	for _, c := range m.accountFlow.emailChanges {
		if c.accountID != accountID || !c.consumed || now.Sub(c.consumedAt) >= within {
			continue
		}
		if last == nil || c.consumedAt.After(last.consumedAt) {
			last = &c
		}
	}
	a, ok := m.accounts[accountID]
	if last == nil || !ok || strings.ToLower(a.Email) != last.newEmail {
		return EmailChange{}, ErrNotFound
	}
	for id, other := range m.accounts {
		if id != accountID && strings.EqualFold(other.Email, last.oldEmail) {
			return EmailChange{}, ErrConflict
		}
	}
	a.Email = last.oldEmail
	a.EmailVerifiedAt = &now
	m.storeRouteCheckAccountLocked(a.ID, a)
	for k, t := range m.loginTokens {
		if t.AccountID == accountID && t.ConsumedAt == nil {
			t.ConsumedAt = &now
			m.loginTokens[k] = t
		}
	}
	for k, req := range m.mfaDisableRequests {
		if req.AccountID == accountID && req.ConsumedAt == nil {
			req.ConsumedAt = &now
			m.mfaDisableRequests[k] = req
		}
	}
	return EmailChange{AccountID: accountID, OldEmail: last.oldEmail, NewEmail: last.newEmail}, nil
}

func (s *PgStore) RevertLastEmailChange(ctx context.Context, accountID string, within time.Duration) (EmailChange, error) {
	account, err := parsePgUUID(accountID)
	if err != nil {
		return EmailChange{}, ErrNotFound
	}
	row, err := sqlc.New().RevertLastAccountEmailChange(ctx, s.pool, sqlc.RevertLastAccountEmailChangeParams{
		AccountID: account, WithinSecs: within.Seconds(),
	})
	if err != nil {
		return EmailChange{}, mapErr(err)
	}
	return EmailChange{AccountID: uuidString(row.ID), OldEmail: row.OldEmail, NewEmail: row.NewEmail}, nil
}
