package state

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	factsql "github.com/onebox-faas/faas/pkg/state/signupfacts/sqlc"
)

var (
	_ AccountSignupFactsStore = (*PgStore)(nil)
	_ AccountSignupFactsStore = (*MemStore)(nil)
)

var (
	signupMethodPattern  = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
	signupCountryPattern = regexp.MustCompile(`^[A-Z]{2}$`)
)

// Bounds mirrored from the account_signup_facts CHECK constraints.
const (
	signupFactsUserAgentMax   = 512
	signupFactsASNOrgMax      = 200
	signupFactsEmailDomainMax = 253
)

// normalizeSignupFacts validates the facts and trims free text to the column
// bounds, so a long user agent never fails a sign-up record.
func normalizeSignupFacts(f AccountSignupFacts) (AccountSignupFacts, error) {
	if f.AccountID == "" || !signupMethodPattern.MatchString(f.Method) || f.CreatedAt.IsZero() {
		return f, ErrInvalidArgument
	}
	if f.SignupIP != "" {
		addr, err := netip.ParseAddr(f.SignupIP)
		if err != nil {
			return f, ErrInvalidArgument
		}
		f.SignupIP = addr.Unmap().String()
	}
	if f.Country != "" && !signupCountryPattern.MatchString(f.Country) {
		f.Country = ""
	}
	f.UserAgent = truncateRunes(f.UserAgent, signupFactsUserAgentMax)
	f.ASNOrg = truncateRunes(f.ASNOrg, signupFactsASNOrgMax)
	if len(f.EmailDomain) > signupFactsEmailDomainMax {
		return f, ErrInvalidArgument
	}
	f.CreatedAt = f.CreatedAt.UTC()
	f.ReducedAt = nil
	return f, nil
}

// RecordAccountSignupFacts implements AccountSignupFactsStore.
func (s *PgStore) RecordAccountSignupFacts(ctx context.Context, facts AccountSignupFacts) error {
	f, err := normalizeSignupFacts(facts)
	if err != nil {
		return err
	}
	account := mustPgUUID(f.AccountID)
	if !account.Valid {
		return ErrInvalidArgument
	}
	params := factsql.InsertSignupFactsParams{
		AccountID: account, Method: f.Method,
		UserAgent: optText(f.UserAgent), Country: optText(f.Country), AsnOrg: optText(f.ASNOrg),
		Asn:         pgtype.Int8{Int64: int64(f.ASN), Valid: f.ASN != 0},
		EmailDomain: f.EmailDomain, DisposableEmail: f.DisposableEmail,
		CreatedAt: pgtype.Timestamptz{Time: f.CreatedAt, Valid: true},
	}
	if f.SignupIP != "" {
		addr := netip.MustParseAddr(f.SignupIP)
		params.SignupIp = &addr
	}
	if err := factsql.New().InsertSignupFacts(ctx, s.pool, params); err != nil {
		return fmt.Errorf("state: record signup facts %s: %w", f.AccountID, mapErr(err))
	}
	return nil
}

// AccountSignupFacts implements AccountSignupFactsStore.
func (s *PgStore) AccountSignupFacts(ctx context.Context, accountID string) (AccountSignupFacts, error) {
	account := mustPgUUID(accountID)
	if !account.Valid {
		return AccountSignupFacts{}, ErrNotFound
	}
	row, err := factsql.New().GetSignupFacts(ctx, s.pool, account)
	if errors.Is(err, pgx.ErrNoRows) {
		return AccountSignupFacts{}, ErrNotFound
	}
	if err != nil {
		return AccountSignupFacts{}, fmt.Errorf("state: signup facts %s: %w", accountID, mapErr(err))
	}
	out := AccountSignupFacts{
		AccountID: accountID, Method: row.Method, SignupIP: row.SignupIp,
		UserAgent: row.UserAgent.String, Country: row.Country.String, ASNOrg: row.AsnOrg.String,
		EmailDomain: row.EmailDomain, DisposableEmail: row.DisposableEmail,
		CreatedAt: row.CreatedAt.Time.UTC(),
	}
	if row.Asn.Valid {
		out.ASN = uint32(row.Asn.Int64)
	}
	if row.ReducedAt.Valid {
		at := row.ReducedAt.Time.UTC()
		out.ReducedAt = &at
	}
	return out, nil
}

// ReduceAccountSignupFacts implements AccountSignupFactsStore.
func (s *PgStore) ReduceAccountSignupFacts(ctx context.Context, cutoff, now time.Time, batch int) (int64, error) {
	if batch <= 0 {
		return 0, ErrInvalidArgument
	}
	n, err := factsql.New().ReduceSignupFacts(ctx, s.pool, factsql.ReduceSignupFactsParams{
		ReducedAt: pgtype.Timestamptz{Time: now.UTC(), Valid: true},
		Cutoff:    pgtype.Timestamptz{Time: cutoff.UTC(), Valid: true},
		Batch:     int32(batch),
	})
	if err != nil {
		return 0, fmt.Errorf("state: reduce signup facts: %w", mapErr(err))
	}
	return n, nil
}

// RecordAccountSignupFacts implements AccountSignupFactsStore.
func (m *MemStore) RecordAccountSignupFacts(_ context.Context, facts AccountSignupFacts) error {
	f, err := normalizeSignupFacts(facts)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.accounts[f.AccountID]; !ok {
		return ErrNotFound
	}
	if m.signupFacts == nil {
		m.signupFacts = make(map[string]AccountSignupFacts)
	}
	if _, ok := m.signupFacts[f.AccountID]; !ok {
		m.signupFacts[f.AccountID] = f
	}
	return nil
}

// AccountSignupFacts implements AccountSignupFactsStore.
func (m *MemStore) AccountSignupFacts(_ context.Context, accountID string) (AccountSignupFacts, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.signupFacts[accountID]
	if !ok {
		return AccountSignupFacts{}, ErrNotFound
	}
	if f.ReducedAt != nil {
		at := *f.ReducedAt
		f.ReducedAt = &at
	}
	return f, nil
}

// ReduceAccountSignupFacts implements AccountSignupFactsStore.
func (m *MemStore) ReduceAccountSignupFacts(_ context.Context, cutoff, now time.Time, batch int) (int64, error) {
	if batch <= 0 {
		return 0, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for id, f := range m.signupFacts {
		if n >= int64(batch) {
			break
		}
		if f.ReducedAt != nil || !f.CreatedAt.Before(cutoff) {
			continue
		}
		at := now.UTC()
		f.SignupIP, f.UserAgent, f.ReducedAt = "", "", &at
		m.signupFacts[id] = f
		n++
	}
	return n, nil
}
