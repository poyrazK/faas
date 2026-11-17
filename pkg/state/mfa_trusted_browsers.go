package state

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/credhash"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Remembered browsers for two-factor authentication (ADR 20261108150000).
// A browser that passed the authenticator check and asked to be remembered
// holds a random token; only its keyed digest is stored. Kept out of Store;
// apid type-asserts MFATrustedBrowserStore.

const credTagMFATrustedBrowser credhash.Tag = "mfa_trusted_browsers.token_hash"

// MFATrustedBrowser is one live remembered browser.
type MFATrustedBrowser struct {
	ID         string
	UAFamily   string
	CreatedIP  string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	LastUsedAt *time.Time
}

// MFATrustedBrowserStore is implemented by MemStore and PgStore.
type MFATrustedBrowserStore interface {
	// IssueMFATrustedBrowser remembers a browser for an enrolled account and
	// keeps only the newest maxActive. ErrNotFound when the account has no
	// enrolled factor.
	IssueMFATrustedBrowser(ctx context.Context, tokenHash []byte, accountID, uaFamily, createdIP string, expiresAt time.Time, maxActive int) error
	// UseMFATrustedBrowser reports whether the token is a live remembered
	// browser of the account with the same user-agent family, and stamps it.
	UseMFATrustedBrowser(ctx context.Context, tokenHash []byte, accountID, uaFamily string) (bool, error)
	// ListMFATrustedBrowsers lists live remembered browsers, newest first.
	ListMFATrustedBrowsers(ctx context.Context, accountID string) ([]MFATrustedBrowser, error)
	// RevokeMFATrustedBrowser forgets one browser. ErrNotFound when it is
	// not a live browser of the account.
	RevokeMFATrustedBrowser(ctx context.Context, accountID, id string) error
	// RevokeAllMFATrustedBrowsers forgets every browser of the account and
	// returns how many it forgot.
	RevokeAllMFATrustedBrowsers(ctx context.Context, accountID string) (int, error)
}

var (
	_ MFATrustedBrowserStore = (*MemStore)(nil)
	_ MFATrustedBrowserStore = (*PgStore)(nil)
)

// --- MemStore ---------------------------------------------------------------

type memMFATrustedBrowser struct {
	MFATrustedBrowser
	accountID string
	revoked   bool
}

func (m *MemStore) IssueMFATrustedBrowser(_ context.Context, tokenHash []byte, accountID, uaFamily, createdIP string, expiresAt time.Time, maxActive int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[accountID]
	if !ok || !a.MFAEnrolled() {
		return ErrNotFound
	}
	if m.accountFlow.trustedBrowsers == nil {
		m.accountFlow.trustedBrowsers = map[string]*memMFATrustedBrowser{}
	}
	key := string(storedCredential(credTagMFATrustedBrowser, tokenHash))
	if _, dup := m.accountFlow.trustedBrowsers[key]; dup {
		return ErrConflict
	}
	m.accountFlow.trustedBrowsers[key] = &memMFATrustedBrowser{
		MFATrustedBrowser: MFATrustedBrowser{ID: uuid.NewString(), UAFamily: uaFamily, CreatedIP: createdIP, CreatedAt: time.Now(), ExpiresAt: expiresAt},
		accountID:         accountID,
	}
	live := m.liveTrustedBrowsersLocked(accountID, false)
	for _, b := range live[min(maxActive, len(live)):] {
		b.revoked = true
	}
	return nil
}

// liveTrustedBrowsersLocked lists the account's unrevoked browsers, newest
// first; onlyUnexpired also drops expired ones.
func (m *MemStore) liveTrustedBrowsersLocked(accountID string, onlyUnexpired bool) []*memMFATrustedBrowser {
	now := time.Now()
	var out []*memMFATrustedBrowser
	for _, b := range m.accountFlow.trustedBrowsers {
		if b.accountID == accountID && !b.revoked && (!onlyUnexpired || now.Before(b.ExpiresAt)) {
			out = append(out, b)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

func (m *MemStore) UseMFATrustedBrowser(_ context.Context, tokenHash []byte, accountID, uaFamily string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	a, ok := m.accounts[accountID]
	if !ok || a.MFAEnrolledAt == nil {
		return false, nil
	}
	for _, stored := range credentialLookups(credTagMFATrustedBrowser, tokenHash) {
		b, ok := m.accountFlow.trustedBrowsers[string(stored)]
		if ok && b.accountID == accountID && b.UAFamily == uaFamily && !b.revoked && now.Before(b.ExpiresAt) &&
			!b.CreatedAt.Before(*a.MFAEnrolledAt) {
			b.LastUsedAt = &now
			return true, nil
		}
	}
	return false, nil
}

func (m *MemStore) ListMFATrustedBrowsers(_ context.Context, accountID string) ([]MFATrustedBrowser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	live := m.liveTrustedBrowsersLocked(accountID, true)
	out := make([]MFATrustedBrowser, 0, len(live))
	for _, b := range live {
		c := b.MFATrustedBrowser
		if c.LastUsedAt != nil {
			t := *c.LastUsedAt
			c.LastUsedAt = &t
		}
		out = append(out, c)
	}
	return out, nil
}

func (m *MemStore) RevokeMFATrustedBrowser(_ context.Context, accountID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, b := range m.accountFlow.trustedBrowsers {
		if b.ID == id && b.accountID == accountID && !b.revoked {
			b.revoked = true
			return nil
		}
	}
	return ErrNotFound
}

func (m *MemStore) RevokeAllMFATrustedBrowsers(_ context.Context, accountID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, b := range m.liveTrustedBrowsersLocked(accountID, false) {
		b.revoked = true
		n++
	}
	return n, nil
}

// --- PgStore ----------------------------------------------------------------

func (s *PgStore) IssueMFATrustedBrowser(ctx context.Context, tokenHash []byte, accountID, uaFamily, createdIP string, expiresAt time.Time, maxActive int) error {
	account, err := parsePgUUID(accountID)
	if err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := sqlc.New()
		n, err := q.InsertMFATrustedBrowser(ctx, tx, sqlc.InsertMFATrustedBrowserParams{
			TokenHash: storedCredential(credTagMFATrustedBrowser, tokenHash),
			UaFamily:  uaFamily,
			CreatedIp: createdIP,
			ExpiresAt: pgtype.Timestamptz{Time: expiresAt, Valid: true},
			AccountID: account,
		})
		if err != nil {
			return mapErr(err)
		}
		if n == 0 {
			return ErrNotFound
		}
		_, err = q.TrimMFATrustedBrowsers(ctx, tx, sqlc.TrimMFATrustedBrowsersParams{AccountID: account, MaxActive: int32(max(0, maxActive))}) //nolint:gosec // small positive limit
		return mapErr(err)
	})
}

func (s *PgStore) UseMFATrustedBrowser(ctx context.Context, tokenHash []byte, accountID, uaFamily string) (bool, error) {
	account, err := parsePgUUID(accountID)
	if err != nil {
		return false, nil //nolint:nilerr // not an account id: no remembered browser
	}
	_, err = firstCredential(ctx, credentialLookups(credTagMFATrustedBrowser, tokenHash), func(ctx context.Context, value []byte) (struct{}, error) {
		n, err := sqlc.New().UseMFATrustedBrowser(ctx, s.pool, sqlc.UseMFATrustedBrowserParams{TokenHash: value, AccountID: account, UaFamily: uaFamily})
		if err != nil {
			return struct{}{}, mapErr(err)
		}
		if n == 0 {
			return struct{}{}, ErrNotFound
		}
		return struct{}{}, nil
	})
	if err == ErrNotFound { //nolint:errorlint // sentinel returned as is
		return false, nil
	}
	return err == nil, err
}

func (s *PgStore) ListMFATrustedBrowsers(ctx context.Context, accountID string) ([]MFATrustedBrowser, error) {
	account, err := parsePgUUID(accountID)
	if err != nil {
		return nil, err
	}
	rows, err := sqlc.New().ListMFATrustedBrowsers(ctx, s.pool, account)
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]MFATrustedBrowser, 0, len(rows))
	for _, r := range rows {
		b := MFATrustedBrowser{ID: uuidString(r.ID), UAFamily: r.UaFamily, CreatedIP: r.CreatedIp, CreatedAt: r.CreatedAt.Time, ExpiresAt: r.ExpiresAt.Time}
		if r.LastUsedAt.Valid {
			t := r.LastUsedAt.Time
			b.LastUsedAt = &t
		}
		out = append(out, b)
	}
	return out, nil
}

func (s *PgStore) RevokeMFATrustedBrowser(ctx context.Context, accountID, id string) error {
	account, err := parsePgUUID(accountID)
	if err != nil {
		return err
	}
	browser, err := parsePgUUID(id)
	if err != nil {
		return ErrNotFound
	}
	n, err := sqlc.New().RevokeMFATrustedBrowser(ctx, s.pool, sqlc.RevokeMFATrustedBrowserParams{ID: browser, AccountID: account})
	if err != nil {
		return mapErr(err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PgStore) RevokeAllMFATrustedBrowsers(ctx context.Context, accountID string) (int, error) {
	account, err := parsePgUUID(accountID)
	if err != nil {
		return 0, err
	}
	n, err := sqlc.New().RevokeAllMFATrustedBrowsers(ctx, s.pool, account)
	return int(n), mapErr(err)
}
