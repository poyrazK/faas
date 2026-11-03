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
	ownerSecondApp, _ := store.CreateApp(ctx, App{AccountID: owner.ID, Slug: "zone-owner-2"})
	otherApp, _ := store.CreateApp(ctx, App{AccountID: other.ID, Slug: "zone-other"})
	if _, err := store.CreateCustomDomain(ctx, "*.example.test", ownerApp.ID, "tok"); err != nil {
		t.Fatal(err)
	}
	if got, err := ForeignVerifiedWildcard(ctx, store, "api.example.test", otherApp); err != nil || got != "" {
		t.Fatalf("pending wildcard = %q, %v; an unproved wildcard must not block", got, err)
	}
	if err := store.MarkDomainVerified(ctx, "*.example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateCustomDomain(ctx, "*.deep.example.test", otherApp.ID, "tok2"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		domain string
		app    App
		want   string
	}{
		{"api.example.test", otherApp, "*.example.test"},
		{"a.b.deep.example.test", otherApp, "*.example.test"},
		{"*.shop.example.test", otherApp, "*.example.test"},
		{"api.example.test", ownerApp, ""},
		{"api.example.test", ownerSecondApp, ""},
		{"*.shop.example.test", ownerApp, ""},
		{"example.test", otherApp, ""},
		{"*.example.test", otherApp, ""},
		{"api.badexample.test", otherApp, ""},
	} {
		got, err := ForeignVerifiedWildcard(ctx, store, tc.domain, tc.app)
		if err != nil || got != tc.want {
			t.Errorf("ForeignVerifiedWildcard(%q, %s) = %q, %v; want %q", tc.domain, tc.app.Slug, got, err, tc.want)
		}
	}
	failing := failingDomainLookup{err: errors.New("db down")}
	if _, err := ForeignVerifiedWildcard(ctx, failing, "api.example.test", otherApp); err == nil {
		t.Fatal("lookup failure was not reported; the create must fail closed")
	}
}

// A company's verified wildcard covers names for every company app, whoever
// created it: keyed by the creator's account, a member could not attach a
// name below the company's zone to an app they created, and once the
// wildcard's creator left the company no member could (bug hunt 32 M5). A
// member's personal app is another tenant.
func TestForeignVerifiedWildcardIsKeyedByTheOrganization(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	creator, _ := store.CreateAccount(ctx, "wildcard-creator@example.test", api.PlanScale)
	member, _ := store.CreateAccount(ctx, "member@example.test", api.PlanScale)
	company, err := store.CreateOrg(ctx, Org{Slug: "zone-company", Name: "Company", Plan: api.PlanScale})
	if err != nil {
		t.Fatal(err)
	}
	rival, err := store.CreateOrg(ctx, Org{Slug: "zone-rival", Name: "Rival", Plan: api.PlanScale})
	if err != nil {
		t.Fatal(err)
	}
	wildcardApp, _ := store.CreateApp(ctx, App{AccountID: creator.ID, OrgID: company.ID, Slug: "company-wildcard"})
	memberCompanyApp, _ := store.CreateApp(ctx, App{AccountID: member.ID, OrgID: company.ID, Slug: "company-member"})
	memberPersonalApp, _ := store.CreateApp(ctx, App{AccountID: member.ID, Slug: "member-personal"})
	creatorPersonalApp, _ := store.CreateApp(ctx, App{AccountID: creator.ID, Slug: "creator-personal"})
	rivalApp, _ := store.CreateApp(ctx, App{AccountID: creator.ID, OrgID: rival.ID, Slug: "rival-app"})
	if _, err := store.CreateCustomDomain(ctx, "*.company.test", wildcardApp.ID, "tok"); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDomainVerified(ctx, "*.company.test"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		app  App
		want string
	}{
		{memberCompanyApp, ""},
		{wildcardApp, ""},
		{memberPersonalApp, "*.company.test"},
		{creatorPersonalApp, "*.company.test"},
		{rivalApp, "*.company.test"},
	} {
		got, err := ForeignVerifiedWildcard(ctx, store, "shop.company.test", tc.app)
		if err != nil || got != tc.want {
			t.Errorf("ForeignVerifiedWildcard(shop.company.test, %s) = %q, %v; want %q", tc.app.Slug, got, err, tc.want)
		}
	}
}

func TestAppTenantID(t *testing.T) {
	owner := "acct-1"
	orgs := orgLookup{
		"org-shared":   {ID: "org-shared"},
		"org-personal": {ID: "org-personal", Personal: true, PersonalOwnerAccountID: &owner},
	}
	for _, tc := range []struct {
		app     App
		want    string
		wantErr bool
	}{
		{App{AccountID: owner}, "account:acct-1", false},
		{App{AccountID: owner, OrgID: "org-personal"}, "account:acct-1", false},
		{App{AccountID: owner, OrgID: "org-shared"}, "org:org-shared", false},
		{App{AccountID: "acct-2", OrgID: "org-shared"}, "org:org-shared", false},
		{App{AccountID: owner, OrgID: "org-missing"}, "", true},
	} {
		got, err := AppTenantID(context.Background(), orgs, tc.app)
		if got != tc.want || (err != nil) != tc.wantErr {
			t.Errorf("AppTenantID(%+v) = %q, %v; want %q (error %v)", tc.app, got, err, tc.want, tc.wantErr)
		}
	}
}

type orgLookup map[string]Org

func (o orgLookup) OrgByID(_ context.Context, id string) (Org, error) {
	org, ok := o[id]
	if !ok {
		return Org{}, ErrNotFound
	}
	return org, nil
}

type failingDomainLookup struct{ err error }

func (f failingDomainLookup) DomainByName(context.Context, string) (CustomDomain, error) {
	return CustomDomain{}, f.err
}

func (f failingDomainLookup) AppByID(context.Context, string) (App, error) {
	return App{}, f.err
}

func (f failingDomainLookup) OrgByID(context.Context, string) (Org, error) {
	return Org{}, f.err
}
