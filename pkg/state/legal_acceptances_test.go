package state

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// legalAcceptanceStore is the slice of PgStore and MemStore these tests use.
type legalAcceptanceStore interface {
	CreateAccountWithPersonalOrg(context.Context, CreateAccountWithPersonalOrgParams) (CreateAccountWithPersonalOrgResult, error)
	LegalAcceptances(context.Context, string) ([]LegalAcceptance, error)
	AccountByEmail(context.Context, string) (Account, error)
}

// Account creation stores the Terms acceptance with the account, and an
// invalid acceptance creates nothing.
func testTermsAcceptanceWithAccount(t *testing.T, s legalAcceptanceStore) {
	t.Helper()
	ctx := context.Background()
	res, err := s.CreateAccountWithPersonalOrg(ctx, CreateAccountWithPersonalOrgParams{
		Email: "terms@x.com", Plan: api.PlanFree,
		TermsAcceptance: &TermsAcceptance{Version: "2026-12-01", Method: "github"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.LegalAcceptances(ctx, res.Account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].AccountID != res.Account.ID || got[0].Document != LegalDocumentTerms ||
		got[0].Version != "2026-12-01" || got[0].Method != "github" || got[0].AcceptedAt.IsZero() {
		t.Fatalf("acceptances = %+v", got)
	}

	plain, err := s.CreateAccountWithPersonalOrg(ctx, CreateAccountWithPersonalOrgParams{Email: "plain@x.com", Plan: api.PlanFree})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := s.LegalAcceptances(ctx, plain.Account.ID); len(got) != 0 {
		t.Fatalf("account without acceptance has rows: %+v", got)
	}

	for _, bad := range []*TermsAcceptance{{Version: "1 Dec 2026", Method: "password"}, {Version: "2026-12-01"}} {
		if _, err := s.CreateAccountWithPersonalOrg(ctx, CreateAccountWithPersonalOrgParams{
			Email: "bad@x.com", Plan: api.PlanFree, TermsAcceptance: bad,
		}); err == nil {
			t.Fatalf("invalid acceptance %+v accepted", bad)
		}
		if _, err := s.AccountByEmail(ctx, "bad@x.com"); err == nil {
			t.Fatalf("invalid acceptance %+v still created the account", bad)
		}
	}
}

func TestMemStore_CreateAccountWithPersonalOrg_TermsAcceptance(t *testing.T) {
	testTermsAcceptanceWithAccount(t, NewMemStore())
}
