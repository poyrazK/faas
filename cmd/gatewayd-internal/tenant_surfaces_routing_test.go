// tenant_surfaces_routing_test.go — unit tests for the pgRouter
// tenant-surface branch (issue #879 / ADR-100 PR-B). The full
// routing-only e2e is deferred to PR-C because cmd/e2e currently
// has no gatewayd-internal boot harness; routing-layer coverage
// at the routing-layer unit level is the right granularity for
// PR-B and matches the existing TestPgRouter_* family in
// backend_test.go.
//
// These tests deliberately use the MemStore path so they run
// under `make test` (no Postgres required). The PgStore
// counterpart is in pkg/state/pgstore_tenant_surface_test.go.
package main

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// seedSurface creates an account + app + tenant surface + a
// single verified hostname in the surface. Returns the app so
// tests can assert the routing outcome.
func seedSurface(t *testing.T, store state.Store, slug, hostname string, status state.SurfaceStatus) (app state.App) {
	t.Helper()
	ctx := context.Background()
	app = seedApp(t, store, slug, api.PlanPro)
	lim := api.Limits{
		TenantSurfacesPerAccount:  5,
		TenantHostnamesPerSurface: 10,
		TenantSurfacesAllowed:     true,
	}
	surf, err := store.CreateTenantSurfaceIfUnderQuota(ctx, state.CreateTenantSurfaceParams{
		AccountID: app.AccountID,
		AppID:     app.ID,
		Name:      "test-surface",
	}, lim)
	if err != nil {
		t.Fatalf("CreateTenantSurfaceIfUnderQuota: %v", err)
	}
	if _, err := store.CreateTenantHostnameIfUnderQuota(ctx, state.CreateTenantHostnameParams{
		SurfaceID:      surf.ID,
		Hostname:       hostname,
		ChallengeToken: "tok",
	}, lim); err != nil {
		t.Fatalf("CreateTenantHostnameIfUnderQuota: %v", err)
	}
	// PR-B review finding #1: the routing branch gates on
	// hostname.Verified() (mirrors dom.Verified() at the legacy
	// custom_domains branch). Mark the seed hostname verified by
	// default; tests that want the unverified path skip the
	// MarkVerified call via the seedSurfaceUnverified variant
	// (or don't call this helper at all).
	if err := store.MarkTenantHostnameVerified(ctx, hostname); err != nil {
		t.Fatalf("MarkTenantHostnameVerified: %v", err)
	}
	if status != state.SurfaceStatusPending {
		if err := store.UpdateTenantSurfaceStatus(ctx, surf.ID, status); err != nil {
			t.Fatalf("UpdateTenantSurfaceStatus: %v", err)
		}
	}
	return app
}

// TestPgRouter_TenantSurfaceRouted pins the happy path: feature
// flag on, well-formed customer-zone host with an active surface
// must route to the surface's app. This is the load-bearing
// behaviour PR-B ships.
func TestPgRouter_TenantSurfaceRouted(t *testing.T) {
	t.Setenv("FAAS_TENANT_SURFACES_ENABLED", "true")
	store := state.NewMemStore()
	app := seedSurface(t, store, "blog", "api.customer-a.com", state.SurfaceStatusActive)
	r := pgRouter{store: store, appsSuffix: ".apps.gregale.dev"}

	got, ok, err := r.ResolveHost(context.Background(), "api.customer-a.com")
	if err != nil || !ok {
		t.Fatalf("ResolveHost ok=%v err=%v, want true/nil", ok, err)
	}
	if got.ID != app.ID {
		t.Errorf("resolved = %+v, want id=%s", got, app.ID)
	}
}

// adr: 226 — suspending a platform customer must not fall through to an
// older custom-domain row with the same hostname.
func TestPgRouter_PlatformTenantSuspensionClaimsHostname(t *testing.T) {
	t.Setenv("FAAS_TENANT_SURFACES_ENABLED", "true")
	store := state.NewMemStore()
	ctx := context.Background()
	app := seedSurface(t, store, "platform-surf", "api.customer-platform.com", state.SurfaceStatusActive)
	surface, err := store.GetTenantSurfaceByName(ctx, app.AccountID, "test-surface")
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := store.CreateApp(ctx, state.App{AccountID: app.AccountID, Slug: "platform-legacy",
		Type: state.AppTypeApp, RAMMB: 128, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateCustomDomain(ctx, "api.customer-platform.com", legacy.ID, "tok"); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDomainVerified(ctx, "api.customer-platform.com"); err != nil {
		t.Fatal(err)
	}
	tenant, _, err := store.CreatePlatformTenant(ctx, app.AccountID, "customer-platform", "Customer Platform", 250)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.LinkPlatformTenantSurface(ctx, app.AccountID, tenant.ID, surface.ID); err != nil {
		t.Fatal(err)
	}
	r := pgRouter{store: store, appsSuffix: ".apps.gregale.dev"}
	if got, ok, err := r.ResolveHost(ctx, "api.customer-platform.com"); err != nil || !ok || got.ID != app.ID {
		t.Fatalf("active tenant route = %+v, %v, %v", got, ok, err)
	}
	if _, err := store.SetPlatformTenantStatus(ctx, app.AccountID, tenant.ID, state.PlatformTenantSuspended); err != nil {
		t.Fatal(err)
	}
	if got, ok, err := r.ResolveHost(ctx, "api.customer-platform.com"); err != nil || ok {
		t.Fatalf("suspended tenant route = %+v, %v, %v; want 404 without legacy fallback", got, ok, err)
	}
}

// TestPgRouter_TenantSurfaceFlagOff confirms the dark-launch
// contract: with the flag off, the routing branch is a no-op
// and a surface-bearing host falls through to legacy (which
// 404s because there is no custom_domains row). The point is
// that the surface row is NOT consulted when the flag is off,
// so a stale surface schema does not affect production behaviour.
func TestPgRouter_TenantSurfaceFlagOff(t *testing.T) {
	t.Setenv("FAAS_TENANT_SURFACES_ENABLED", "false")
	store := state.NewMemStore()
	_ = seedSurface(t, store, "blog", "api.customer-a.com", state.SurfaceStatusActive)
	r := pgRouter{store: store, appsSuffix: ".apps.gregale.dev"}

	if _, ok, _ := r.ResolveHost(context.Background(), "api.customer-a.com"); ok {
		t.Fatal("surface routed while feature flag is off")
	}
}

// TestPgRouter_TenantSurfaceSuspendedFallsThrough: a surface
// exists, but the customer has suspended it (a customer-visible
// state). The routing branch must route-around so a legacy
// custom_domains row that pre-dates the surface remains the
// source of truth. This pins the soft-delete + suspend semantics
// from pkg/state/tenant_surface.go.
func TestPgRouter_TenantSurfaceSuspendedFallsThrough(t *testing.T) {
	t.Setenv("FAAS_TENANT_SURFACES_ENABLED", "true")
	store := state.NewMemStore()
	_ = seedSurface(t, store, "blog", "api.customer-a.com", state.SurfaceStatusSuspended)
	r := pgRouter{store: store, appsSuffix: ".apps.gregale.dev"}

	// No legacy domain row either → 404, NOT a route to the
	// suspended surface.
	if _, ok, _ := r.ResolveHost(context.Background(), "api.customer-a.com"); ok {
		t.Fatal("suspended surface was still routed")
	}
}

// TestPgRouter_TenantSurfaceUnknownHostFallsThrough: a
// well-formed customer-zone host with no surface row must
// fall through to the legacy custom_domains path. This is
// the dispatch arm correctness test — the branch in
// ResolveHost must NOT 404 prematurely when only the surface
// lookup misses.
func TestPgRouter_TenantSurfaceUnknownHostFallsThrough(t *testing.T) {
	t.Setenv("FAAS_TENANT_SURFACES_ENABLED", "true")
	store := state.NewMemStore()
	// App exists; no surface exists for api.unknown.com.
	_ = seedApp(t, store, "blog", api.PlanPro)
	r := pgRouter{store: store, appsSuffix: ".apps.gregale.dev"}

	if _, ok, _ := r.ResolveHost(context.Background(), "api.unknown.com"); ok {
		t.Fatal("unknown host routed")
	}
}

// TestPgRouter_TenantSurfaceNonSurfaceHostIgnored: a host that
// is not even well-formed as a surface host (e.g. "localhost"
// with no apex) must skip the surface branch entirely. The
// skip is by parser rejection, not by store lookup. This pins
// the parser→routing hand-off.
func TestPgRouter_TenantSurfaceNonSurfaceHostIgnored(t *testing.T) {
	t.Setenv("FAAS_TENANT_SURFACES_ENABLED", "true")
	store := state.NewMemStore()
	_ = seedApp(t, store, "blog", api.PlanPro)
	r := pgRouter{store: store, appsSuffix: ".apps.gregale.dev"}

	// "localhost" is a single label → SurfaceParse returns ok=false.
	// The routing branch is skipped; the legacy path is also a
	// miss (no custom_domains row). 404.
	if _, ok, _ := r.ResolveHost(context.Background(), "localhost"); ok {
		t.Fatal("non-surface host routed")
	}
}

// TestPgRouter_TenantSurfaceOrderingWithPreview: a PR-preview
// host (pr-{N}.slug.apps.zone) is shape-disjoint with a
// customer-zone host. The surface branch must not consume a
// preview host. PR #872's preview routing must continue to work
// after PR-B. This test pins the parser-rejects-multi-label-prefix
// behaviour for hosts that look like neither.
func TestPgRouter_TenantSurfaceOrderingWithPreview(t *testing.T) {
	t.Setenv("FAAS_TENANT_SURFACES_ENABLED", "true")
	store := state.NewMemStore()
	_ = seedApp(t, store, "blog", api.PlanPro)
	r := pgRouter{store: store, appsSuffix: ".apps.gregale.dev"}

	// "pr-42.blog.apps.gregale.dev" is preview-shaped. Without
	// a preview app row, the surface branch must skip (host is
	// not a surface candidate) and the preview branch must 404.
	// The assertion pins that the surface branch did not
	// intercept.
	if _, ok, _ := r.ResolveHost(context.Background(), "pr-42.blog.apps.gregale.dev"); ok {
		t.Fatal("surface branch consumed a preview host")
	}
}

// TestPgRouter_TenantSurfaceUnverifiedHostnameFallsThrough pins
// the security gate added by PR-B review finding #1: a hostname
// row exists on an active surface, but the dns_poller has not yet
// verified the TXT challenge. The legacy custom_domains path
// already gates on dom.Verified(); the tenant-surface branch must
// mirror that gate (or pre-challenge hostnames would route,
// handing traffic to an attacker-controlled DNS that pointed at
// us mid-challenge). Surface branch falls through to legacy; with
// no custom_domains row, this is a clean 404.
func TestPgRouter_TenantSurfaceUnverifiedHostnameFallsThrough(t *testing.T) {
	t.Setenv("FAAS_TENANT_SURFACES_ENABLED", "true")
	store := state.NewMemStore()
	ctx := context.Background()
	app := seedApp(t, store, "blog", api.PlanPro)
	lim := api.Limits{
		TenantSurfacesPerAccount:  5,
		TenantHostnamesPerSurface: 10,
		TenantSurfacesAllowed:     true,
	}
	surf, err := store.CreateTenantSurfaceIfUnderQuota(ctx, state.CreateTenantSurfaceParams{
		AccountID: app.AccountID,
		AppID:     app.ID,
		Name:      "pre-challenge",
	}, lim)
	if err != nil {
		t.Fatalf("CreateTenantSurfaceIfUnderQuota: %v", err)
	}
	if err := store.UpdateTenantSurfaceStatus(ctx, surf.ID, state.SurfaceStatusActive); err != nil {
		t.Fatalf("UpdateTenantSurfaceStatus: %v", err)
	}
	// Insert hostname WITHOUT calling MarkTenantHostnameVerified —
	// this is the pre-challenge state the dns_poller hasn't yet
	// confirmed. The seedSurface helper verifies by default; this
	// test deliberately skips that step.
	if _, err := store.CreateTenantHostnameIfUnderQuota(ctx, state.CreateTenantHostnameParams{
		SurfaceID:      surf.ID,
		Hostname:       "api.customer-a.com",
		ChallengeToken: "tok",
	}, lim); err != nil {
		t.Fatalf("CreateTenantHostnameIfUnderQuota: %v", err)
	}
	r := pgRouter{store: store, appsSuffix: ".apps.gregale.dev"}

	if _, ok, _ := r.ResolveHost(context.Background(), "api.customer-a.com"); ok {
		t.Fatal("pre-challenge (unverified) hostname was routed — security gate bypassed")
	}
}

// TestPgRouter_TenantSurfaceCrossAccountMismatchFallsThrough pins
// the security gate added by PR-B review finding #8: an invariant
// violation in the store layer (or a future soft-delete+reassign
// race) leaves the surface→account and the app→account fields
// disagreeing. The routing branch must NOT trust surface.AppID
// alone — it must verify the accounts agree. With a mismatched
// pair, the routing branch returns ok=false (falls through to
// legacy) instead of routing to the wrong account's app.
func TestPgRouter_TenantSurfaceCrossAccountMismatchFallsThrough(t *testing.T) {
	t.Setenv("FAAS_TENANT_SURFACES_ENABLED", "true")
	store := state.NewMemStore()
	ctx := context.Background()
	// Account A owns the surface; account B owns the app the
	// surface points at (a hypothetical invariant violation).
	appA := seedApp(t, store, "blog", api.PlanPro)
	appB := seedApp(t, store, "shop", api.PlanPro)
	lim := api.Limits{
		TenantSurfacesPerAccount:  5,
		TenantHostnamesPerSurface: 10,
		TenantSurfacesAllowed:     true,
	}
	// Create the surface against account A's app, then mutate the
	// app_id field to point at account B's app — simulating the
	// invariant violation. The store layer doesn't prevent this
	// (the cross-account check is the routing layer's job).
	surf, err := store.CreateTenantSurfaceIfUnderQuota(ctx, state.CreateTenantSurfaceParams{
		AccountID: appA.AccountID,
		AppID:     appB.ID, // wrong account
		Name:      "mismatch",
	}, lim)
	if err != nil {
		t.Fatalf("CreateTenantSurfaceIfUnderQuota: %v", err)
	}
	if err := store.UpdateTenantSurfaceStatus(ctx, surf.ID, state.SurfaceStatusActive); err != nil {
		t.Fatalf("UpdateTenantSurfaceStatus: %v", err)
	}
	if _, err := store.CreateTenantHostnameIfUnderQuota(ctx, state.CreateTenantHostnameParams{
		SurfaceID:      surf.ID,
		Hostname:       "api.customer-a.com",
		ChallengeToken: "tok",
	}, lim); err != nil {
		t.Fatalf("CreateTenantHostnameIfUnderQuota: %v", err)
	}
	if err := store.MarkTenantHostnameVerified(ctx, "api.customer-a.com"); err != nil {
		t.Fatalf("MarkTenantHostnameVerified: %v", err)
	}
	r := pgRouter{store: store, appsSuffix: ".apps.gregale.dev"}

	if _, ok, _ := r.ResolveHost(context.Background(), "api.customer-a.com"); ok {
		t.Fatal("cross-account mismatch was routed — would deliver account A's traffic to account B's app")
	}
}

// TestPgRouter_TenantSurfacePrecedenceOverCustomDomain pins the
// routing order (PR-B review finding #4): when a hostname matches
// BOTH a tenant-surface row AND a custom_domains row, the surface
// branch must win. This is the load-bearing design decision —
// a customer who has a custom_domain from before surfaces shipped
// and then later adds a surface on the same hostname (the apid
// path forbids this, but the store allows it because the citext
// UQ is per-table, not cross-table) must see the surface route,
// not the legacy custom_domain. The reverse precedence would
// silently route around a customer's explicit surface config.
func TestPgRouter_TenantSurfacePrecedenceOverCustomDomain(t *testing.T) {
	t.Setenv("FAAS_TENANT_SURFACES_ENABLED", "true")
	store := state.NewMemStore()
	ctx := context.Background()
	// One account owns both apps (the customer scenario: surface
	// points at the new app, legacy custom_domain pre-dates the
	// surface and points at an older app). seedApp auto-creates
	// a fresh account each call, so we create one account and
	// attach both apps directly.
	acct, err := store.CreateAccount(ctx, "blog@local", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	surfApp, err := store.CreateApp(ctx, state.App{
		AccountID: acct.ID, Slug: "blog", Type: state.AppTypeApp, RAMMB: 128, Status: state.AppActive,
	})
	if err != nil {
		t.Fatalf("CreateApp(surf): %v", err)
	}
	legacyApp, err := store.CreateApp(ctx, state.App{
		AccountID: acct.ID, Slug: "legacy", Type: state.AppTypeApp, RAMMB: 128, Status: state.AppActive,
	})
	if err != nil {
		t.Fatalf("CreateApp(legacy): %v", err)
	}
	lim := api.Limits{
		TenantSurfacesPerAccount:  5,
		TenantHostnamesPerSurface: 10,
		TenantSurfacesAllowed:     true,
	}
	surf, err := store.CreateTenantSurfaceIfUnderQuota(ctx, state.CreateTenantSurfaceParams{
		AccountID: acct.ID,
		AppID:     surfApp.ID,
		Name:      "primary",
	}, lim)
	if err != nil {
		t.Fatalf("CreateTenantSurfaceIfUnderQuota: %v", err)
	}
	if err := store.UpdateTenantSurfaceStatus(ctx, surf.ID, state.SurfaceStatusActive); err != nil {
		t.Fatalf("UpdateTenantSurfaceStatus: %v", err)
	}
	if _, err := store.CreateTenantHostnameIfUnderQuota(ctx, state.CreateTenantHostnameParams{
		SurfaceID:      surf.ID,
		Hostname:       "api.customer-a.com",
		ChallengeToken: "tok",
	}, lim); err != nil {
		t.Fatalf("CreateTenantHostnameIfUnderQuota: %v", err)
	}
	if err := store.MarkTenantHostnameVerified(ctx, "api.customer-a.com"); err != nil {
		t.Fatalf("MarkTenantHostnameVerified: %v", err)
	}
	// Legacy custom_domains row on the same hostname, owned by
	// the same account (the legacy app). Without surface
	// precedence this would route to legacyApp.
	if _, err := store.CreateCustomDomain(ctx, "api.customer-a.com", legacyApp.ID, "tok"); err != nil {
		t.Fatalf("CreateCustomDomain: %v", err)
	}
	if err := store.MarkDomainVerified(ctx, "api.customer-a.com"); err != nil {
		t.Fatalf("MarkDomainVerified: %v", err)
	}
	r := pgRouter{store: store, appsSuffix: ".apps.gregale.dev"}

	got, ok, err := r.ResolveHost(context.Background(), "api.customer-a.com")
	if err != nil || !ok {
		t.Fatalf("ResolveHost ok=%v err=%v, want true/nil", ok, err)
	}
	if got.ID != surfApp.ID {
		t.Fatalf("surface did not win precedence: got=%s, want surface app id=%s", got.ID, surfApp.ID)
	}
}
