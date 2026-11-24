package state

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
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
// anonymous deploy's account. The caps are checked in the same transaction
// as the insert, serialised across writers, so concurrent requests and
// several apids cannot exceed them. A cap of 0 or less refuses every new
// account, whatever its address; above 0, the address caps count only when
// CreatedIP parses.
type UnclaimedAccountParams struct {
	ClaimTokenSHA256 []byte
	ExpiresAt        time.Time
	CreatedIP        string
	UserAgent        string

	// LiveMax bounds open accounts before their expiry, platform wide.
	LiveMax int
	// PerAddress and PerNetwork bound accounts created in the last
	// api.UnclaimedAccountWindow from CreatedIP and from its /24 (IPv6 /48).
	PerAddress int
	PerNetwork int
}

// UnclaimedLimitError refuses an anonymous account over one of the caps in
// UnclaimedAccountParams.
type UnclaimedLimitError struct {
	// Reason is UnclaimedLimitLive, UnclaimedLimitAddress or
	// UnclaimedLimitNetwork.
	Reason   string
	Limit    int
	Observed int
}

// Reasons an UnclaimedLimitError names.
const (
	UnclaimedLimitLive    = "live_cap"
	UnclaimedLimitAddress = "address"
	UnclaimedLimitNetwork = "network"
)

func (e *UnclaimedLimitError) Error() string {
	return fmt.Sprintf("state: unclaimed account %s limit reached (%d of %d)", e.Reason, e.Observed, e.Limit)
}

// UnclaimedCursor is a position in the expiry sweep's order.
type UnclaimedCursor struct {
	ExpiresAt time.Time
	AccountID string
}

func (c UnclaimedCursor) before(u UnclaimedAccount) bool {
	if !u.ExpiresAt.Equal(c.ExpiresAt) {
		return c.ExpiresAt.Before(u.ExpiresAt)
	}
	return c.AccountID < u.AccountID
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
	// RotateUnclaimedClaimToken replaces an open account's claim token and
	// drops its pending email changes, so neither the previous link nor a
	// confirmation mailed through it still works. ErrNotFound when the
	// account is not open.
	RotateUnclaimedClaimToken(ctx context.Context, accountID string, hash []byte) error
	// MarkUnclaimedAccountClaimed records the claim. It reports false when
	// the account was not open (already claimed or expired).
	MarkUnclaimedAccountClaimed(ctx context.Context, accountID string, at time.Time) (bool, error)
	// MarkUnclaimedAccountExpired records the sweep's decision and drops the
	// account's pending email changes, so no claim confirmation lands after
	// it. It reports false when the account was not open.
	MarkUnclaimedAccountExpired(ctx context.Context, accountID string, at time.Time) (bool, error)
	// EndUnclaimedWindow moves an open account's expiry back to at (never
	// later than it was, nor before just after its creation) and drops its
	// pending email changes, so the next sweep ends it. It reports false
	// when the account was not open.
	EndUnclaimedWindow(ctx context.Context, accountID string, at time.Time) (bool, error)
	// ListExpiredUnclaimedAccounts returns up to limit open accounts whose
	// expiry is at or before now, ordered by (ExpiresAt, AccountID) and
	// strictly after the cursor; the zero cursor starts at the beginning.
	// Postgres also requires the expiry to have passed by the database's
	// clock: claim confirmations expire by that clock no later than the
	// account (ConsumeEmailChange), so once a row is listed no claim can
	// land on it, whatever apid's clock says.
	ListExpiredUnclaimedAccounts(ctx context.Context, now time.Time, after UnclaimedCursor, limit int) ([]UnclaimedCursor, error)
	// ListUnrecordedClaims returns up to limit open accounts, after the
	// account ID after ("" starts at the beginning), whose address is no
	// longer the placeholder: claimed, but the claim was not recorded.
	ListUnrecordedClaims(ctx context.Context, after string, limit int) ([]string, error)
	// IssueClaimEmailChange issues the claim's email change (as
	// IssueEmailChange) only while claimHash still names a live account,
	// serialised with the link rotation and the expiry that would drop it.
	// It returns the account, or ErrNotFound.
	IssueClaimEmailChange(ctx context.Context, claimHash, changeHash []byte, newEmail string, expiresAt, now time.Time) (string, error)
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

// unclaimedNetwork returns an address and the network the per-network cap
// counts it in: its /24, or /48 for IPv6.
func unclaimedNetwork(ip string) (netip.Addr, netip.Prefix, bool) {
	addr, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil {
		return netip.Addr{}, netip.Prefix{}, false
	}
	addr = addr.Unmap().WithZone("")
	bits := 48
	if addr.Is4() {
		bits = 24
	}
	prefix, err := addr.Prefix(bits)
	if err != nil {
		return netip.Addr{}, netip.Prefix{}, false
	}
	return addr, prefix, true
}

// checkUnclaimedCaps compares the observed counts with the caps.
func checkUnclaimedCaps(p UnclaimedAccountParams, live, fromAddress, fromNetwork int, haveAddress bool) error {
	if live >= p.LiveMax {
		return &UnclaimedLimitError{Reason: UnclaimedLimitLive, Limit: p.LiveMax, Observed: live}
	}
	if !haveAddress {
		// No address to count against, but a cap set to 0 still closes.
		if p.PerAddress <= 0 {
			return &UnclaimedLimitError{Reason: UnclaimedLimitAddress, Limit: p.PerAddress}
		}
		if p.PerNetwork <= 0 {
			return &UnclaimedLimitError{Reason: UnclaimedLimitNetwork, Limit: p.PerNetwork}
		}
		return nil
	}
	if fromAddress >= p.PerAddress {
		return &UnclaimedLimitError{Reason: UnclaimedLimitAddress, Limit: p.PerAddress, Observed: fromAddress}
	}
	if fromNetwork >= p.PerNetwork {
		return &UnclaimedLimitError{Reason: UnclaimedLimitNetwork, Limit: p.PerNetwork, Observed: fromNetwork}
	}
	return nil
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
	addr, network, haveAddress := unclaimedNetwork(p.CreatedIP)
	live, fromAddress, fromNetwork := 0, 0, 0
	for id := range m.unclaimedAccounts {
		u, ok := m.unclaimedRowLocked(id)
		if !ok {
			continue
		}
		if bytes.Equal(u.ClaimTokenSHA256, p.ClaimTokenSHA256) {
			return ErrConflict
		}
		if u.Live(now) {
			live++
		}
		if other, err := netip.ParseAddr(u.CreatedIP); err == nil && haveAddress && u.CreatedAt.After(now.Add(-api.UnclaimedAccountWindow)) {
			other = other.Unmap()
			if network.Contains(other) {
				fromNetwork++
				if other == addr {
					fromAddress++
				}
			}
		}
	}
	if err := checkUnclaimedCaps(p, live, fromAddress, fromNetwork, haveAddress); err != nil {
		return err
	}
	created := ""
	if haveAddress {
		created = addr.String()
	}
	m.unclaimedAccounts[accountID] = UnclaimedAccount{
		AccountID:        accountID,
		ClaimTokenSHA256: append([]byte(nil), p.ClaimTokenSHA256...),
		CreatedAt:        now,
		ExpiresAt:        p.ExpiresAt.UTC(),
		CreatedIP:        created,
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

// clearPendingEmailChangesLocked mirrors ClearPendingAccountEmailChanges.
func (m *MemStore) clearPendingEmailChangesLocked(accountID string) {
	for k, c := range m.accountFlow.emailChanges {
		if c.accountID == accountID && !c.consumed {
			delete(m.accountFlow.emailChanges, k)
		}
	}
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
	m.clearPendingEmailChangesLocked(accountID)
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
	m.clearPendingEmailChangesLocked(accountID)
	return true, nil
}

func (m *MemStore) EndUnclaimedWindow(_ context.Context, accountID string, at time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.unclaimedRowLocked(accountID)
	if !ok || !u.Open() {
		return false, nil
	}
	if floor := u.CreatedAt.Add(time.Millisecond); at.Before(floor) {
		at = floor
	}
	if at.Before(u.ExpiresAt) {
		u.ExpiresAt = at.UTC()
	}
	m.unclaimedAccounts[accountID] = u
	m.clearPendingEmailChangesLocked(accountID)
	return true, nil
}

func (m *MemStore) ListExpiredUnclaimedAccounts(_ context.Context, now time.Time, after UnclaimedCursor, limit int) ([]UnclaimedCursor, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var due []UnclaimedAccount
	for id := range m.unclaimedAccounts {
		if u, ok := m.unclaimedRowLocked(id); ok && u.Open() && !u.ExpiresAt.After(now) && after.before(u) {
			due = append(due, u)
		}
	}
	sort.Slice(due, func(i, j int) bool {
		return UnclaimedCursor{ExpiresAt: due[i].ExpiresAt, AccountID: due[i].AccountID}.before(due[j])
	})
	out := make([]UnclaimedCursor, 0, min(limit, len(due)))
	for i := 0; i < len(due) && i < limit; i++ {
		out = append(out, UnclaimedCursor{ExpiresAt: due[i].ExpiresAt, AccountID: due[i].AccountID})
	}
	return out, nil
}

func (m *MemStore) ListUnrecordedClaims(_ context.Context, after string, limit int) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for id := range m.unclaimedAccounts {
		u, ok := m.unclaimedRowLocked(id)
		if !ok || !u.Open() || id <= after {
			continue
		}
		if a, ok := m.accounts[id]; ok && !api.IsUnclaimedPlaceholderEmail(a.Email) {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	if len(out) > limit {
		out = out[:max(0, limit)]
	}
	return out, nil
}

func (m *MemStore) IssueClaimEmailChange(_ context.Context, claimHash, changeHash []byte, newEmail string, expiresAt, now time.Time) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var row UnclaimedAccount
	found := false
	for id := range m.unclaimedAccounts {
		if u, ok := m.unclaimedRowLocked(id); ok && bytes.Equal(u.ClaimTokenSHA256, claimHash) {
			row, found = u, true
			break
		}
	}
	if !found || !row.Live(now) {
		return "", ErrNotFound
	}
	a, ok := m.accounts[row.AccountID]
	if !ok || a.Status != AccountActive {
		return "", ErrNotFound
	}
	if m.accountFlow.emailChanges == nil {
		m.accountFlow.emailChanges = map[string]memEmailChange{}
	}
	m.clearPendingEmailChangesLocked(row.AccountID)
	key := string(storedCredential(credTagEmailChange, changeHash))
	m.accountFlow.emailChanges[key] = memEmailChange{accountID: row.AccountID, oldEmail: strings.ToLower(a.Email), newEmail: newEmail, expiresAt: expiresAt}
	return row.AccountID, nil
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

func optionalPgTime(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time.UTC()
	return &v
}

// insertUnclaimedAccountTx checks the caps and records the row inside the
// account-creation transaction.
func insertUnclaimedAccountTx(ctx context.Context, tx pgx.Tx, accountID string, p UnclaimedAccountParams) error {
	if err := p.validate(); err != nil {
		return err
	}
	account, err := parsePgUUID(accountID)
	if err != nil {
		return err
	}
	q := sqlc.New()
	if err := q.LockUnclaimedAccountCreation(ctx, tx); err != nil {
		return fmt.Errorf("state: lock unclaimed account creation: %w", mapErr(err))
	}
	now := time.Now().UTC()
	live, err := q.CountLiveUnclaimedAccounts(ctx, tx, pgTime(now))
	if err != nil {
		return fmt.Errorf("state: count live unclaimed accounts: %w", mapErr(err))
	}
	addr, network, haveAddress := unclaimedNetwork(p.CreatedIP)
	var recent sqlc.CountRecentUnclaimedAccountsFromNetworkRow
	if haveAddress {
		recent, err = q.CountRecentUnclaimedAccountsFromNetwork(ctx, tx, sqlc.CountRecentUnclaimedAccountsFromNetworkParams{
			Address: addr, Since: pgTime(now.Add(-api.UnclaimedAccountWindow)), Network: network,
		})
		if err != nil {
			return fmt.Errorf("state: count recent unclaimed accounts: %w", mapErr(err))
		}
	}
	if err := checkUnclaimedCaps(p, int(live), int(recent.FromAddress), int(recent.FromNetwork), haveAddress); err != nil {
		return err
	}
	var ip *netip.Addr
	if haveAddress {
		ip = &addr
	}
	if err := q.InsertUnclaimedAccount(ctx, tx, sqlc.InsertUnclaimedAccountParams{
		AccountID: account, ClaimTokenSha256: p.ClaimTokenSHA256, ExpiresAt: pgTime(p.ExpiresAt),
		CreatedIp: ip, UserAgent: truncateUserAgent(p.UserAgent),
	}); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgerrcode.UniqueViolation {
			return ErrConflict
		}
		return fmt.Errorf("state: record unclaimed account: %w", mapErr(err))
	}
	return nil
}

func unclaimedFromRow(r sqlc.UnclaimedAccountByIDRow) UnclaimedAccount {
	return UnclaimedAccount{
		AccountID: r.AccountID, ClaimTokenSHA256: r.ClaimTokenSha256,
		CreatedAt: r.CreatedAt.Time.UTC(), ExpiresAt: r.ExpiresAt.Time.UTC(),
		ClaimedAt: optionalPgTime(r.ClaimedAt), ExpiredAt: optionalPgTime(r.ExpiredAt),
		CreatedIP: r.CreatedIp, UserAgent: r.UserAgent,
	}
}

func (s *PgStore) UnclaimedAccountByID(ctx context.Context, accountID string) (UnclaimedAccount, error) {
	account, err := parsePgUUID(accountID)
	if err != nil {
		return UnclaimedAccount{}, ErrNotFound
	}
	row, err := sqlc.New().UnclaimedAccountByID(ctx, s.pool, account)
	if err != nil {
		return UnclaimedAccount{}, mapErr(err)
	}
	return unclaimedFromRow(row), nil
}

func (s *PgStore) UnclaimedAccountByClaimToken(ctx context.Context, hash []byte) (UnclaimedAccount, error) {
	row, err := sqlc.New().UnclaimedAccountByClaimToken(ctx, s.pool, hash)
	if err != nil {
		return UnclaimedAccount{}, mapErr(err)
	}
	return unclaimedFromRow(sqlc.UnclaimedAccountByIDRow(row)), nil
}

func (s *PgStore) RotateUnclaimedClaimToken(ctx context.Context, accountID string, hash []byte) error {
	if len(hash) != 32 {
		return errors.New("state: unclaimed account: claim token hash must be 32 bytes")
	}
	account, err := parsePgUUID(accountID)
	if err != nil {
		return ErrNotFound
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := sqlc.New()
		n, err := q.RotateUnclaimedClaimToken(ctx, tx, sqlc.RotateUnclaimedClaimTokenParams{ClaimTokenSha256: hash, AccountID: account})
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == pgerrcode.UniqueViolation {
				return ErrConflict
			}
			return fmt.Errorf("state: rotate unclaimed claim token: %w", mapErr(err))
		}
		if n == 0 {
			return ErrNotFound
		}
		if err := q.ClearPendingAccountEmailChanges(ctx, tx, account); err != nil {
			return fmt.Errorf("state: clear pending claim confirmations: %w", mapErr(err))
		}
		return nil
	})
}

func (s *PgStore) MarkUnclaimedAccountClaimed(ctx context.Context, accountID string, at time.Time) (bool, error) {
	account, err := parsePgUUID(accountID)
	if err != nil {
		return false, fmt.Errorf("state: unclaimed account id: %w", err)
	}
	n, err := sqlc.New().MarkUnclaimedAccountClaimed(ctx, s.pool, sqlc.MarkUnclaimedAccountClaimedParams{At: pgTime(at), AccountID: account})
	if err != nil {
		return false, fmt.Errorf("state: mark unclaimed account claimed: %w", mapErr(err))
	}
	return n == 1, nil
}

func (s *PgStore) MarkUnclaimedAccountExpired(ctx context.Context, accountID string, at time.Time) (bool, error) {
	account, err := parsePgUUID(accountID)
	if err != nil {
		return false, fmt.Errorf("state: unclaimed account id: %w", err)
	}
	var marked bool
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := sqlc.New()
		n, err := q.MarkUnclaimedAccountExpired(ctx, tx, sqlc.MarkUnclaimedAccountExpiredParams{At: pgTime(at), AccountID: account})
		if err != nil {
			return fmt.Errorf("state: mark unclaimed account expired: %w", mapErr(err))
		}
		if marked = n == 1; !marked {
			return nil
		}
		if err := q.ClearPendingAccountEmailChanges(ctx, tx, account); err != nil {
			return fmt.Errorf("state: clear pending claim confirmations: %w", mapErr(err))
		}
		return nil
	})
	return marked, err
}

func (s *PgStore) EndUnclaimedWindow(ctx context.Context, accountID string, at time.Time) (bool, error) {
	account, err := parsePgUUID(accountID)
	if err != nil {
		return false, fmt.Errorf("state: unclaimed account id: %w", err)
	}
	var ended bool
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := sqlc.New()
		n, err := q.EndUnclaimedAccountWindow(ctx, tx, sqlc.EndUnclaimedAccountWindowParams{At: pgTime(at), AccountID: account})
		if err != nil {
			return fmt.Errorf("state: end unclaimed window: %w", mapErr(err))
		}
		if ended = n == 1; !ended {
			return nil
		}
		if err := q.ClearPendingAccountEmailChanges(ctx, tx, account); err != nil {
			return fmt.Errorf("state: clear pending claim confirmations: %w", mapErr(err))
		}
		return nil
	})
	return ended, err
}

func (s *PgStore) ListExpiredUnclaimedAccounts(ctx context.Context, now time.Time, after UnclaimedCursor, limit int) ([]UnclaimedCursor, error) {
	afterID := pgtype.UUID{Valid: true}
	if after.AccountID != "" {
		id, err := parsePgUUID(after.AccountID)
		if err != nil {
			return nil, fmt.Errorf("state: unclaimed sweep cursor: %w", err)
		}
		afterID = id
	}
	rows, err := sqlc.New().ListExpiredUnclaimedAccounts(ctx, s.pool, sqlc.ListExpiredUnclaimedAccountsParams{
		Now: pgTime(now), AfterExpiresAt: pgTime(after.ExpiresAt), AfterAccountID: afterID,
		RowLimit: int32(max(0, min(limit, 10_000))), //nolint:gosec // bounded above
	})
	if err != nil {
		return nil, fmt.Errorf("state: list expired unclaimed accounts: %w", mapErr(err))
	}
	out := make([]UnclaimedCursor, 0, len(rows))
	for _, r := range rows {
		out = append(out, UnclaimedCursor{ExpiresAt: r.ExpiresAt.Time.UTC(), AccountID: r.AccountID})
	}
	return out, nil
}

func (s *PgStore) ListUnrecordedClaims(ctx context.Context, after string, limit int) ([]string, error) {
	afterID := pgtype.UUID{Valid: true}
	if after != "" {
		id, err := parsePgUUID(after)
		if err != nil {
			return nil, fmt.Errorf("state: unrecorded claims cursor: %w", err)
		}
		afterID = id
	}
	ids, err := sqlc.New().ListUnrecordedUnclaimedClaims(ctx, s.pool, sqlc.ListUnrecordedUnclaimedClaimsParams{
		PlaceholderDomain: strings.ToLower(api.UnclaimedEmailDomain), AfterAccountID: afterID,
		RowLimit: int32(max(0, min(limit, 10_000))), //nolint:gosec // bounded above
	})
	if err != nil {
		return nil, fmt.Errorf("state: list unrecorded claims: %w", mapErr(err))
	}
	return ids, nil
}

func (s *PgStore) IssueClaimEmailChange(ctx context.Context, claimHash, changeHash []byte, newEmail string, expiresAt, now time.Time) (string, error) {
	var accountID string
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := sqlc.New()
		account, err := q.LockLiveUnclaimedAccountByClaimToken(ctx, tx, sqlc.LockLiveUnclaimedAccountByClaimTokenParams{
			ClaimTokenSha256: claimHash, Now: pgTime(now),
		})
		if err != nil {
			return mapErr(err)
		}
		if err := q.ClearPendingAccountEmailChanges(ctx, tx, account); err != nil {
			return fmt.Errorf("state: clear pending claim confirmations: %w", mapErr(err))
		}
		n, err := q.InsertAccountEmailChange(ctx, tx, sqlc.InsertAccountEmailChangeParams{
			TokenHash: storedCredential(credTagEmailChange, changeHash), NewEmail: newEmail,
			ExpiresAt: pgTime(expiresAt), AccountID: account,
		})
		if err != nil {
			return fmt.Errorf("state: issue claim confirmation: %w", mapErr(err))
		}
		if n == 0 {
			return ErrNotFound
		}
		accountID = pgUUIDStringOrEmpty(account)
		return nil
	})
	return accountID, err
}

func (s *PgStore) CountLiveUnclaimedAccounts(ctx context.Context, now time.Time) (int, error) {
	n, err := sqlc.New().CountLiveUnclaimedAccounts(ctx, s.pool, pgTime(now))
	if err != nil {
		return 0, fmt.Errorf("state: count live unclaimed accounts: %w", mapErr(err))
	}
	return int(n), nil
}
