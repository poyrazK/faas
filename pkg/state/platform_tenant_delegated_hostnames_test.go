package state

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func delegatedHostnameFixture(t *testing.T, cap int) (*MemStore, context.Context, Account, PlatformTenant, TenantSurface, TenantSurface, api.Limits) {
	t.Helper()
	m, ctx, account, app, limits := tenantSurfaceFixture(t)
	tenant, _, err := m.CreatePlatformTenant(ctx, account.ID, "delegated-hostnames", "Delegated hostnames", 250)
	if err != nil {
		t.Fatal(err)
	}
	first, err := m.CreateTenantSurfaceIfUnderQuota(ctx, CreateTenantSurfaceParams{AccountID: account.ID, AppID: app.ID, Name: "first"}, limits)
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.CreateTenantSurfaceIfUnderQuota(ctx, CreateTenantSurfaceParams{AccountID: account.ID, AppID: app.ID, Name: "second"}, limits)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.LinkPlatformTenantSurface(ctx, account.ID, tenant.ID, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.LinkPlatformTenantSurface(ctx, account.ID, tenant.ID, second.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetPlatformTenantHostnamePolicy(ctx, account.ID, tenant.ID, []string{"customers.example.com"}, cap); err != nil {
		t.Fatal(err)
	}
	return m, ctx, account, tenant, first, second, limits
}

func TestMemPlatformTenantDelegatedHostnamePolicyAndReplay(t *testing.T) {
	m, ctx, account, tenant, first, second, limits := delegatedHostnameFixture(t, 2)
	create := func(surfaceID, hostname, challenge string) (PlatformTenantDelegatedHostnameResult, error) {
		return m.CreatePlatformTenantDelegatedHostname(ctx, account.ID, tenant.ID, surfaceID, hostname, challenge, limits)
	}
	created, err := create(first.ID, " SHOP.Customers.Example.COM ", "challenge-1")
	if err != nil || created.Action != "created" || created.Hostname.Hostname != "shop.customers.example.com" || created.Hostname.ChallengeToken != "challenge-1" {
		t.Fatalf("create=%+v err=%v", created, err)
	}
	replay, err := create(first.ID, "shop.customers.example.com", "challenge-2")
	if err != nil || replay.Action != "unchanged" || replay.Hostname.ID != created.Hostname.ID || replay.Hostname.ChallengeToken != "challenge-1" {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
	if _, err := create(second.ID, "other.customers.example.com", "challenge-3"); err != nil {
		t.Fatalf("second linked surface: %v", err)
	}
	if _, err := create(first.ID, "notcustomers.example.com", "challenge-4"); !errors.Is(err, ErrPlatformTenantHostnameSuffixNotAllowed) {
		t.Fatalf("suffix boundary error=%v", err)
	}
	if _, err := m.SetPlatformTenantHostnamePolicy(ctx, account.ID, tenant.ID, nil, 0); err != nil {
		t.Fatal(err)
	}
	replay, err = create(first.ID, "shop.customers.example.com", "challenge-5")
	if err != nil || replay.Action != "unchanged" || replay.Hostname.ChallengeToken != "challenge-1" {
		t.Fatalf("replay after disabling policy=%+v err=%v", replay, err)
	}
	if _, err := create(first.ID, "new.customers.example.com", "challenge-6"); !errors.Is(err, ErrPlatformTenantHostnameDelegationDisabled) {
		t.Fatalf("disabled policy error=%v", err)
	}
}

func TestMemPlatformTenantDelegatedHostnameCapAndIsolation(t *testing.T) {
	m, ctx, account, tenant, first, second, limits := delegatedHostnameFixture(t, 1)
	create := func(surfaceID, hostname string) (PlatformTenantDelegatedHostnameResult, error) {
		return m.CreatePlatformTenantDelegatedHostname(ctx, account.ID, tenant.ID, surfaceID, hostname, "challenge", limits)
	}
	if _, err := create(first.ID, "one.customers.example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := create(second.ID, "two.customers.example.com"); err == nil {
		t.Fatal("hostname above tenant-wide cap was accepted")
	} else {
		var quota *PlatformTenantDelegatedHostnameQuotaError
		if !errors.As(err, &quota) || quota.Limit != 1 || quota.Observed != 1 {
			t.Fatalf("tenant cap error=%v", err)
		}
	}
	other, _, err := m.CreatePlatformTenant(ctx, account.ID, "other", "Other", 250)
	if err != nil {
		t.Fatal(err)
	}
	otherSurface, err := m.CreateTenantSurfaceIfUnderQuota(ctx, CreateTenantSurfaceParams{AccountID: account.ID, AppID: first.AppID, Name: "other"}, limits)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.LinkPlatformTenantSurface(ctx, account.ID, other.ID, otherSurface.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreatePlatformTenantDelegatedHostname(ctx, account.ID, other.ID, first.ID,
		"other.customers.example.com", "challenge", limits); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant surface error=%v", err)
	}
	if _, err := m.CreatePlatformTenantDelegatedHostname(ctx, account.ID, other.ID, otherSurface.ID,
		"unconfigured.customers.example.com", "challenge", limits); !errors.Is(err, ErrPlatformTenantHostnameDelegationDisabled) {
		t.Fatalf("missing policy error=%v", err)
	}
	if _, err := m.SetPlatformTenantStatus(ctx, account.ID, tenant.ID, PlatformTenantSuspended); err != nil {
		t.Fatal(err)
	}
	if _, err := create(second.ID, "suspended.customers.example.com"); !errors.Is(err, ErrPlatformTenantSuspended) {
		t.Fatalf("suspended tenant error=%v", err)
	}
}
