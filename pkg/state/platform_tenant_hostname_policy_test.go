package state_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemPlatformTenantHostnamePolicyIsTenantScopedAndIdempotent(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "hostname-policy@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	tenant, _, err := store.CreatePlatformTenant(ctx, account.ID, "customer", "Customer", 250)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := store.GetPlatformTenantHostnamePolicy(ctx, account.ID, tenant.ID)
	if err != nil || initial.MaxHostnames != 0 || len(initial.AllowedSuffixes) != 0 {
		t.Fatalf("default policy=%+v err=%v", initial, err)
	}
	wantSuffixes := []string{"customers.example.com", "vanity.example.net"}
	first, err := store.SetPlatformTenantHostnamePolicy(ctx, account.ID, tenant.ID, wantSuffixes, 12)
	if err != nil {
		t.Fatal(err)
	}
	if first.MaxHostnames != 12 || !reflect.DeepEqual(first.AllowedSuffixes, wantSuffixes) || first.UpdatedAt.IsZero() {
		t.Fatalf("configured policy=%+v", first)
	}
	replay, err := store.SetPlatformTenantHostnamePolicy(ctx, account.ID, tenant.ID, wantSuffixes, 12)
	if err != nil || !replay.UpdatedAt.Equal(first.UpdatedAt) {
		t.Fatalf("idempotent policy replay=%+v err=%v", replay, err)
	}
	if _, err := store.SetPlatformTenantHostnamePolicy(ctx, account.ID, tenant.ID, nil, 0); err != nil {
		t.Fatalf("disable policy: %v", err)
	}
	foreign, err := store.CreateAccount(ctx, "hostname-policy-foreign@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetPlatformTenantHostnamePolicy(ctx, foreign.ID, tenant.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-account read error=%v", err)
	}
	if _, err := store.SetPlatformTenantHostnamePolicy(ctx, foreign.ID, tenant.ID, wantSuffixes, 1); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-account write error=%v", err)
	}
}

func TestPlatformTenantHostnamePolicyRejectsInvalidInput(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "hostname-policy-invalid@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	tenant, _, err := store.CreatePlatformTenant(ctx, account.ID, "customer", "Customer", 250)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		suffixes []string
		limit    int
	}{
		{name: "uppercase is not canonical", suffixes: []string{"Customers.example.com"}, limit: 2},
		{name: "duplicate suffix", suffixes: []string{"a.example.com", "a.example.com"}, limit: 2},
		{name: "wildcard", suffixes: []string{"*.example.com"}, limit: 2},
		{name: "suffixes require a limit", suffixes: []string{"example.com"}, limit: 0},
		{name: "limit requires suffixes", suffixes: []string{}, limit: 1},
		{name: "limit too large", suffixes: []string{"example.com"}, limit: state.MaxPlatformTenantDelegatedHosts + 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := store.SetPlatformTenantHostnamePolicy(ctx, account.ID, tenant.ID, tc.suffixes, tc.limit); !errors.Is(err, state.ErrInvalidArgument) {
				t.Fatalf("SetPlatformTenantHostnamePolicy error=%v, want ErrInvalidArgument", err)
			}
		})
	}
}

func TestPgPlatformTenantHostnamePolicyRoundTrip(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	accountID, _ := seedConsumerKeyAccountApp(t, ctx, store)
	tenant, _, err := store.CreatePlatformTenant(ctx, accountID, "hostname-policy", "Hostname policy", 250)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := store.GetPlatformTenantHostnamePolicy(ctx, accountID, tenant.ID)
	if err != nil || initial.TenantID != tenant.ID || len(initial.AllowedSuffixes) != 0 || initial.MaxHostnames != 0 {
		t.Fatalf("default policy=%+v err=%v", initial, err)
	}
	suffixes := []string{"customers.example.com", "vanity.example.net"}
	policy, err := store.SetPlatformTenantHostnamePolicy(ctx, accountID, tenant.ID, suffixes, 9)
	if err != nil {
		t.Fatal(err)
	}
	if policy.MaxHostnames != 9 || !reflect.DeepEqual(policy.AllowedSuffixes, suffixes) || policy.UpdatedAt.IsZero() {
		t.Fatalf("stored policy=%+v", policy)
	}
	replay, err := store.SetPlatformTenantHostnamePolicy(ctx, accountID, tenant.ID, suffixes, 9)
	if err != nil || !replay.UpdatedAt.Equal(policy.UpdatedAt) {
		t.Fatalf("idempotent policy replay=%+v err=%v", replay, err)
	}
	disabled, err := store.SetPlatformTenantHostnamePolicy(ctx, accountID, tenant.ID, nil, 0)
	if err != nil || len(disabled.AllowedSuffixes) != 0 || disabled.MaxHostnames != 0 {
		t.Fatalf("disabled policy=%+v err=%v", disabled, err)
	}
}
