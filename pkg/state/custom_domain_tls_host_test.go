package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type tlsHostTestStore interface {
	state.CustomDomainTLSHostStore
	CreateAccount(ctx context.Context, email string, plan api.Plan) (state.Account, error)
	CreateOrg(ctx context.Context, org state.Org) (state.Org, error)
	UpdateOrgPlan(ctx context.Context, id string, plan api.Plan) error
	CreateApp(ctx context.Context, app state.App) (state.App, error)
	CreateCustomDomain(ctx context.Context, domain, appID, token string) (state.CustomDomain, error)
	MarkDomainVerifiedIfChallenge(ctx context.Context, domain, token string) (bool, error)
	DeleteCustomDomain(ctx context.Context, domain string) error
}

func tlsHostStores() map[string]func(t *testing.T) (tlsHostTestStore, context.Context) {
	return map[string]func(t *testing.T) (tlsHostTestStore, context.Context){
		"mem": func(t *testing.T) (tlsHostTestStore, context.Context) {
			return state.NewMemStore(), context.Background()
		},
		"pg": func(t *testing.T) (tlsHostTestStore, context.Context) {
			s, ctx := pgStore(t)
			return s, ctx
		},
	}
}

// tlsHostApp creates an app owned by a new account, or by org when set.
func tlsHostApp(t *testing.T, ctx context.Context, s tlsHostTestStore, orgID string) string {
	t.Helper()
	acct, err := s.CreateAccount(ctx, "tls-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, OrgID: orgID, Slug: "tls-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	return app.ID
}

func verifiedTLSWildcard(t *testing.T, ctx context.Context, s tlsHostTestStore, wildcard, appID string) {
	t.Helper()
	if _, err := s.CreateCustomDomain(ctx, wildcard, appID, "tok"); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.MarkDomainVerifiedIfChallenge(ctx, wildcard, "tok"); err != nil || !ok {
		t.Fatalf("verify %s = %v, %v", wildcard, ok, err)
	}
}

// roomyTLSAdmission has every cap far above what a test admits.
func roomyTLSAdmission(wildcard, host string, now time.Time) state.CustomDomainTLSAdmission {
	return state.CustomDomainTLSAdmission{
		Wildcard: wildcard, Host: host, Now: now,
		Window: 7 * 24 * time.Hour, PerRegisteredDomain: 100, PerOwner: 100,
		FleetWindow: 3 * time.Hour, FleetLimit: 100,
	}
}

// TestCustomDomainTLSHostAdmission pins ADR-520's wildcard issuance budget on
// both stores: known hosts are free, new hosts are capped per rolling window,
// unverified or deleted wildcards admit nothing, and deleting a wildcard does
// not refill the budget (amendment 1).
func TestCustomDomainTLSHostAdmission(t *testing.T) {
	for name, open := range tlsHostStores() {
		t.Run(name, func(t *testing.T) {
			s, ctx := open(t)
			appID := tlsHostApp(t, ctx, s, "")
			suffix := uuid.NewString()[:8] + ".example.com"
			wildcard := "*." + suffix
			if _, err := s.CreateCustomDomain(ctx, wildcard, appID, "tok"); err != nil {
				t.Fatal(err)
			}
			now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
			const window = 7 * 24 * time.Hour
			admit := func(host string, at time.Time, limit int) state.CustomDomainTLSVerdict {
				t.Helper()
				a := roomyTLSAdmission(wildcard, host, at)
				a.PerRegisteredDomain = limit
				v, err := s.AdmitCustomDomainTLSHost(ctx, a)
				if err != nil {
					t.Fatalf("admit %s: %v", host, err)
				}
				return v
			}

			if v := admit("a."+suffix, now, 2); v != state.TLSHostWildcardUnverified {
				t.Fatalf("unverified wildcard = %v, want TLSHostWildcardUnverified", v)
			}
			if ok, err := s.MarkDomainVerifiedIfChallenge(ctx, wildcard, "tok"); err != nil || !ok {
				t.Fatalf("verify wildcard = %v, %v", ok, err)
			}
			if admit("a."+suffix, now, 2) != state.TLSHostAdmitted || admit("b."+suffix, now, 2) != state.TLSHostAdmitted {
				t.Fatal("hosts within the budget were refused")
			}
			if v := admit("c."+suffix, now, 2); v != state.TLSHostRegisteredDomainFull {
				t.Fatalf("third new host past a budget of 2 = %v", v)
			}
			if admit("A."+suffix, now.Add(time.Hour), 2) != state.TLSHostAdmitted {
				t.Fatal("a known host must be re-admitted without budget (reload/renewal)")
			}
			later := now.Add(window + time.Minute)
			if admit("c."+suffix, later, 2) != state.TLSHostAdmitted {
				t.Fatal("budget did not refill after the window")
			}
			if v := admit("d."+suffix, later, 0); v != state.TLSHostRegisteredDomainFull {
				t.Fatalf("zero budget = %v", v)
			}

			if _, err := s.AdmitCustomDomainTLSHost(ctx, roomyTLSAdmission(wildcard, suffix, now)); !errors.Is(err, state.ErrInvalidArgument) {
				t.Fatalf("apex below its own wildcard = %v, want ErrInvalidArgument", err)
			}
			if _, err := s.AdmitCustomDomainTLSHost(ctx, roomyTLSAdmission(wildcard, "x.other.com", now)); !errors.Is(err, state.ErrInvalidArgument) {
				t.Fatalf("unrelated host = %v, want ErrInvalidArgument", err)
			}

			if err := s.DeleteCustomDomain(ctx, wildcard); err != nil {
				t.Fatal(err)
			}
			if _, err := s.AdmitCustomDomainTLSHost(ctx, roomyTLSAdmission(wildcard, "a."+suffix, later)); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("deleted wildcard = %v, want ErrNotFound", err)
			}
			// Re-creating the wildcard forgets its hosts but not the
			// admissions the budget counts.
			if _, err := s.CreateCustomDomain(ctx, wildcard, appID, "tok2"); err != nil {
				t.Fatal(err)
			}
			if ok, err := s.MarkDomainVerifiedIfChallenge(ctx, wildcard, "tok2"); err != nil || !ok {
				t.Fatalf("re-verify wildcard = %v, %v", ok, err)
			}
			if v := admit("n0."+suffix, later, 1); v != state.TLSHostRegisteredDomainFull {
				t.Fatalf("re-created wildcard refilled its budget: %v", v)
			}
			if v := admit("c."+suffix, later.Add(time.Minute), 1); v != state.TLSHostRegisteredDomainFull {
				t.Fatalf("a host forgotten with its wildcard must count again: %v", v)
			}
			if v := admit("n0."+suffix, later.Add(window+time.Minute), 1); v != state.TLSHostAdmitted {
				t.Fatalf("budget after the re-created wildcard's window = %v", v)
			}
		})
	}
}

// TestCustomDomainTLSHostAdmissionSharedCaps pins the caps that span
// wildcards: sibling wildcards share their registered domain's budget, an
// owner's wildcards share the owner's (an organization's apps are one owner
// whoever created them), and every admission counts toward the fleet's.
func TestCustomDomainTLSHostAdmissionSharedCaps(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	admitter := func(t *testing.T, ctx context.Context, s tlsHostTestStore, tune func(*state.CustomDomainTLSAdmission)) func(wildcard, host string, at time.Time) state.CustomDomainTLSVerdict {
		return func(wildcard, host string, at time.Time) state.CustomDomainTLSVerdict {
			t.Helper()
			a := roomyTLSAdmission(wildcard, host, at)
			tune(&a)
			v, err := s.AdmitCustomDomainTLSHost(ctx, a)
			if err != nil {
				t.Fatalf("admit %s: %v", host, err)
			}
			return v
		}
	}
	for name, open := range tlsHostStores() {
		t.Run(name+"/registered domain", func(t *testing.T) {
			s, ctx := open(t)
			admit := admitter(t, ctx, s, func(a *state.CustomDomainTLSAdmission) { a.PerRegisteredDomain = 2 })
			reg := uuid.NewString()[:8] + ".co.uk"
			appID := tlsHostApp(t, ctx, s, "")
			verifiedTLSWildcard(t, ctx, s, "*.one."+reg, appID)
			verifiedTLSWildcard(t, ctx, s, "*.two."+reg, tlsHostApp(t, ctx, s, ""))
			verifiedTLSWildcard(t, ctx, s, "*.elsewhere-"+reg, appID)
			if admit("*.one."+reg, "x.one."+reg, now) != state.TLSHostAdmitted ||
				admit("*.two."+reg, "y.two."+reg, now) != state.TLSHostAdmitted {
				t.Fatal("hosts within the registered domain's budget were refused")
			}
			if v := admit("*.one."+reg, "z.one."+reg, now); v != state.TLSHostRegisteredDomainFull {
				t.Fatalf("sibling wildcard past the shared budget = %v", v)
			}
			if v := admit("*.elsewhere-"+reg, "z.elsewhere-"+reg, now); v != state.TLSHostAdmitted {
				t.Fatalf("another registered domain = %v", v)
			}
		})

		t.Run(name+"/owner", func(t *testing.T) {
			s, ctx := open(t)
			admit := admitter(t, ctx, s, func(a *state.CustomDomainTLSAdmission) { a.PerOwner = 2 })
			org, err := s.CreateOrg(ctx, state.Org{Slug: "tls-" + uuid.NewString()[:8], Name: "TLS", Plan: api.PlanFree})
			if err != nil {
				t.Fatal(err)
			}
			if err := s.UpdateOrgPlan(ctx, org.ID, api.PlanPro); err != nil {
				t.Fatal(err)
			}
			d1, d2 := uuid.NewString()[:8]+".example.net", uuid.NewString()[:8]+".example.org"
			verifiedTLSWildcard(t, ctx, s, "*."+d1, tlsHostApp(t, ctx, s, org.ID))
			verifiedTLSWildcard(t, ctx, s, "*."+d2, tlsHostApp(t, ctx, s, org.ID))
			other := uuid.NewString()[:8] + ".example.io"
			verifiedTLSWildcard(t, ctx, s, "*."+other, tlsHostApp(t, ctx, s, ""))
			if admit("*."+d1, "x."+d1, now) != state.TLSHostAdmitted ||
				admit("*."+d2, "x."+d2, now) != state.TLSHostAdmitted {
				t.Fatal("hosts within the owner's budget were refused")
			}
			if v := admit("*."+d2, "y."+d2, now); v != state.TLSHostOwnerFull {
				t.Fatalf("organization past its budget through another member's app = %v", v)
			}
			if v := admit("*."+other, "x."+other, now); v != state.TLSHostAdmitted {
				t.Fatalf("another owner = %v", v)
			}
		})

		t.Run(name+"/fleet", func(t *testing.T) {
			s, ctx := open(t)
			admit := admitter(t, ctx, s, func(a *state.CustomDomainTLSAdmission) { a.FleetLimit = 2 })
			d1, d2 := uuid.NewString()[:8]+".example.dev", uuid.NewString()[:8]+".example.app"
			verifiedTLSWildcard(t, ctx, s, "*."+d1, tlsHostApp(t, ctx, s, ""))
			verifiedTLSWildcard(t, ctx, s, "*."+d2, tlsHostApp(t, ctx, s, ""))
			if admit("*."+d1, "x."+d1, now) != state.TLSHostAdmitted ||
				admit("*."+d2, "x."+d2, now.Add(time.Hour)) != state.TLSHostAdmitted {
				t.Fatal("hosts within the fleet budget were refused")
			}
			if v := admit("*."+d1, "y."+d1, now.Add(2*time.Hour)); v != state.TLSHostFleetFull {
				t.Fatalf("host past the fleet budget = %v", v)
			}
			if v := admit("*."+d1, "y."+d1, now.Add(3*time.Hour+time.Minute)); v != state.TLSHostAdmitted {
				t.Fatalf("host once the first admission left the fleet window = %v", v)
			}
		})
	}
}

// TestCustomDomainTLSHostAdmissionLimitsAreCallerOwned pins the budgets the
// store is given: the numbers live in pkg/api/limits.go, under the ACME CA's
// limits.
func TestCustomDomainTLSHostAdmissionLimitsAreCallerOwned(t *testing.T) {
	if n := api.OnDemandTLSRegisteredDomainNewHostsPerWeek; n <= 0 || n >= 50 {
		t.Fatalf("OnDemandTLSRegisteredDomainNewHostsPerWeek = %d, want 1..49 (under Let's Encrypt's 50 certificates per registered domain per week)", n)
	}
	if n := api.OnDemandTLSFleetNewHostsPer3h; n <= 0 || n >= 300 {
		t.Fatalf("OnDemandTLSFleetNewHostsPer3h = %d, want 1..299 (under Let's Encrypt's 300 new orders per account per 3 hours)", n)
	}
	if api.OnDemandTLSOwnerNewHostsPerWeek < api.OnDemandTLSRegisteredDomainNewHostsPerWeek {
		t.Fatalf("OnDemandTLSOwnerNewHostsPerWeek = %d is below one registered domain's budget (%d)",
			api.OnDemandTLSOwnerNewHostsPerWeek, api.OnDemandTLSRegisteredDomainNewHostsPerWeek)
	}
}
