package state

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// LegalDocumentTerms is the only document accepted at sign-up today; the
// Privacy Policy is a notice, not an agreement.
const LegalDocumentTerms = "terms"

// TermsAcceptance is the Terms of Service version a new account accepts at
// sign-up (pkg/legal Config.Version) and the sign-up method that showed it.
type TermsAcceptance struct {
	Version string
	Method  string
}

// LegalAcceptance is one stored acceptance row (table legal_acceptances).
type LegalAcceptance struct {
	AccountID  string
	Document   string
	Version    string
	Method     string
	AcceptedAt time.Time
}

func (a *TermsAcceptance) validate() error {
	if a == nil {
		return nil
	}
	if _, err := time.Parse("2006-01-02", a.Version); err != nil {
		return fmt.Errorf("state: terms acceptance version %q: want YYYY-MM-DD", a.Version)
	}
	if a.Method == "" || len(a.Method) > 32 {
		return fmt.Errorf("state: terms acceptance method %q", a.Method)
	}
	return nil
}

// LegalAcceptances lists an account's stored acceptances, oldest first.
func (s *PgStore) LegalAcceptances(ctx context.Context, accountID string) ([]LegalAcceptance, error) {
	rows, err := s.pool.Query(ctx, `
		select account_id, document, version, method, accepted_at
		from legal_acceptances where account_id = $1
		order by accepted_at, document, version`, accountID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	var out []LegalAcceptance
	for rows.Next() {
		var a LegalAcceptance
		if err := rows.Scan(&a.AccountID, &a.Document, &a.Version, &a.Method, &a.AcceptedAt); err != nil {
			return nil, mapErr(err)
		}
		out = append(out, a)
	}
	return out, mapErr(rows.Err())
}

// LegalAcceptances lists an account's stored acceptances, oldest first.
func (m *MemStore) LegalAcceptances(_ context.Context, accountID string) ([]LegalAcceptance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := append([]LegalAcceptance(nil), m.legalAcceptances[accountID]...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].AcceptedAt.Before(out[j].AcceptedAt) })
	return out, nil
}
