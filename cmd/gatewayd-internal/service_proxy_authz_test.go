// adr: 168
package main

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestServiceProxyAuthorizer(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()

	// seedApp creates a fresh account per call, so caller and target are
	// built explicitly here to share one — otherwise the "same account"
	// case would be testing the cross-account path by accident.
	acct, err := store.CreateAccount(ctx, "authz@local", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	newApp := func(accountID, slug string) state.App {
		t.Helper()
		app, err := store.CreateApp(ctx, state.App{
			AccountID: accountID, Slug: slug,
			Type: state.AppTypeApp, RAMMB: 128, Status: state.AppActive,
		})
		if err != nil {
			t.Fatalf("CreateApp %q: %v", slug, err)
		}
		return app
	}
	caller := newApp(acct.ID, "authzcaller")
	target := newApp(acct.ID, "authztarget")

	// A second account, to stand in for a cross-tenant caller.
	otherAcct, err := store.CreateAccount(ctx, "other@local", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	outsider := newApp(otherAcct.ID, "authzoutsider")

	// A well-formed id that names nothing.
	const absentUUID = "00000000-0000-4000-8000-000000000000"

	tests := []struct {
		name    string
		caller  string
		target  string
		wantErr error
	}{
		{"same account is allowed", caller.ID, target.ID, nil},
		{"cross-account is denied", outsider.ID, target.ID, gateway.ErrServiceProxyDenied},
		{"absent caller is denied", absentUUID, target.ID, gateway.ErrServiceProxyDenied},
		{"absent target is denied", caller.ID, absentUUID, gateway.ErrServiceProxyDenied},
		// A malformed id can never name a row in a uuid column. Passing it
		// through makes Postgres raise 22P02, which the proxy could only
		// report as 503 "authorization unavailable" — blaming the platform
		// for a caller error, and paying a round-trip guaranteed to fail.
		{"malformed caller is denied, not a platform fault", "app-does-not-exist", target.ID, gateway.ErrServiceProxyDenied},
		{"malformed target is denied", caller.ID, "not-a-uuid", gateway.ErrServiceProxyDenied},
		{"empty caller is denied", "", target.ID, gateway.ErrServiceProxyDenied},
	}

	authorize := newServiceProxyAuthorizer(store)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := authorize(context.Background(), tc.caller, tc.target)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("authorize = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("authorize = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

// A genuine store failure must stay a 503 — it is the one case that really is
// a platform fault, and collapsing it into "denied" would hide an outage
// behind what looks like an authorization decision.
func TestServiceProxyAuthorizerSurfacesStoreFailure(t *testing.T) {
	boom := errors.New("connection refused")
	authorize := newServiceProxyAuthorizer(failingAppStore{Store: state.NewMemStore(), err: boom})

	err := authorize(context.Background(), "00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000002")
	if errors.Is(err, gateway.ErrServiceProxyDenied) {
		t.Fatal("store failure was reported as a denial; an outage would look like an authz decision")
	}
	if !errors.Is(err, boom) {
		t.Fatalf("authorize = %v, want it to wrap %v", err, boom)
	}
}

type failingAppStore struct {
	state.Store
	err error
}

func (f failingAppStore) AppByID(context.Context, string) (state.App, error) {
	return state.App{}, f.err
}
