package state

import (
	"context"
	"errors"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/credhash"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Account flow storage (ADR 20261107103000): email changes, recovery code
// replacement, linked sign-in methods, display names and the sign-in
// history the new-device notice compares against. Kept out of Store so
// the interface does not grow; apid type-asserts AccountFlowStore.

const credTagEmailChange credhash.Tag = "account_email_changes.token_hash"

// EmailChange is a confirmed (consumed) account email change.
type EmailChange struct {
	AccountID string
	OldEmail  string
	NewEmail  string
}

// SignInRecord is one past browser session's origin.
type SignInRecord struct {
	IssuedIP string
	IssuedUA string
	IssuedAt time.Time
}

// AccountFlowStore is implemented by MemStore and PgStore.
type AccountFlowStore interface {
	// IssueEmailChange replaces any pending change for the account with one
	// to newEmail (lowercase). ErrNotFound when the account is not active.
	IssueEmailChange(ctx context.Context, tokenHash []byte, accountID, newEmail string, expiresAt time.Time) error
	// ConsumeEmailChange applies a pending change once: the account takes
	// the new address, verified. ErrNotFound for unknown, used, expired or
	// stale links; ErrConflict when another account holds the address.
	ConsumeEmailChange(ctx context.Context, tokenHash []byte) (EmailChange, error)
	// ReplaceRecoveryCodes swaps an enrolled account's recovery code
	// hashes. ErrNotFound when the account has no enrolled factor.
	ReplaceRecoveryCodes(ctx context.Context, accountID string, hashes [][]byte) error
	ListOAuthLinks(ctx context.Context, accountID string) ([]OAuthLink, error)
	// DeleteOAuthLink removes the account's links for provider and records
	// the provider as unlinked.
	DeleteOAuthLink(ctx context.Context, accountID, provider string) (bool, error)
	// OAuthProviderUnlinked reports whether the account removed provider;
	// signing in with it must not re-link it by matching email.
	OAuthProviderUnlinked(ctx context.Context, accountID, provider string) (bool, error)
	// ClearOAuthUnlink forgets the removal once the signed-in owner links
	// the provider again.
	ClearOAuthUnlink(ctx context.Context, accountID, provider string) error
	AccountDisplayName(ctx context.Context, accountID string) (string, error)
	// AccountDisplayNames returns the set display names of accountIDs in
	// one read; accounts without a name are absent from the map.
	AccountDisplayNames(ctx context.Context, accountIDs []string) (map[string]string, error)
	SetAccountDisplayName(ctx context.Context, accountID, name string) error
	// RecentSignIns lists browser sessions (revoked included) since.
	RecentSignIns(ctx context.Context, accountID string, since time.Time) ([]SignInRecord, error)
}

var (
	_ AccountFlowStore = (*MemStore)(nil)
	_ AccountFlowStore = (*PgStore)(nil)
)

// --- MemStore ---------------------------------------------------------------

type memEmailChange struct {
	accountID, oldEmail, newEmail string
	expiresAt                     time.Time
	consumed                      bool
}

type memAccountFlow struct {
	emailChanges map[string]memEmailChange // stored token hash → change
	displayNames map[string]string
	unlinked     map[string]bool // account ID + "\x00" + provider
}

func (m *MemStore) IssueEmailChange(_ context.Context, tokenHash []byte, accountID, newEmail string, expiresAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[accountID]
	if !ok || a.Status != AccountActive {
		return ErrNotFound
	}
	if m.accountFlow.emailChanges == nil {
		m.accountFlow.emailChanges = map[string]memEmailChange{}
	}
	for k, c := range m.accountFlow.emailChanges {
		if c.accountID == accountID && !c.consumed {
			delete(m.accountFlow.emailChanges, k)
		}
	}
	key := string(storedCredential(credTagEmailChange, tokenHash))
	m.accountFlow.emailChanges[key] = memEmailChange{accountID: accountID, oldEmail: strings.ToLower(a.Email), newEmail: newEmail, expiresAt: expiresAt}
	return nil
}

func (m *MemStore) ConsumeEmailChange(_ context.Context, tokenHash []byte) (EmailChange, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, stored := range credentialLookups(credTagEmailChange, tokenHash) {
		c, ok := m.accountFlow.emailChanges[string(stored)]
		if !ok {
			continue
		}
		a, ok := m.accounts[c.accountID]
		if c.consumed || !time.Now().Before(c.expiresAt) || !ok || a.Status != AccountActive || strings.ToLower(a.Email) != c.oldEmail {
			return EmailChange{}, ErrNotFound
		}
		for id, other := range m.accounts {
			if id != a.ID && strings.EqualFold(other.Email, c.newEmail) {
				return EmailChange{}, ErrConflict
			}
		}
		now := time.Now()
		a.Email = c.newEmail
		a.EmailVerifiedAt = &now
		m.storeRouteCheckAccountLocked(a.ID, a)
		c.consumed = true
		m.accountFlow.emailChanges[string(stored)] = c
		// Spend account-bound links mailed to the old address.
		for k, t := range m.loginTokens {
			if t.AccountID == a.ID && t.ConsumedAt == nil {
				t.ConsumedAt = &now
				m.loginTokens[k] = t
			}
		}
		for k, req := range m.mfaDisableRequests {
			if req.AccountID == a.ID && req.ConsumedAt == nil {
				req.ConsumedAt = &now
				m.mfaDisableRequests[k] = req
			}
		}
		return EmailChange{AccountID: a.ID, OldEmail: c.oldEmail, NewEmail: c.newEmail}, nil
	}
	return EmailChange{}, ErrNotFound
}

func (m *MemStore) ReplaceRecoveryCodes(_ context.Context, accountID string, hashes [][]byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[accountID]
	if !ok || !a.MFAEnrolled() {
		return ErrNotFound
	}
	a.MFARecoveryCodesHash = make([][]byte, len(hashes))
	for i, h := range hashes {
		a.MFARecoveryCodesHash[i] = slices.Clone(h)
	}
	m.storeRouteCheckAccountLocked(accountID, a)
	return nil
}

func (m *MemStore) ListOAuthLinks(_ context.Context, accountID string) ([]OAuthLink, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []OAuthLink
	for _, l := range m.oauthLinks {
		if l.AccountID == accountID {
			out = append(out, l)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].Provider < out[j].Provider
	})
	return out, nil
}

func (m *MemStore) DeleteOAuthLink(_ context.Context, accountID, provider string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	removed := false
	for k, l := range m.oauthLinks {
		if l.AccountID == accountID && l.Provider == provider {
			delete(m.oauthLinks, k)
			removed = true
		}
	}
	if removed {
		if m.accountFlow.unlinked == nil {
			m.accountFlow.unlinked = map[string]bool{}
		}
		m.accountFlow.unlinked[accountID+"\x00"+provider] = true
	}
	return removed, nil
}

func (m *MemStore) OAuthProviderUnlinked(_ context.Context, accountID, provider string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.accountFlow.unlinked[accountID+"\x00"+provider], nil
}

func (m *MemStore) ClearOAuthUnlink(_ context.Context, accountID, provider string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.accountFlow.unlinked, accountID+"\x00"+provider)
	return nil
}

func (m *MemStore) AccountDisplayName(_ context.Context, accountID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.accounts[accountID]; !ok {
		return "", ErrNotFound
	}
	return m.accountFlow.displayNames[accountID], nil
}

func (m *MemStore) AccountDisplayNames(_ context.Context, accountIDs []string) (map[string]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]string, len(accountIDs))
	for _, id := range accountIDs {
		if name := m.accountFlow.displayNames[id]; name != "" {
			out[id] = name
		}
	}
	return out, nil
}

func (m *MemStore) SetAccountDisplayName(_ context.Context, accountID, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.accounts[accountID]; !ok {
		return ErrNotFound
	}
	if m.accountFlow.displayNames == nil {
		m.accountFlow.displayNames = map[string]string{}
	}
	m.accountFlow.displayNames[accountID] = name
	return nil
}

func (m *MemStore) RecentSignIns(_ context.Context, accountID string, since time.Time) ([]SignInRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []SignInRecord
	for _, s := range m.sessions {
		if s.AccountID == accountID && !s.IssuedAt.Before(since) {
			out = append(out, SignInRecord{IssuedIP: s.IssuedIP, IssuedUA: s.IssuedUA, IssuedAt: s.IssuedAt})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].IssuedAt.After(out[j].IssuedAt) })
	if len(out) > 200 {
		out = out[:200]
	}
	return out, nil
}

// --- PgStore ----------------------------------------------------------------

func (s *PgStore) IssueEmailChange(ctx context.Context, tokenHash []byte, accountID, newEmail string, expiresAt time.Time) error {
	account, err := parsePgUUID(accountID)
	if err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := sqlc.New()
		if err := q.ClearPendingAccountEmailChanges(ctx, tx, account); err != nil {
			return mapErr(err)
		}
		n, err := q.InsertAccountEmailChange(ctx, tx, sqlc.InsertAccountEmailChangeParams{
			TokenHash: storedCredential(credTagEmailChange, tokenHash),
			NewEmail:  newEmail,
			ExpiresAt: pgtype.Timestamptz{Time: expiresAt, Valid: true},
			AccountID: account,
		})
		if err != nil {
			return mapErr(err)
		}
		if n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

func (s *PgStore) ConsumeEmailChange(ctx context.Context, tokenHash []byte) (EmailChange, error) {
	return firstCredential(ctx, credentialLookups(credTagEmailChange, tokenHash), func(ctx context.Context, value []byte) (EmailChange, error) {
		row, err := sqlc.New().ConsumeAccountEmailChange(ctx, s.pool, value)
		if err != nil {
			return EmailChange{}, mapErr(err)
		}
		return EmailChange{AccountID: uuidString(row.ID), OldEmail: row.OldEmail, NewEmail: row.NewEmail}, nil
	})
}

func (s *PgStore) ReplaceRecoveryCodes(ctx context.Context, accountID string, hashes [][]byte) error {
	account, err := parsePgUUID(accountID)
	if err != nil {
		return err
	}
	n, err := sqlc.New().ReplaceAccountRecoveryCodes(ctx, s.pool, sqlc.ReplaceAccountRecoveryCodesParams{Hashes: hashes, AccountID: account})
	if err != nil {
		return mapErr(err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PgStore) ListOAuthLinks(ctx context.Context, accountID string) ([]OAuthLink, error) {
	account, err := parsePgUUID(accountID)
	if err != nil {
		return nil, err
	}
	rows, err := sqlc.New().ListAccountOAuthLinks(ctx, s.pool, account)
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]OAuthLink, 0, len(rows))
	for _, r := range rows {
		out = append(out, OAuthLink{Provider: r.Provider, ProviderSubject: r.ProviderSubject, AccountID: accountID,
			Email: r.Email, EmailVerified: r.EmailVerified, CreatedAt: r.CreatedAt.Time})
	}
	return out, nil
}

func (s *PgStore) DeleteOAuthLink(ctx context.Context, accountID, provider string) (bool, error) {
	account, err := parsePgUUID(accountID)
	if err != nil {
		return false, err
	}
	n, err := sqlc.New().DeleteAccountOAuthLink(ctx, s.pool, sqlc.DeleteAccountOAuthLinkParams{AccountID: account, Provider: provider})
	if err != nil {
		return false, mapErr(err)
	}
	return n > 0, nil
}

func (s *PgStore) OAuthProviderUnlinked(ctx context.Context, accountID, provider string) (bool, error) {
	account, err := parsePgUUID(accountID)
	if err != nil {
		return false, err
	}
	unlinked, err := sqlc.New().AccountOAuthProviderUnlinked(ctx, s.pool, sqlc.AccountOAuthProviderUnlinkedParams{AccountID: account, Provider: provider})
	return unlinked, mapErr(err)
}

func (s *PgStore) ClearOAuthUnlink(ctx context.Context, accountID, provider string) error {
	account, err := parsePgUUID(accountID)
	if err != nil {
		return err
	}
	return mapErr(sqlc.New().ClearAccountOAuthUnlink(ctx, s.pool, sqlc.ClearAccountOAuthUnlinkParams{AccountID: account, Provider: provider}))
}

func (s *PgStore) AccountDisplayName(ctx context.Context, accountID string) (string, error) {
	account, err := parsePgUUID(accountID)
	if err != nil {
		return "", err
	}
	name, err := sqlc.New().AccountDisplayName(ctx, s.pool, account)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return name, mapErr(err)
}

func (s *PgStore) AccountDisplayNames(ctx context.Context, accountIDs []string) (map[string]string, error) {
	ids := make([]pgtype.UUID, 0, len(accountIDs))
	for _, id := range accountIDs {
		u, err := parsePgUUID(id)
		if err != nil {
			continue // not an account id; it has no name
		}
		ids = append(ids, u)
	}
	out := make(map[string]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := sqlc.New().AccountDisplayNames(ctx, s.pool, ids)
	if err != nil {
		return nil, mapErr(err)
	}
	for _, row := range rows {
		out[operationUUIDString(row.ID)] = row.DisplayName
	}
	return out, nil
}

func (s *PgStore) SetAccountDisplayName(ctx context.Context, accountID, name string) error {
	account, err := parsePgUUID(accountID)
	if err != nil {
		return err
	}
	n, err := sqlc.New().SetAccountDisplayName(ctx, s.pool, sqlc.SetAccountDisplayNameParams{DisplayName: name, AccountID: account})
	if err != nil {
		return mapErr(err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PgStore) RecentSignIns(ctx context.Context, accountID string, since time.Time) ([]SignInRecord, error) {
	account, err := parsePgUUID(accountID)
	if err != nil {
		return nil, err
	}
	rows, err := sqlc.New().RecentAccountSessions(ctx, s.pool, sqlc.RecentAccountSessionsParams{AccountID: account, Since: pgtype.Timestamptz{Time: since, Valid: true}})
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]SignInRecord, 0, len(rows))
	for _, r := range rows {
		out = append(out, SignInRecord{IssuedIP: r.IssuedIp, IssuedUA: r.IssuedUa, IssuedAt: r.IssuedAt.Time})
	}
	return out, nil
}
