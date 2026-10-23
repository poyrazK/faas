// adr: 167
package state

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCustomDomainClaimLive(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		d    CustomDomain
		want bool
	}{
		{"pending before deadline", CustomDomain{VerificationExpiresAt: now.Add(time.Minute)}, true},
		{"pending at deadline", CustomDomain{VerificationExpiresAt: now}, false},
		{"pending after deadline", CustomDomain{VerificationExpiresAt: now.Add(-time.Minute)}, false},
		{"verified after deadline", CustomDomain{VerifiedAt: now.Add(-time.Hour), VerificationExpiresAt: now.Add(-time.Minute)}, true},
		{"legacy row without deadline", CustomDomain{}, true},
	} {
		if got := tc.d.ClaimLive(now); got != tc.want {
			t.Errorf("%s: ClaimLive = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// An expired, unverified wildcard reserves nothing: it must not hide a
// broader verified wildcard from routing or block tenant hostnames below it.
func TestWildcardDomainForHostSkipsExpiredClaims(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	if _, err := store.CreateCustomDomain(ctx, "*.example.test", "app-zone", "tok-zone"); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDomainVerified(ctx, "*.example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateCustomDomain(ctx, "*.shop.example.test", "app-squat", "tok-squat"); err != nil {
		t.Fatal(err)
	}
	if got, err := store.WildcardDomainForHost(ctx, "a.shop.example.test"); err != nil || got.Domain != "*.shop.example.test" {
		t.Fatalf("live pending claim = %q, %v; want it to shadow the broader wildcard", got.Domain, err)
	}
	store.mu.Lock()
	expired := store.domains["*.shop.example.test"]
	expired.VerificationExpiresAt = time.Now().Add(-time.Minute)
	store.domains["*.shop.example.test"] = expired
	store.mu.Unlock()
	if got, err := store.WildcardDomainForHost(ctx, "a.shop.example.test"); err != nil || got.Domain != "*.example.test" {
		t.Fatalf("expired claim lookup = %q, %v; want the verified *.example.test", got.Domain, err)
	}
}

func TestForeignVerifiedWildcard(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	owner, _ := store.CreateAccount(ctx, "zone-owner@example.test", api.PlanScale)
	other, _ := store.CreateAccount(ctx, "other@example.test", api.PlanScale)
	ownerApp, _ := store.CreateApp(ctx, App{AccountID: owner.ID, Slug: "zone-owner"})
	otherApp, _ := store.CreateApp(ctx, App{AccountID: other.ID, Slug: "zone-other"})
	if _, err := store.CreateCustomDomain(ctx, "*.example.test", ownerApp.ID, "tok"); err != nil {
		t.Fatal(err)
	}
	if got, err := ForeignVerifiedWildcard(ctx, store, "api.example.test", other.ID); err != nil || got != "" {
		t.Fatalf("pending wildcard = %q, %v; an unproved wildcard must not block", got, err)
	}
	if err := store.MarkDomainVerified(ctx, "*.example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateCustomDomain(ctx, "*.deep.example.test", otherApp.ID, "tok2"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		domain, account, want string
	}{
		{"api.example.test", other.ID, "*.example.test"},
		{"a.b.deep.example.test", other.ID, "*.example.test"},
		{"*.shop.example.test", other.ID, "*.example.test"},
		{"api.example.test", owner.ID, ""},
		{"*.shop.example.test", owner.ID, ""},
		{"example.test", other.ID, ""},
		{"*.example.test", other.ID, ""},
		{"api.badexample.test", other.ID, ""},
	} {
		got, err := ForeignVerifiedWildcard(ctx, store, tc.domain, tc.account)
		if err != nil || got != tc.want {
			t.Errorf("ForeignVerifiedWildcard(%q, %s) = %q, %v; want %q", tc.domain, tc.account, got, err, tc.want)
		}
	}
	failing := failingDomainLookup{err: errors.New("db down")}
	if _, err := ForeignVerifiedWildcard(ctx, failing, "api.example.test", other.ID); err == nil {
		t.Fatal("lookup failure was not reported; the create must fail closed")
	}
}

type failingDomainLookup struct{ err error }

func (f failingDomainLookup) DomainByName(context.Context, string) (CustomDomain, error) {
	return CustomDomain{}, f.err
}

func (f failingDomainLookup) AppByID(context.Context, string) (App, error) {
	return App{}, f.err
}
