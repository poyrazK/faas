package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type tenantRefStoreFunc func(context.Context, string, string) (state.PlatformTenant, error)

func (f tenantRefStoreFunc) ResolvePlatformTenantExternalRef(ctx context.Context, accountID, ref string) (state.PlatformTenant, error) {
	return f(ctx, accountID, ref)
}

// A JWT customer claim's tenant is read once per cache TTL, not per request;
// a suspension shows within the TTL; and a store outage serves the last
// answer for the grace instead of refusing (bug hunt H29).
func TestPlatformTenantJWTResolverCaches(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	var reads int
	status := state.PlatformTenantActive
	var storeErr error
	r := newCachedPlatformTenantExternalRefResolver(tenantRefStoreFunc(func(_ context.Context, _, ref string) (state.PlatformTenant, error) {
		reads++
		if storeErr != nil {
			return state.PlatformTenant{}, storeErr
		}
		if ref == "unknown" {
			return state.PlatformTenant{}, state.ErrNotFound
		}
		return state.PlatformTenant{ID: "tenant-1", Status: status}, nil
	}), func() time.Time { return now })
	ctx := context.Background()
	for range 10 {
		identity, found, err := r.ResolvePlatformTenantExternalRef(ctx, "acct", "cust-1")
		if err != nil || !found || !identity.Active || identity.TenantID != "tenant-1" {
			t.Fatalf("resolve = %+v %v %v", identity, found, err)
		}
		if _, found, err := r.ResolvePlatformTenantExternalRef(ctx, "acct", "unknown"); err != nil || found {
			t.Fatalf("unknown claim = %v %v", found, err)
		}
	}
	if reads != 2 {
		t.Fatalf("store reads = %d, want 2", reads)
	}
	status = state.PlatformTenantSuspended
	now = now.Add(api.PlatformTenantJWTCacheTTL)
	if identity, _, _ := r.ResolvePlatformTenantExternalRef(ctx, "acct", "cust-1"); identity.Active {
		t.Fatal("a suspension was not seen after the TTL")
	}
	storeErr = errors.New("store unavailable")
	now = now.Add(api.PlatformTenantJWTCacheTTL)
	if _, found, err := r.ResolvePlatformTenantExternalRef(ctx, "acct", "cust-1"); err != nil || !found {
		t.Fatalf("outage inside the grace = %v %v, want the last answer", found, err)
	}
	now = now.Add(api.PlatformTenantJWTOutageGrace)
	if _, _, err := r.ResolvePlatformTenantExternalRef(ctx, "acct", "cust-1"); err == nil {
		t.Fatal("outage past the grace still answered from cache")
	}
}
