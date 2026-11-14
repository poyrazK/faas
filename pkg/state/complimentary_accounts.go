package state

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/lazyregexp"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ComplimentaryAccount is an account an operator gave a paid plan without a
// subscription (ADR-20261111140000). meterd neither bills nor holds its usage;
// its spend cap is $0, so it stops at the plan's included amounts; once
// ExpiresAt passes (when set), apid returns it to Free.
type ComplimentaryAccount struct {
	AccountID string
	Plan      api.Plan
	Reason    string
	GrantedBy string
	GrantedAt time.Time
	// ExpiresAt is when the account returns to Free; zero means it does not.
	ExpiresAt time.Time
}

// ComplimentaryAccountStore holds complimentary accounts. apid is the only
// writer; meterd reads the IDs.
type ComplimentaryAccountStore interface {
	// ComplimentaryAccount returns the account's row, nil when it has none.
	ComplimentaryAccount(ctx context.Context, accountID string) (*ComplimentaryAccount, error)
	// SetComplimentaryAccount writes the account's row, replacing any it had.
	SetComplimentaryAccount(ctx context.Context, c ComplimentaryAccount) error
	// ClearComplimentaryAccount removes the account's row, if it has one.
	ClearComplimentaryAccount(ctx context.Context, accountID string) error
	// ComplimentaryAccountIDs returns every complimentary account's ID.
	ComplimentaryAccountIDs(ctx context.Context) (map[string]struct{}, error)
	// ExpiredComplimentaryAccounts returns up to limit rows whose end date is
	// at or before now, oldest first.
	ExpiredComplimentaryAccounts(ctx context.Context, now time.Time, limit int) ([]ComplimentaryAccount, error)
}

var complimentaryReason = lazyregexp.New(`^[a-z0-9_]{1,64}$`)

// validComplimentaryAccount holds a row to the table's CHECKs.
func validComplimentaryAccount(c ComplimentaryAccount) error {
	if !c.Plan.IsPaid() {
		return fmt.Errorf("complimentary account: plan %q is not a paid plan", c.Plan)
	}
	if !complimentaryReason.MatchString(c.Reason) {
		return fmt.Errorf("complimentary account: reason %q must match [a-z0-9_]{1,64}", c.Reason)
	}
	if c.GrantedAt.IsZero() {
		return fmt.Errorf("complimentary account: granted_at is required")
	}
	if !c.ExpiresAt.IsZero() && !c.ExpiresAt.After(c.GrantedAt) {
		return fmt.Errorf("complimentary account: expires_at must be after granted_at")
	}
	return nil
}

func complimentaryFromSQL(row sqlc.ComplimentaryAccount) ComplimentaryAccount {
	c := ComplimentaryAccount{AccountID: pgUUIDString(row.AccountID), Plan: api.Plan(row.Plan), Reason: row.Reason,
		GrantedBy: pgUUIDString(row.GrantedBy), GrantedAt: row.GrantedAt.Time.UTC()}
	if row.ExpiresAt.Valid {
		c.ExpiresAt = row.ExpiresAt.Time.UTC()
	}
	return c
}

// ComplimentaryAccount implements ComplimentaryAccountStore.
func (s *PgStore) ComplimentaryAccount(ctx context.Context, accountID string) (*ComplimentaryAccount, error) {
	id, err := parsePgUUID(accountID)
	if err != nil {
		return nil, err
	}
	row, err := sqlc.New().ComplimentaryAccount(ctx, s.pool, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read complimentary account: %w", err)
	}
	c := complimentaryFromSQL(row)
	return &c, nil
}

// SetComplimentaryAccount implements ComplimentaryAccountStore.
func (s *PgStore) SetComplimentaryAccount(ctx context.Context, c ComplimentaryAccount) error {
	if err := validComplimentaryAccount(c); err != nil {
		return err
	}
	id, err := parsePgUUID(c.AccountID)
	if err != nil {
		return err
	}
	by, err := parsePgUUID(c.GrantedBy)
	if err != nil {
		return err
	}
	expires := pgtype.Timestamptz{}
	if !c.ExpiresAt.IsZero() {
		expires = pgtype.Timestamptz{Time: c.ExpiresAt.UTC(), Valid: true}
	}
	if err := sqlc.New().SetComplimentaryAccount(ctx, s.pool, sqlc.SetComplimentaryAccountParams{
		AccountID: id, Plan: string(c.Plan), Reason: c.Reason, GrantedBy: by,
		GrantedAt: pgtype.Timestamptz{Time: c.GrantedAt.UTC(), Valid: true}, ExpiresAt: expires,
	}); err != nil {
		return fmt.Errorf("write complimentary account: %w", err)
	}
	return nil
}

// ClearComplimentaryAccount implements ComplimentaryAccountStore.
func (s *PgStore) ClearComplimentaryAccount(ctx context.Context, accountID string) error {
	id, err := parsePgUUID(accountID)
	if err != nil {
		return err
	}
	if err := sqlc.New().ClearComplimentaryAccount(ctx, s.pool, id); err != nil {
		return fmt.Errorf("clear complimentary account: %w", err)
	}
	return nil
}

// ComplimentaryAccountIDs implements ComplimentaryAccountStore.
func (s *PgStore) ComplimentaryAccountIDs(ctx context.Context) (map[string]struct{}, error) {
	rows, err := sqlc.New().ListComplimentaryAccountIDs(ctx, s.pool)
	if err != nil {
		return nil, fmt.Errorf("list complimentary accounts: %w", err)
	}
	ids := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		ids[pgUUIDString(row)] = struct{}{}
	}
	return ids, nil
}

// ExpiredComplimentaryAccounts implements ComplimentaryAccountStore.
func (s *PgStore) ExpiredComplimentaryAccounts(ctx context.Context, now time.Time, limit int) ([]ComplimentaryAccount, error) {
	if limit < 1 || limit > 500 {
		return nil, fmt.Errorf("expired complimentary accounts: limit %d out of range", limit)
	}
	rows, err := sqlc.New().ListExpiredComplimentaryAccounts(ctx, s.pool, sqlc.ListExpiredComplimentaryAccountsParams{
		Now: pgtype.Timestamptz{Time: now.UTC(), Valid: true}, BatchSize: int32(limit)})
	if err != nil {
		return nil, fmt.Errorf("list expired complimentary accounts: %w", err)
	}
	out := make([]ComplimentaryAccount, 0, len(rows))
	for _, row := range rows {
		out = append(out, complimentaryFromSQL(row))
	}
	return out, nil
}

// ComplimentaryAccount implements ComplimentaryAccountStore.
func (m *MemStore) ComplimentaryAccount(_ context.Context, accountID string) (*ComplimentaryAccount, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.complimentaryAccounts[accountID]
	if !ok {
		return nil, nil
	}
	return &c, nil
}

// SetComplimentaryAccount implements ComplimentaryAccountStore.
func (m *MemStore) SetComplimentaryAccount(_ context.Context, c ComplimentaryAccount) error {
	if err := validComplimentaryAccount(c); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.accounts[c.AccountID]; !ok {
		return ErrNotFound
	}
	if m.complimentaryAccounts == nil {
		m.complimentaryAccounts = map[string]ComplimentaryAccount{}
	}
	c.GrantedAt = c.GrantedAt.UTC()
	if !c.ExpiresAt.IsZero() {
		c.ExpiresAt = c.ExpiresAt.UTC()
	}
	m.complimentaryAccounts[c.AccountID] = c
	return nil
}

// ClearComplimentaryAccount implements ComplimentaryAccountStore.
func (m *MemStore) ClearComplimentaryAccount(_ context.Context, accountID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.complimentaryAccounts, accountID)
	return nil
}

// ComplimentaryAccountIDs implements ComplimentaryAccountStore.
func (m *MemStore) ComplimentaryAccountIDs(_ context.Context) (map[string]struct{}, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make(map[string]struct{}, len(m.complimentaryAccounts))
	for id := range m.complimentaryAccounts {
		ids[id] = struct{}{}
	}
	return ids, nil
}

// ExpiredComplimentaryAccounts implements ComplimentaryAccountStore.
func (m *MemStore) ExpiredComplimentaryAccounts(_ context.Context, now time.Time, limit int) ([]ComplimentaryAccount, error) {
	if limit < 1 || limit > 500 {
		return nil, fmt.Errorf("expired complimentary accounts: limit %d out of range", limit)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []ComplimentaryAccount
	for _, c := range m.complimentaryAccounts {
		if !c.ExpiresAt.IsZero() && !c.ExpiresAt.After(now) {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].ExpiresAt.Equal(out[j].ExpiresAt) {
			return out[i].ExpiresAt.Before(out[j].ExpiresAt)
		}
		return out[i].AccountID < out[j].AccountID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
