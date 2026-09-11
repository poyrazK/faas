package state_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// PostgreSQL represents a nullable domain timestamp as epoch in its shared
// projection. The store boundary must translate that sentinel back to Go's
// zero time so every API surface reports a fresh domain as unverified.
func TestPgStoreCustomDomainVerificationState(t *testing.T) {
	s, _, ctx := pgStoreWithPool(t)
	acct, err := s.CreateAccount(ctx, "domain-verification-pg@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{
		AccountID: acct.ID,
		Slug:      "domain-verification-pg-app",
		Status:    state.AppActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	domain, err := s.CreateCustomDomain(ctx, "unverified-pg.example.com", app.ID, "token")
	if err != nil {
		t.Fatal(err)
	}
	assertUnverified := func(source string, got state.CustomDomain) {
		t.Helper()
		if got.Verified() || !got.VerifiedAt.IsZero() {
			t.Fatalf("%s: fresh domain reported verified: %+v", source, got)
		}
	}
	assertUnverified("create", domain)

	byName, err := s.DomainByName(ctx, domain.Domain)
	if err != nil {
		t.Fatal(err)
	}
	assertUnverified("by name", byName)
	if err := s.UpdateCustomDomainCertStatus(ctx, domain.Domain, state.CustomDomainCertFailed, time.Time{}, "dns challenge missing", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	failed, err := s.DomainByName(ctx, domain.Domain)
	if err != nil {
		t.Fatal(err)
	}
	assertUnverified("failed verification", failed)

	appDomains, err := s.ListDomainsForApp(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	accountDomains, err := s.ListDomainsForAccount(ctx, acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	unverifiedDomains, err := s.ListUnverifiedCustomDomains(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for source, list := range map[string][]state.CustomDomain{
		"for app":     appDomains,
		"for account": accountDomains,
		"unverified":  unverifiedDomains,
	} {
		if len(list) != 1 {
			t.Fatalf("%s: got %d domains, want 1", source, len(list))
		}
		assertUnverified(source, list[0])
	}

	if err := s.MarkDomainVerified(ctx, domain.Domain); err != nil {
		t.Fatal(err)
	}
	verified, err := s.DomainByName(ctx, domain.Domain)
	if err != nil {
		t.Fatal(err)
	}
	if !verified.Verified() || verified.VerifiedAt.IsZero() {
		t.Fatalf("verified domain did not retain timestamp: %+v", verified)
	}
}
