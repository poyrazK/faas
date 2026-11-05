package state_test

// adr: 520

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// wildcardAllowlistEndsWithItsClaim: a squatter claims a wildcard nobody has
// verified and lists one decoy name. The zone's owner takes the claim over by
// DNS proof. The squatter's list must not decide which of the owner's names get
// certificates, so the new claim starts with no allowlist, as a new row would.
func wildcardAllowlistEndsWithItsClaim(t *testing.T, store state.Store) {
	t.Helper()
	ctx, suffix := t.Context(), uuid.NewString()[:8]
	app := func(name string) state.App {
		t.Helper()
		account, err := store.CreateAccount(ctx, name+"-"+suffix+"@example.test", api.PlanScale)
		if err != nil {
			t.Fatal(err)
		}
		created, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: name + "-" + suffix,
			Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 60})
		if err != nil {
			t.Fatal(err)
		}
		return created
	}
	squatter, owner := app("squatter"), app("owner")
	zone := "reclaim-" + suffix + ".example.test"
	wildcard := "*." + zone
	claims := store.(interface {
		CreateCustomDomainIfUnderQuota(ctx context.Context, domain, appID, token string, appLimit, accountLimit int) (state.CustomDomain, error)
	})
	stale, err := claims.CreateCustomDomainIfUnderQuota(ctx, wildcard, squatter.ID, "squatter-token", 10, 10)
	if err != nil {
		t.Fatal(err)
	}
	hosts := store.(interface {
		state.CustomDomainTLSAllowedHostStore
		state.CustomDomainTLSAllowedHostReader
	})
	if err := hosts.SetCustomDomainTLSAllowedHosts(ctx, wildcard, []string{"decoy"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.(state.CustomDomainClaimTakeover).TakeOverCustomDomainClaim(ctx, wildcard, owner.ID, "", "owner-token", 10, 10); err != nil {
		t.Fatal(err)
	}
	if names, err := hosts.CustomDomainTLSAllowedHosts(ctx, wildcard); err != nil || len(names) != 0 {
		t.Fatalf("allowlist after the takeover = %v, %v; want none of the squatter's names", names, err)
	}
	if allowed, err := hosts.CustomDomainTLSHostAllowed(ctx, wildcard, "api."+zone); err != nil || !allowed {
		t.Fatalf("api.%s may hold a certificate = %v, %v; want the owner's names unrestricted", zone, allowed, err)
	}
	current, err := store.DomainByName(ctx, wildcard)
	if err != nil {
		t.Fatal(err)
	}
	if err := hosts.SetCustomDomainTLSAllowedHostsForClaim(ctx, current, []string{"api"}); err != nil {
		t.Fatal(err)
	}
	// Both PUT and DELETE from an already-authorized old request must fail.
	for _, names := range [][]string{{"decoy"}, nil} {
		if err := hosts.SetCustomDomainTLSAllowedHostsForClaim(ctx, stale, names); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("stale claim mutation = %v, want ErrNotFound", err)
		}
		if names, err := hosts.CustomDomainTLSAllowedHosts(ctx, wildcard); err != nil || len(names) != 1 || names[0] != "api" {
			t.Fatalf("stale claim changed the new owner's allowlist: %v, %v", names, err)
		}
	}
}

func TestMemWildcardAllowlistEndsWithItsClaim(t *testing.T) {
	wildcardAllowlistEndsWithItsClaim(t, state.NewMemStore())
}
