package state

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"time"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Claimable deploys (ADR-20261124080000). An agent deploys with no account:
// apid creates a locked-down Free account and records it in
// unclaimed_accounts. A human claims it by proving an email address through
// the claim link, which converts the account in place; unclaimed accounts
// past their expiry are scheduled for deletion.

// UnclaimedAccount is one unclaimed_accounts row.
type UnclaimedAccount struct {
	AccountID        string
	ClaimTokenSHA256 []byte
	CreatedAt        time.Time
	ExpiresAt        time.Time
	ClaimedAt        *time.Time
	ExpiredAt        *time.Time
	CreatedIP        string
	UserAgent        string
}

// Open reports whether the account is still waiting to be claimed: neither
// claimed nor expired by the sweep. An open row past ExpiresAt is waiting
// for the sweep and must already be treated as expired by callers.
func (u UnclaimedAccount) Open() bool { return u.ClaimedAt == nil && u.ExpiredAt == nil }

// Live reports whether the account is open and before its expiry at now.
func (u UnclaimedAccount) Live(now time.Time) bool { return u.Open() && now.Before(u.ExpiresAt) }

// UnclaimedAccountParams is what CreateAccountWithPersonalOrg records with an
// anonymous deploy's account.
type UnclaimedAccountParams struct {
	ClaimTokenSHA256 []byte
	ExpiresAt        time.Time
	CreatedIP        string
	UserAgent        string
}

// UnclaimedAccountStore reads and moves unclaimed accounts. Both stores
// implement it; apid asserts it like the other optional store interfaces.
type UnclaimedAccountStore interface {
	// UnclaimedAccountByID returns the row for an account, or ErrNotFound
	// when the account was never unclaimed.
	UnclaimedAccountByID(ctx context.Context, accountID string) (UnclaimedAccount, error)
	// UnclaimedAccountByClaimToken returns the row whose claim token hashes
	// to hash, or ErrNotFound.
	UnclaimedAccountByClaimToken(ctx context.Context, hash []byte) (UnclaimedAccount, error)
	// RotateUnclaimedClaimToken replaces an open account's claim token, so
	// the previous link stops working. ErrNotFound when the account is not
	// open.
	RotateUnclaimedClaimToken(ctx context.Context, accountID string, hash []byte) error
	// MarkUnclaimedAccountClaimed records the claim. It reports false when
	// the account was not open (already claimed or expired).
	MarkUnclaimedAccountClaimed(ctx context.Context, accountID string, at time.Time) (bool, error)
	// MarkUnclaimedAccountExpired records the sweep's decision. It reports
	// false when the account was not open.
	MarkUnclaimedAccountExpired(ctx context.Context, accountID string, at time.Time) (bool, error)
	// ListExpiredUnclaimedAccounts returns up to limit open accounts whose
	// expiry is at or before now, oldest expiry first.
	ListExpiredUnclaimedAccounts(ctx context.Context, now time.Time, limit int) ([]string, error)
	// CountLiveUnclaimedAccounts counts open accounts before their expiry.
	CountLiveUnclaimedAccounts(ctx context.Context, now time.Time) (int, error)
}

var (
	_ UnclaimedAccountStore = (*MemStore)(nil)
	_ UnclaimedAccountStore = (*PgStore)(nil)
)

func (p UnclaimedAccountParams) validate() error {
	if len(p.ClaimTokenSHA256) != 32 {
		return errors.New("state: unclaimed account: claim token hash must be 32 bytes")
	}
	if p.ExpiresAt.IsZero() {
		return errors.New("state: unclaimed account: expiry is required")
	}
	return nil
}

func truncateUserAgent(ua string) string {
	if len(ua) <= 512 {
		return ua
	}
	return ua[:512]
}

// ---- MemStore ----

func (m *MemStore) insertUnclaimedLocked(accountID string, p UnclaimedAccountParams, now time.Time) error {
	if err := p.validate(); err != nil {
		return err
	}
	if !p.ExpiresAt.After(now) {
		return errors.New("state: unclaimed account: expiry must be after creation")
	}
	if m.unclaimedAccounts == nil {
		m.unclaimedAccounts = map[string]UnclaimedAccount{}
	}
	for _, u := range m.unclaimedAccounts {
		if bytes.Equal(u.ClaimTokenSHA256, p.ClaimTokenSHA256) {
			return ErrConflict
		}
	}
	m.unclaimedAccounts[accountID] = UnclaimedAccount{
		AccountID:        accountID,
		ClaimTokenSHA256: append([]byte(nil), p.ClaimTokenSHA256...),
		CreatedAt:        now,
		ExpiresAt:        p.ExpiresAt.UTC(),
		CreatedIP:        p.CreatedIP,
		UserAgent:        truncateUserAgent(p.UserAgent),
	}
	return nil
}

// unclaimedRowLocked returns an account's row and drops rows whose account was deleted, mirroring the
// table's ON DELETE CASCADE.
func (m *MemStore) unclaimedRowLocked(accountID string) (UnclaimedAccount, bool) {
	u, ok := m.unclaimedAccounts[accountID]
	if !ok {
		return UnclaimedAccount{}, false
	}
	if _, exists := m.accounts[accountID]; !exists {
		delete(m.unclaimedAccounts, accountID)
		return UnclaimedAccount{}, false
	}
	return u, true
}

func (m *MemStore) UnclaimedAccountByID(_ context.Context, accountID string) (UnclaimedAccount, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.unclaimedRowLocked(accountID)
	if !ok {
		return UnclaimedAccount{}, ErrNotFound
	}
	return u, nil
}

func (m *MemStore) UnclaimedAccountByClaimToken(_ context.Context, hash []byte) (UnclaimedAccount, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id := range m.unclaimedAccounts {
		if u, ok := m.unclaimedRowLocked(id); ok && bytes.Equal(u.ClaimTokenSHA256, hash) {
			return u, nil
		}
	}
	return UnclaimedAccount{}, ErrNotFound
}

func (m *MemStore) RotateUnclaimedClaimToken(_ context.Context, accountID string, hash []byte) error {
	if len(hash) != 32 {
		return errors.New("state: unclaimed account: claim token hash must be 32 bytes")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.unclaimedRowLocked(accountID)
	if !ok || !u.Open() {
		return ErrNotFound
	}
	for id, other := range m.unclaimedAccounts {
		if id != accountID && bytes.Equal(other.ClaimTokenSHA256, hash) {
			return ErrConflict
		}
	}
	u.ClaimTokenSHA256 = append([]byte(nil), hash...)
	m.unclaimedAccounts[accountID] = u
	return nil
}

func (m *MemStore) MarkUnclaimedAccountClaimed(_ context.Context, accountID string, at time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.unclaimedRowLocked(accountID)
	if !ok || !u.Open() {
		return false, nil
	}
	t := at.UTC()
	u.ClaimedAt = &t
	m.unclaimedAccounts[accountID] = u
	return true, nil
}

func (m *MemStore) MarkUnclaimedAccountExpired(_ context.Context, accountID string, at time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.unclaimedRowLocked(accountID)
	if !ok || !u.Open() {
		return false, nil
	}
	t := at.UTC()
	u.ExpiredAt = &t
	m.unclaimedAccounts[accountID] = u
	return true, nil
}

func (m *MemStore) ListExpiredUnclaimedAccounts(_ context.Context, now time.Time, limit int) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var due []UnclaimedAccount
	for id := range m.unclaimedAccounts {
		if u, ok := m.unclaimedRowLocked(id); ok && u.Open() && !u.ExpiresAt.After(now) {
			due = append(due, u)
		}
	}
	sort.Slice(due, func(i, j int) bool { return due[i].ExpiresAt.Before(due[j].ExpiresAt) })
	ids := make([]string, 0, min(limit, len(due)))
	for i := 0; i < len(due) && i < limit; i++ {
		ids = append(ids, due[i].AccountID)
	}
	return ids, nil
}

func (m *MemStore) CountLiveUnclaimedAccounts(_ context.Context, now time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for id := range m.unclaimedAccounts {
		if u, ok := m.unclaimedRowLocked(id); ok && u.Live(now) {
			n++
		}
	}
	return n, nil
}

// ---- PgStore ----

func insertUnclaimedAccountTx(ctx context.Context, tx pgx.Tx, accountID string, p UnclaimedAccountParams) error {
	if err := p.validate(); err != nil {
		return err
	}
	var ip *netip.Addr
	if addr, err := netip.ParseAddr(p.CreatedIP); err == nil {
		ip = &addr
	}
	if _, err := tx.Exec(ctx, `
		insert into unclaimed_accounts (account_id, claim_token_sha256, expires_at, created_ip, user_agent)
		values ($1, $2, $3, $4, $5)
	`, accountID, p.ClaimTokenSHA256, p.ExpiresAt.UTC(), ip, truncateUserAgent(p.UserAgent)); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgerrcode.UniqueViolation {
			return ErrConflict
		}
		return fmt.Errorf("state: record unclaimed account: %w", mapErr(err))
	}
	return nil
}

const unclaimedAccountCols = `account_id, claim_token_sha256, created_at, expires_at, claimed_at, expired_at,
	coalesce(host(created_ip), ''), user_agent`

func scanUnclaimedAccount(row pgx.Row) (UnclaimedAccount, error) {
	var u UnclaimedAccount
	if err := row.Scan(&u.AccountID, &u.ClaimTokenSHA256, &u.CreatedAt, &u.ExpiresAt, &u.ClaimedAt, &u.ExpiredAt, &u.CreatedIP, &u.UserAgent); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return UnclaimedAccount{}, ErrNotFound
		}
		return UnclaimedAccount{}, fmt.Errorf("state: scan unclaimed account: %w", err)
	}
	return u, nil
}

func (s *PgStore) UnclaimedAccountByID(ctx context.Context, accountID string) (UnclaimedAccount, error) {
	return scanUnclaimedAccount(s.pool.QueryRow(ctx,
		`select `+unclaimedAccountCols+` from unclaimed_accounts where account_id = $1`, accountID))
}

func (s *PgStore) UnclaimedAccountByClaimToken(ctx context.Context, hash []byte) (UnclaimedAccount, error) {
	return scanUnclaimedAccount(s.pool.QueryRow(ctx,
		`select `+unclaimedAccountCols+` from unclaimed_accounts where claim_token_sha256 = $1`, hash))
}

func (s *PgStore) RotateUnclaimedClaimToken(ctx context.Context, accountID string, hash []byte) error {
	if len(hash) != 32 {
		return errors.New("state: unclaimed account: claim token hash must be 32 bytes")
	}
	tag, err := s.pool.Exec(ctx, `
		update unclaimed_accounts set claim_token_sha256 = $2
		 where account_id = $1 and claimed_at is null and expired_at is null
	`, accountID, hash)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgerrcode.UniqueViolation {
			return ErrConflict
		}
		return fmt.Errorf("state: rotate unclaimed claim token: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PgStore) MarkUnclaimedAccountClaimed(ctx context.Context, accountID string, at time.Time) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		update unclaimed_accounts set claimed_at = $2
		 where account_id = $1 and claimed_at is null and expired_at is null
	`, accountID, at.UTC())
	if err != nil {
		return false, fmt.Errorf("state: mark unclaimed account claimed: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (s *PgStore) MarkUnclaimedAccountExpired(ctx context.Context, accountID string, at time.Time) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		update unclaimed_accounts set expired_at = $2
		 where account_id = $1 and claimed_at is null and expired_at is null
	`, accountID, at.UTC())
	if err != nil {
		return false, fmt.Errorf("state: mark unclaimed account expired: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (s *PgStore) ListExpiredUnclaimedAccounts(ctx context.Context, now time.Time, limit int) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
		select account_id::text from unclaimed_accounts
		 where claimed_at is null and expired_at is null and expires_at <= $1
		 order by expires_at
		 limit $2
	`, now.UTC(), min(limit, 10_000))
	if err != nil {
		return nil, fmt.Errorf("state: list expired unclaimed accounts: %w", err)
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

func (s *PgStore) CountLiveUnclaimedAccounts(ctx context.Context, now time.Time) (int, error) {
	var n int
	if err := s.pool.QueryRow(ctx, `
		select count(*) from unclaimed_accounts
		 where claimed_at is null and expired_at is null and expires_at > $1
	`, now.UTC()).Scan(&n); err != nil {
		return 0, fmt.Errorf("state: count live unclaimed accounts: %w", err)
	}
	return n, nil
}
