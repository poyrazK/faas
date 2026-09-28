package state_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgPlatformTenantDelegatedHostnamePolicyAndReplay(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	accountID, appID := seedConsumerKeyAccountApp(t, ctx, store)
	tenant, _, err := store.CreatePlatformTenant(ctx, accountID, "delegated-hostnames", "Delegated hostnames", 250)
	if err != nil {
		t.Fatal(err)
	}
	limits := api.Limits{TenantSurfacesAllowed: true, TenantSurfacesPerAccount: 10, TenantHostnamesPerSurface: 10}
	surface, err := store.CreateTenantSurfaceIfUnderQuota(ctx, state.CreateTenantSurfaceParams{
		AccountID: accountID, AppID: appID, Name: "customer",
	}, limits)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.LinkPlatformTenantSurface(ctx, accountID, tenant.ID, surface.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetPlatformTenantHostnamePolicy(ctx, accountID, tenant.ID, []string{"customers.example.com"}, 1); err != nil {
		t.Fatal(err)
	}
	create := func(hostname, challenge string) (state.PlatformTenantDelegatedHostnameResult, error) {
		return store.CreatePlatformTenantDelegatedHostname(ctx, accountID, tenant.ID, surface.ID, hostname, challenge, limits)
	}
	first, err := create("shop.customers.example.com", "challenge-1")
	if err != nil || first.Action != "created" || first.Hostname.ChallengeToken != "challenge-1" {
		t.Fatalf("create=%+v err=%v", first, err)
	}
	replay, err := create("shop.customers.example.com", "challenge-2")
	if err != nil || replay.Action != "unchanged" || replay.Hostname.ID != first.Hostname.ID || replay.Hostname.ChallengeToken != "challenge-1" {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
	if _, err := create("outside.example.net", "challenge-3"); !errors.Is(err, state.ErrPlatformTenantHostnameSuffixNotAllowed) {
		t.Fatalf("suffix policy error=%v", err)
	}
	if _, err := store.SetPlatformTenantHostnamePolicy(ctx, accountID, tenant.ID, nil, 0); err != nil {
		t.Fatal(err)
	}
	replay, err = create("shop.customers.example.com", "challenge-4")
	if err != nil || replay.Action != "unchanged" || replay.Hostname.ChallengeToken != "challenge-1" {
		t.Fatalf("replay after disabling policy=%+v err=%v", replay, err)
	}
	if _, err := create("new.customers.example.com", "challenge-5"); !errors.Is(err, state.ErrPlatformTenantHostnameDelegationDisabled) {
		t.Fatalf("disabled policy error=%v", err)
	}
	other, _, err := store.CreatePlatformTenant(ctx, accountID, "other-delegated-hostnames", "Other", 250)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreatePlatformTenantDelegatedHostname(ctx, accountID, other.ID, surface.ID,
		"cross-tenant.customers.example.com", "challenge", limits); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-tenant surface without policy error=%v", err)
	}
}

func TestPgPlatformTenantDelegatedHostnameSerializesTenantCap(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	accountID, appID := seedConsumerKeyAccountApp(t, ctx, store)
	tenant, _, err := store.CreatePlatformTenant(ctx, accountID, "delegated-hostname-cap", "Delegated hostname cap", 250)
	if err != nil {
		t.Fatal(err)
	}
	limits := api.Limits{TenantSurfacesAllowed: true, TenantSurfacesPerAccount: 10, TenantHostnamesPerSurface: 10}
	var surfaces []state.TenantSurface
	for _, name := range []string{"first", "second"} {
		surface, err := store.CreateTenantSurfaceIfUnderQuota(ctx, state.CreateTenantSurfaceParams{
			AccountID: accountID, AppID: appID, Name: name,
		}, limits)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.LinkPlatformTenantSurface(ctx, accountID, tenant.ID, surface.ID); err != nil {
			t.Fatal(err)
		}
		surfaces = append(surfaces, surface)
	}
	if _, err := store.SetPlatformTenantHostnamePolicy(ctx, accountID, tenant.ID, []string{"customers.example.com"}, 1); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	accepted, rejected := 0, 0
	for i, surface := range surfaces {
		wg.Add(1)
		go func(i int, surface state.TenantSurface) {
			defer wg.Done()
			_, err := store.CreatePlatformTenantDelegatedHostname(ctx, accountID, tenant.ID, surface.ID,
				[]string{"one.customers.example.com", "two.customers.example.com"}[i], "challenge", limits)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				accepted++
			} else {
				var quota *state.PlatformTenantDelegatedHostnameQuotaError
				if errors.As(err, &quota) {
					rejected++
				} else {
					t.Errorf("delegated create error=%v", err)
				}
			}
		}(i, surface)
	}
	wg.Wait()
	if accepted != 1 || rejected != 1 {
		t.Fatalf("accepted=%d rejected=%d, want 1 each", accepted, rejected)
	}
}
