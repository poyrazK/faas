package state_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type tlsHostTestStore interface {
	state.CustomDomainTLSHostStore
	CreateCustomDomain(ctx context.Context, domain, appID, token string) (state.CustomDomain, error)
	MarkDomainVerifiedIfChallenge(ctx context.Context, domain, token string) (bool, error)
	DeleteCustomDomain(ctx context.Context, domain string) error
}

// TestCustomDomainTLSHostAdmission pins ADR-520's wildcard issuance budget on
// both stores: known hosts are free, new hosts are capped per rolling window,
// unverified or deleted wildcards admit nothing.
func TestCustomDomainTLSHostAdmission(t *testing.T) {
	stores := map[string]func(t *testing.T) (tlsHostTestStore, context.Context, string){
		"mem": func(t *testing.T) (tlsHostTestStore, context.Context, string) {
			return state.NewMemStore(), context.Background(), uuid.NewString()
		},
		"pg": func(t *testing.T) (tlsHostTestStore, context.Context, string) {
			s, ctx, _, app, _ := pgCoverageFixture(t)
			return s, ctx, app.ID
		},
	}
	for name, open := range stores {
		t.Run(name, func(t *testing.T) {
			s, ctx, appID := open(t)
			suffix := uuid.NewString()[:8] + ".example.com"
			wildcard := "*." + suffix
			if _, err := s.CreateCustomDomain(ctx, wildcard, appID, "tok"); err != nil {
				t.Fatal(err)
			}
			now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
			const window = 7 * 24 * time.Hour
			admit := func(host string, at time.Time, limit int) bool {
				t.Helper()
				ok, err := s.AdmitCustomDomainTLSHost(ctx, wildcard, host, at, window, limit)
				if err != nil {
					t.Fatalf("admit %s: %v", host, err)
				}
				return ok
			}

			if admit("a."+suffix, now, 2) {
				t.Fatal("unverified wildcard admitted a host")
			}
			if ok, err := s.MarkDomainVerifiedIfChallenge(ctx, wildcard, "tok"); err != nil || !ok {
				t.Fatalf("verify wildcard = %v, %v", ok, err)
			}
			if !admit("a."+suffix, now, 2) || !admit("b."+suffix, now, 2) {
				t.Fatal("hosts within the budget were refused")
			}
			if admit("c."+suffix, now, 2) {
				t.Fatal("third new host admitted past a budget of 2")
			}
			if !admit("A."+suffix, now.Add(time.Hour), 2) {
				t.Fatal("a known host must be re-admitted without budget (reload/renewal)")
			}
			if !admit("c."+suffix, now.Add(window+time.Minute), 2) {
				t.Fatal("budget did not refill after the window")
			}
			if admit("d."+suffix, now, 0) {
				t.Fatal("zero budget admitted a new host")
			}

			if _, err := s.AdmitCustomDomainTLSHost(ctx, wildcard, suffix, now, window, 2); !errors.Is(err, state.ErrInvalidArgument) {
				t.Fatalf("apex below its own wildcard = %v, want ErrInvalidArgument", err)
			}
			if _, err := s.AdmitCustomDomainTLSHost(ctx, wildcard, "x.other.com", now, window, 2); !errors.Is(err, state.ErrInvalidArgument) {
				t.Fatalf("unrelated host = %v, want ErrInvalidArgument", err)
			}

			if err := s.DeleteCustomDomain(ctx, wildcard); err != nil {
				t.Fatal(err)
			}
			if _, err := s.AdmitCustomDomainTLSHost(ctx, wildcard, "a."+suffix, now, window, 2); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("deleted wildcard = %v, want ErrNotFound", err)
			}
			// Re-creating the wildcard starts from an empty admission set.
			if _, err := s.CreateCustomDomain(ctx, wildcard, appID, "tok2"); err != nil {
				t.Fatal(err)
			}
			if ok, err := s.MarkDomainVerifiedIfChallenge(ctx, wildcard, "tok2"); err != nil || !ok {
				t.Fatalf("re-verify wildcard = %v, %v", ok, err)
			}
			for i := 0; i < 2; i++ {
				if !admit(fmt.Sprintf("n%d.%s", i, suffix), now, 2) {
					t.Fatalf("host %d refused after the wildcard was re-created", i)
				}
			}
		})
	}
}

// TestCustomDomainTLSHostAdmissionLimitIsCallerOwned pins the budget the store
// is given: the number lives in pkg/api/limits.go, under the ACME CA limit.
func TestCustomDomainTLSHostAdmissionLimitIsCallerOwned(t *testing.T) {
	if api.OnDemandTLSWildcardNewHostsPerWeek <= 0 || api.OnDemandTLSWildcardNewHostsPerWeek >= 50 {
		t.Fatalf("OnDemandTLSWildcardNewHostsPerWeek = %d, want 1..49 (under Let's Encrypt's 50 certificates per registered domain per week)",
			api.OnDemandTLSWildcardNewHostsPerWeek)
	}
}
