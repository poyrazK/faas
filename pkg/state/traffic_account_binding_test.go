// adr: 375
package state

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func testTrafficAccountRetirement(t *testing.T, store Store, account Account, app App, mode string, redirect func(string, string), seed func(EdgeRule), intent func() string) {
	t.Helper()
	ctx := t.Context()
	host := "account-retirement.example.test"
	peerAccount, peer := trafficTenantTransitionPeer(t, store)
	var legacy EdgeRule
	switch mode {
	case "tenant":
		surface, hostname := newTrafficTenantClaim(t, store, account, app, host)
		if err := store.UpdateTenantSurfaceStatus(ctx, surface.ID, SurfaceStatusActive); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkTenantHostnameVerified(ctx, hostname.Hostname); err != nil {
			t.Fatal(err)
		}
	case "domain", "global":
		if _, err := store.CreateCustomDomain(ctx, host, app.ID, "private-account-challenge"); err != nil {
			t.Fatal(err)
		}
		if mode == "domain" {
			if err := store.MarkDomainVerified(ctx, host); err != nil {
				t.Fatal(err)
			}
		}
	case "redirect":
		if _, err := store.CreateCustomDomain(ctx, host, peer.ID, "private-redirect-challenge"); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDomainVerified(ctx, host); err != nil {
			t.Fatal(err)
		}
		redirect(host, app.ID)
		owner, err := store.CreateAccount(ctx, "retirement-third@example.test", api.PlanScale)
		if err != nil {
			t.Fatal(err)
		}
		target, err := store.CreateApp(ctx, App{AccountID: owner.ID, Slug: "retirement-third", Status: AppActive})
		if err != nil {
			t.Fatal(err)
		}
		peerAccount, peer = owner, target
	}
	if mode != "global" {
		if _, err := store.CreateCustomDomain(ctx, "*.example.test", peer.ID, "private-retirement-peer"); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDomainVerified(ctx, "*.example.test"); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.UpsertAppSecret(ctx, account.ID, app.ID, "RETIREMENT_SECRET", []byte("test-ciphertext")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIKey(ctx, account.ID, []byte("retirement-test-hash"), "retirement", []string{"admin"}); err != nil {
		t.Fatal(err)
	}
	seedTrafficAppCleanup(t, store, account, app)
	legacy = publicationLegacyRule(peerAccount, peer, host)
	seed(legacy)
	if err := store.MarkAccountDeletionPending(ctx, account.ID); err != nil {
		t.Fatal(err)
	}
	before := intent()
	err := store.DeleteAccount(ctx, account.ID)
	requireTrafficTenantRefusal(t, err)
	var binding *TrafficPolicyBindingError
	if !errors.As(err, &binding) {
		t.Fatalf("account refusal lacks binding privacy marker: %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := store.DeleteAccount(canceled, account.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled account retirement: %v", err)
	}
	if before != intent() {
		t.Fatal("refused account retirement erased intent or recorded deletion")
	}
	// A refused sweep must leave self-service recovery available.
	if err := store.RestoreAccount(ctx, account.ID); err != nil {
		t.Fatalf("restore after refusal: %v", err)
	}
	if err := store.DeleteAccount(ctx, account.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("restored account retirement: %v", err)
	}
	if err := store.MarkAccountDeletionPending(ctx, account.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteEdgeRule(ctx, legacy.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteAccount(ctx, account.ID); err != nil {
		t.Fatalf("retirement after repair: %v", err)
	}
	if _, err := store.AccountByID(ctx, account.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("accepted retirement kept account")
	}
	if _, err := store.AppByID(ctx, app.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("accepted retirement kept owned app")
	}
	if mode == "tenant" {
		if _, err := store.GetTenantHostnameByName(ctx, host); !errors.Is(err, ErrNotFound) {
			t.Fatal("accepted retirement kept tenant hostname")
		}
	} else {
		if _, err := store.DomainByName(ctx, host); !errors.Is(err, ErrNotFound) {
			t.Fatal("accepted retirement kept domain reservation")
		}
	}
	if _, err := store.AccountByID(ctx, peerAccount.ID); err != nil {
		t.Fatal("retirement removed foreign account")
	}
}

func memTrafficAccountIntent(t *testing.T, m *MemStore) string {
	return trafficTenantIntentDigest(t, []any{memTrafficAppIntent(t, m), m.accounts, m.projects, m.projectEnvironments, m.edgeRules, m.corsPresets,
		m.platformTenants, m.keys, m.keyByHash, trafficAccountMapEntries(m.secrets), trafficAccountMapEntries(m.envs), trafficAccountMapEntries(m.registryCreds), m.events, m.auditLog, trafficAccountMapEntries(m.billingIdentities), m.objectBuckets, m.previewSets})
}

func TestMemTrafficAccountRetirement(t *testing.T) {
	for _, mode := range []string{"tenant", "domain", "global", "redirect"} {
		t.Run(mode, func(t *testing.T) {
			m, account, _, app, _ := memTrafficFixture(t)
			testTrafficAccountRetirement(t, m, account, app, mode, func(host, id string) { d := m.domains[host]; d.RedirectAppID = id; m.domains[host] = d }, func(rule EdgeRule) { m.edgeRules[rule.ID] = rule }, func() string { return memTrafficAccountIntent(t, m) })
		})
	}
}

// Convert compound-key maps into deterministic JSON only for the private digest.
func trafficAccountMapEntries[K comparable, V any](rows map[K]V) map[string]V {
	out := make(map[string]V, len(rows))
	for key, value := range rows {
		out[fmt.Sprintf("%#v", key)] = value
	}
	return out
}

func testTrafficAccountForeignSurface(t *testing.T, store Store, account Account, app App, retarget func(string, string), seed func(EdgeRule), intent func() string) {
	t.Helper()
	ctx := t.Context()
	owner, ownedApp := trafficTenantTransitionPeer(t, store)
	surface, host := newTrafficTenantClaim(t, store, owner, ownedApp, "foreign-surface-retirement.example.test")
	if err := store.UpdateTenantSurfaceStatus(ctx, surface.ID, SurfaceStatusActive); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkTenantHostnameVerified(ctx, host.Hostname); err != nil {
		t.Fatal(err)
	}
	platform := store.(PlatformTenantStore)
	tenant, _, err := platform.CreatePlatformTenant(ctx, owner.ID, "retirement-tenant", "Retirement tenant", owner.Plan.ConsumerKeysPerAccount())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := platform.LinkPlatformTenantSurface(ctx, owner.ID, tenant.ID, surface.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := platform.SetPlatformTenantStatus(ctx, owner.ID, tenant.ID, PlatformTenantSuspended); err != nil {
		t.Fatal(err)
	}
	// This legacy mismatch is permitted by the app FK. Deleting the app
	// cascades this foreign surface even though its account owns no local app.
	retarget(surface.ID, app.ID)
	third, err := store.CreateAccount(ctx, "surface-retirement-third@example.test", api.PlanScale)
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.CreateApp(ctx, App{AccountID: third.ID, Slug: "surface-retirement-third", Status: AppActive})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateCustomDomain(ctx, "*.example.test", target.ID, "private-surface-peer"); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDomainVerified(ctx, "*.example.test"); err != nil {
		t.Fatal(err)
	}
	legacy := publicationLegacyRule(third, target, host.Hostname)
	legacy.Kind, legacy.Action.Kind, legacy.Action.Route = EdgeRuleKindHeaders, EdgeRuleKindHeaders, nil
	legacy.Action.Headers = &EdgeRuleHeadersAction{ResponseHeaders: []EdgeRuleHeaderOp{{Name: "X-Private", Action: "set", Value: "surface-retirement"}}}
	seed(legacy)
	if err := store.MarkAccountDeletionPending(ctx, account.ID); err != nil {
		t.Fatal(err)
	}
	before := intent()
	requireTrafficTenantRefusal(t, store.DeleteAccount(ctx, account.ID))
	if before != intent() {
		t.Fatal("refused retirement changed foreign surface, account or audit")
	}
	if err := store.DeleteEdgeRule(ctx, legacy.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteAccount(ctx, account.ID); err != nil {
		t.Fatalf("foreign surface retirement after repair: %v", err)
	}
	if _, err := store.GetTenantHostnameByName(ctx, host.Hostname); !errors.Is(err, ErrNotFound) {
		t.Fatal("app FK cascade retained foreign hostname")
	}
	if _, err := store.AppByID(ctx, ownedApp.ID); err != nil {
		t.Fatal("app FK cascade deleted foreign app")
	}
	if rows, err := platform.ListPlatformTenantSurfaces(ctx, owner.ID, tenant.ID); err != nil || len(rows) != 0 {
		t.Fatal("app FK cascade retained foreign tenant link")
	}
}

func TestMemTrafficAccountForeignSurface(t *testing.T) {
	m, account, _, app, _ := memTrafficFixture(t)
	testTrafficAccountForeignSurface(t, m, account, app, func(surface, id string) {
		row := m.tenantSurfaces[surface]
		row.AppID = id
		m.tenantSurfaces[surface] = row
	}, func(rule EdgeRule) { m.edgeRules[rule.ID] = rule }, func() string { return memTrafficAccountIntent(t, m) })
}
