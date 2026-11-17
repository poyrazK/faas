package state

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Ending an account's pending grants (ADR 20261109120000). A recovery, such
// as a password reset, signing out everywhere or an ownership claim, ends
// the account's sessions and sign-in keys. Grants the account started before
// then stayed redeemable: an email change link (moved the account to whoever
// held it), an org invitation (added whoever held its token), and a CLI or
// agent code approved but not yet exchanged (minted a key).

// PendingGrantsEnded counts what EndAccountPendingGrants ended.
type PendingGrantsEnded struct {
	EmailChanges, Invitations, CliCodes int64
}

// Any reports whether anything was ended.
func (p PendingGrantsEnded) Any() bool {
	return p.EmailChanges+p.Invitations+p.CliCodes > 0
}

// AccountPendingGrantsStore is implemented by MemStore and PgStore.
type AccountPendingGrantsStore interface {
	// EndAccountPendingGrants deletes the account's unconfirmed email
	// changes, revokes the pending org invitations it created and expires
	// its approved, unexchanged CLI and agent codes, in one transaction.
	EndAccountPendingGrants(ctx context.Context, accountID string) (PendingGrantsEnded, error)
}

var (
	_ AccountPendingGrantsStore = (*MemStore)(nil)
	_ AccountPendingGrantsStore = (*PgStore)(nil)
)

func (m *MemStore) EndAccountPendingGrants(_ context.Context, accountID string) (PendingGrantsEnded, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out PendingGrantsEnded
	for k, c := range m.accountFlow.emailChanges {
		if c.accountID == accountID && !c.consumed {
			delete(m.accountFlow.emailChanges, k)
			out.EmailChanges++
		}
	}
	now := time.Now().UTC()
	for k, inv := range m.invitations {
		if inv.InvitedByAccountID != nil && *inv.InvitedByAccountID == accountID && inv.ConsumedAt == nil && inv.RevokedAt == nil {
			inv.RevokedAt = &now
			m.invitations[k] = inv
			out.Invitations++
		}
	}
	for k, c := range m.cliAuthCodes {
		if c.AccountID == accountID && c.ConsumedAt == nil && c.ExpiresAt.After(now) {
			c.ExpiresAt = now.Add(-time.Second)
			m.cliAuthCodes[k] = c
			out.CliCodes++
		}
	}
	return out, nil
}

func (s *PgStore) EndAccountPendingGrants(ctx context.Context, accountID string) (PendingGrantsEnded, error) {
	account, err := parsePgUUID(accountID)
	if err != nil {
		return PendingGrantsEnded{}, ErrNotFound
	}
	var out PendingGrantsEnded
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := sqlc.New()
		var err error
		if out.EmailChanges, err = q.EndAccountPendingEmailChanges(ctx, tx, account); err != nil {
			return err
		}
		if out.Invitations, err = q.EndAccountPendingInvitations(ctx, tx, account); err != nil {
			return err
		}
		out.CliCodes, err = q.EndAccountApprovedCliCodes(ctx, tx, account)
		return err
	})
	if err != nil {
		return PendingGrantsEnded{}, mapErr(err)
	}
	return out, nil
}
