// adr: 531
package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var trafficTenantRemovalModes = []string{"hostname", "owned-hostname", "surface-suspend", "surface-delete", "cascade", "offboarding", "reconciliation", "global-pending", "global-deleted"}

func trafficTenantTransitionPeer(t *testing.T, store Store) (Account, App) {
	t.Helper()
	account, err := store.CreateAccount(t.Context(), "tenant-transition-peer@example.test", api.PlanScale)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), App{AccountID: account.ID, Slug: "tenant-transition-peer", Status: AppActive})
	if err != nil {
		t.Fatal(err)
	}
	return account, app
}

func requireTrafficTenantRefusal(t *testing.T, err error) {
	t.Helper()
	var aggregate *TrafficPolicyAggregateError
	if !errors.As(err, &aggregate) || aggregate.Observed <= aggregate.Limit {
		t.Fatalf("tenant transition exposed an oversized binding: %v", err)
	}
}

func testTrafficTenantRemoval(t *testing.T, store Store, account Account, app App, mode string, seed func(EdgeRule), intent func() string) {
	t.Helper()
	ctx := t.Context()
	applyStore := store.(PlatformTenantApplyStore)
	in := ApplyPlatformTenantParams{AccountID: account.ID, ExternalRef: "transition", Name: "Private transition tenant", TenantLimit: 100,
		Limits: api.MustLimitsFor(account.Plan), Consumers: []ApplyPlatformTenantConsumer{{AppID: app.ID, ExternalRef: "managed", Name: "Managed"}},
		Surfaces: []ApplyPlatformTenantSurface{{AppID: app.ID, Name: "transition", CertKind: CertKindPerHostSAN,
			Hostnames: []ApplyPlatformTenantHostname{{Hostname: "upper.transition.example.test", ChallengeToken: "private-transition-token"}}}}}
	if mode == "reconciliation" {
		in.Surfaces[0].Hostnames = append(in.Surfaces[0].Hostnames, ApplyPlatformTenantHostname{Hostname: "retained.transition.example.test", ChallengeToken: "retained-private-token"})
	}
	created, err := applyStore.ApplyPlatformTenant(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	surface, host := created.Surfaces[0].Surface, created.Surfaces[0].Hostnames[0].Hostname
	if mode != "global-pending" {
		status := SurfaceStatusActive
		if mode == "global-deleted" {
			status = SurfaceStatusDeleted
		}
		if err := store.UpdateTenantSurfaceStatus(ctx, surface.ID, status); err != nil {
			t.Fatal(err)
		}
	}
	if !strings.HasPrefix(mode, "global-") {
		if err := store.MarkTenantHostnameVerified(ctx, host.Hostname); err != nil {
			t.Fatal(err)
		}
	}
	peerAccount, peer := trafficTenantTransitionPeer(t, store)
	if !strings.HasPrefix(mode, "global-") {
		if _, err := store.CreateCustomDomain(ctx, "*.transition.example.test", peer.ID, "private-peer-token"); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDomainVerified(ctx, "*.transition.example.test"); err != nil {
			t.Fatal(err)
		}
	}
	if mode == "offboarding" || mode == "reconciliation" {
		seedTrafficTenantCredentials(t, store, account, created)
	}
	var offPlan api.PlatformTenantOffboardingPlanResponse
	var reconPlan api.PlatformTenantReconciliationPlanResponse
	recon := PlatformTenantReconciliationParams{TenantID: created.Tenant.ID, ApplyPlatformTenantParams: in}
	if mode == "reconciliation" {
		recon.Surfaces = append([]ApplyPlatformTenantSurface(nil), in.Surfaces...)
		recon.Surfaces[0].Hostnames = recon.Surfaces[0].Hostnames[1:]
	}
	if mode == "offboarding" {
		offPlan, err = store.(PlatformTenantOffboardingStore).PlanPlatformTenantOffboarding(ctx, account.ID, created.Tenant.ID)
		if err != nil {
			t.Fatal(err)
		}
	} else if mode == "reconciliation" {
		reconPlan, err = store.(PlatformTenantReconciliationStore).PlanPlatformTenantReconciliation(ctx, recon)
		if err != nil {
			t.Fatal(err)
		}
	}
	legacy := publicationLegacyRule(peerAccount, peer, strings.ToLower(host.Hostname))
	seed(legacy)
	beforeSurface, err := store.GetTenantSurfaceByID(ctx, surface.ID)
	if err != nil {
		t.Fatal(err)
	}
	beforeHost, err := readTrafficTenantHostnameRow(t, store, surface.ID, host.ID)
	if err != nil {
		t.Fatal(err)
	}
	beforeIntent := intent()
	remove := func(c context.Context) error {
		switch mode {
		case "surface-suspend":
			return store.UpdateTenantSurfaceStatus(c, surface.ID, SurfaceStatusSuspended)
		case "surface-delete":
			return store.DeleteTenantSurface(c, surface.ID)
		case "owned-hostname":
			return store.(TenantHostnameRemovalOwnerStore).DeleteTenantHostnameForSurface(c, host.Hostname, surface.ID)
		case "cascade":
			return store.(TenantSurfaceRemovalOwnerStore).DeleteTenantSurfaceWithHostnames(c, surface.ID, account.ID)
		case "offboarding":
			out, err := store.(PlatformTenantOffboardingApplyStore).ApplyPlatformTenantOffboarding(c, account.ID, created.Tenant.ID, offPlan.PlanHash)
			if out.Applied || out.ReceiptID != "" {
				t.Fatal("refused offboarding returned a receipt")
			}
			return err
		case "reconciliation":
			out, err := store.(PlatformTenantReconciliationStore).ApplyPlatformTenantReconciliation(c, recon, reconPlan.PlanHash)
			if err != nil && (out.Applied || out.ReceiptID != "") {
				t.Fatal("refused reconciliation returned a receipt")
			}
			return err
		default:
			return store.DeleteTenantHostname(c, host.Hostname)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := remove(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled transition: %v", err)
	}
	requireTrafficTenantRefusal(t, remove(ctx))
	if mode == "offboarding" {
		_, err := store.(PlatformTenantOffboardingStore).PlanPlatformTenantOffboarding(ctx, account.ID, created.Tenant.ID)
		requireTrafficTenantRefusal(t, err)
	} else if mode == "reconciliation" {
		_, err := store.(PlatformTenantReconciliationStore).PlanPlatformTenantReconciliation(ctx, recon)
		requireTrafficTenantRefusal(t, err)
	}
	afterSurface, err := store.GetTenantSurfaceByID(ctx, surface.ID)
	if err != nil || !reflect.DeepEqual(beforeSurface, afterSurface) {
		t.Fatal("refusal changed surface intent")
	}
	afterHost, err := readTrafficTenantHostnameRow(t, store, surface.ID, host.ID)
	if err != nil || !reflect.DeepEqual(beforeHost, afterHost) || intent() != beforeIntent {
		t.Fatal("refusal changed hostname, credentials, webhook, or receipt intent")
	}
	if mode == "offboarding" {
		if _, err := store.(PlatformTenantStore).SetPlatformTenantStatus(ctx, account.ID, created.Tenant.ID, PlatformTenantSuspended); err != nil {
			t.Fatalf("immediate suspension was blocked by cleanup policy: %v", err)
		}
	}
	if err := store.DeleteEdgeRule(ctx, legacy.ID); err != nil {
		t.Fatal(err)
	}
	// Apply a fresh safe plan after repair. Ordinary surface/hostname removals
	// retain the same captured identity.
	if mode == "offboarding" {
		plan, err := store.(PlatformTenantOffboardingStore).PlanPlatformTenantOffboarding(ctx, account.ID, created.Tenant.ID)
		if err != nil {
			t.Fatal(err)
		}
		out, err := store.(PlatformTenantOffboardingApplyStore).ApplyPlatformTenantOffboarding(ctx, account.ID, created.Tenant.ID, plan.PlanHash)
		if err != nil || !out.Applied || out.ReceiptID == "" {
			t.Fatalf("offboarding repair: %v", err)
		}
	} else if err := remove(ctx); err != nil {
		t.Fatalf("transition repair: %v", err)
	}
	if mode == "surface-suspend" || mode == "surface-delete" {
		if _, err := readTrafficTenantHostnameRow(t, store, surface.ID, host.ID); err != nil {
			t.Fatal("soft surface change removed reservation")
		}
	} else if _, err := readTrafficTenantHostnameRow(t, store, surface.ID, host.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("repair retained removed hostname: %v", err)
	}
}

func testTrafficTenantBulkLink(t *testing.T, store Store, account Account, app App, seed func(EdgeRule), intent func() string) {
	t.Helper()
	surface, host := newTrafficTenantClaim(t, store, account, app, "bulk-link.example.test")
	if err := store.UpdateTenantSurfaceStatus(t.Context(), surface.ID, SurfaceStatusActive); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkTenantHostnameVerified(t.Context(), host.Hostname); err != nil {
		t.Fatal(err)
	}
	in := ApplyPlatformTenantParams{AccountID: account.ID, ExternalRef: "bulk", Name: "Bulk", TenantLimit: 100, Limits: api.MustLimitsFor(account.Plan), SurfaceIDs: []string{surface.ID},
		Consumers: []ApplyPlatformTenantConsumer{{AppID: app.ID, ExternalRef: "new-consumer", Name: "New consumer"}}}
	legacy := publicationLegacyRule(account, app, host.Hostname)
	seed(legacy)
	before := intent()
	for _, dryRun := range []bool{true, false} {
		in.DryRun = dryRun
		result, err := store.(PlatformTenantApplyStore).ApplyPlatformTenant(t.Context(), in)
		requireTrafficTenantRefusal(t, err)
		if result.Tenant.ID != "" || intent() != before {
			t.Fatal("bulk refusal published tenant, links, consumer or webhooks")
		}
	}
	if err := store.DeleteEdgeRule(t.Context(), legacy.ID); err != nil {
		t.Fatal(err)
	}
	in.DryRun = true
	planned, err := store.(PlatformTenantApplyStore).ApplyPlatformTenant(t.Context(), in)
	if err != nil || planned.Tenant.ID != "" || planned.Consumers[0].Consumer.ID != "" {
		t.Fatalf("dry run leaked staged identities: %v", err)
	}
	in.DryRun = false
	created, err := store.(PlatformTenantApplyStore).ApplyPlatformTenant(t.Context(), in)
	if err != nil || created.Tenant.ID == "" || created.Consumers[0].Consumer.ID == "" {
		t.Fatalf("bulk repair: %v", err)
	}
}

func memTrafficTenantIntent(t *testing.T, m *MemStore) string {
	t.Helper()
	// Only a digest is returned to assertions; credential material is never printed.
	return trafficTenantIntentDigest(t, []any{m.platformTenants, m.apiConsumers, m.platformTenantBySurface, m.platformTenantByConsumer,
		m.tenantSurfaces, m.tenantHostnames, m.consumerKeys, m.platformTenantAccessTokens, m.platformTenantHostnamePolicies,
		m.platformTenantCredentialPolicies, m.platformTenantConsumerPolicies, m.platformTenantReconciliationReceipts,
		m.platformTenantOffboardingReceipts, m.appWebhookEventOutbox, m.appWebhookDeliveries})
}

func TestMemTrafficTenantCompleteTransitions(t *testing.T) {
	for _, mode := range trafficTenantRemovalModes {
		t.Run(mode, func(t *testing.T) {
			m, account, _, app, _ := memTrafficFixture(t)
			testTrafficTenantRemoval(t, m, account, app, mode, func(rule EdgeRule) { m.edgeRules[rule.ID] = rule }, func() string { return memTrafficTenantIntent(t, m) })
		})
	}
	t.Run("bulk-link", func(t *testing.T) {
		m, account, _, app, _ := memTrafficFixture(t)
		testTrafficTenantBulkLink(t, m, account, app, func(rule EdgeRule) { m.edgeRules[rule.ID] = rule }, func() string { return memTrafficTenantIntent(t, m) })
	})
}

func TestTrafficTenantSelectionPrecedence(t *testing.T) {
	tenant := trafficHostTenant{Host: "api.example.test", App: "app", Surface: "surface", ID: "host", PlatformTenant: "tenant"}
	for _, tc := range []struct {
		name                                                             string
		status                                                           SurfaceStatus
		verified, public, suspended, enabled, namespace, domain, binding bool
	}{
		{"active", SurfaceStatusActive, true, true, false, true, false, false, true},
		{"unverified", SurfaceStatusActive, false, true, false, true, false, true, false},
		{"pending", SurfaceStatusPending, true, true, false, true, false, true, false},
		{"internal", SurfaceStatusActive, true, false, false, true, false, true, false},
		{"suspended-unverified", SurfaceStatusPending, false, false, true, true, false, false, false},
		{"deleted-suspended", SurfaceStatusDeleted, true, true, true, true, false, true, false},
		{"disabled", SurfaceStatusActive, true, true, false, false, false, true, false},
		{"namespace", SurfaceStatusActive, true, true, false, true, true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			claim := trafficTenantClaim{trafficHostTenant: tenant, Status: tc.status, Verified: tc.verified, Public: tc.public, Suspended: tc.suspended}
			domain := trafficHostDomain{Domain: "*.example.test", App: "peer"}
			bindings := map[trafficHostDomain]*trafficHostEnvironment{domain: nil, tenantTrafficBinding(tenant): nil}
			selectTrafficTenantBindings(bindings, &claim, tc.enabled, tc.namespace)
			_, hasDomain := bindings[domain]
			_, hasTenant := bindings[tenantTrafficBinding(tenant)]
			if hasDomain != tc.domain || hasTenant != tc.binding {
				t.Fatalf("domain=%v tenant=%v", hasDomain, hasTenant)
			}
		})
	}
}

func trafficTenantIntentDigest(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func seedTrafficTenantCredentials(t *testing.T, store Store, account Account, created ApplyPlatformTenantResult) {
	t.Helper()
	ctx := t.Context()
	_, prefix, hash, err := api.GenerateConsumerKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.(PlatformTenantCredentialStore).ApplyPlatformTenantCredentials(ctx, ApplyPlatformTenantCredentialsParams{
		AccountID: account.ID, TenantID: created.Tenant.ID, AppLimit: 100, AccountLimit: 100,
		Keys: []PlatformTenantCredentialIntent{{ConsumerID: created.Consumers[0].Consumer.ID, Name: "retained-key", Prefix: prefix, Hash: hash, Scopes: []string{"read"}}},
	}); err != nil {
		t.Fatal(err)
	}
	_, tokenPrefix, tokenHash, err := api.GeneratePlatformTenantAccessToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.(PlatformTenantAccessStore).CreatePlatformTenantAccessToken(ctx, PlatformTenantAccessTokenInput{
		AccountID: account.ID, TenantID: created.Tenant.ID, Name: "retained-token", Prefix: tokenPrefix, TokenHash: tokenHash,
		Scopes: []string{api.ScopePlatformTenantConsumersManage}, ExpiresAt: time.Now().UTC().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.(PlatformTenantCredentialPolicyStore).SetPlatformTenantCredentialPolicy(ctx, account.ID, created.Tenant.ID, []string{"read"}, 3); err != nil {
		t.Fatal(err)
	}
	if _, err := store.(PlatformTenantConsumerProvisioningPolicyStore).SetPlatformTenantConsumerProvisioningPolicy(ctx, account.ID, created.Tenant.ID, true, 4); err != nil {
		t.Fatal(err)
	}
	if _, err := store.(PlatformTenantHostnamePolicyStore).SetPlatformTenantHostnamePolicy(ctx, account.ID, created.Tenant.ID, []string{"transition.example.test"}, 4); err != nil {
		t.Fatal(err)
	}
	if _, err := store.(PlatformTenantWebhookStore).CreatePlatformTenantWebhookIfUnderQuota(ctx, AppWebhook{
		AccountID: account.ID, PlatformTenantID: created.Tenant.ID, TargetURL: "https://private.example.test/transition-events", SecretSealed: []byte("sealed-private"),
		EventFilter: []string{PlatformTenantReconciliationAppliedEvent, PlatformTenantCustomerLinkedEvent}, Enabled: true,
	}, api.MustLimitsFor(account.Plan)); err != nil {
		t.Fatal(err)
	}
}

// Intent enumeration includes reservations on soft-deleted surfaces; the
// serving lookup deliberately hides them and cannot verify their retention.
func readTrafficTenantHostnameRow(t *testing.T, store Store, surface, id string) (TenantHostname, error) {
	t.Helper()
	rows, err := store.ListTenantHostnamesForSurface(t.Context(), surface)
	if err != nil {
		return TenantHostname{}, err
	}
	for _, row := range rows {
		if row.ID == id {
			return row, nil
		}
	}
	return TenantHostname{}, ErrNotFound
}

func testTrafficTenantReconciliationFinalTopology(t *testing.T, store Store, account Account, app App, seed func(EdgeRule), unlink func(string), intent func() string) {
	t.Helper()
	in := ApplyPlatformTenantParams{AccountID: account.ID, ExternalRef: "final-topology", Name: "Final topology", TenantLimit: 100, Limits: api.MustLimitsFor(account.Plan),
		Surfaces: []ApplyPlatformTenantSurface{{AppID: app.ID, Name: "final-topology", CertKind: CertKindPerHostSAN, Hostnames: []ApplyPlatformTenantHostname{
			{Hostname: "removed.final.example.test", ChallengeToken: "removed-private"}, {Hostname: "retained.final.example.test", ChallengeToken: "retained-private"},
		}}}}
	created, err := store.(PlatformTenantApplyStore).ApplyPlatformTenant(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	surface, removed := created.Surfaces[0].Surface, created.Surfaces[0].Hostnames[0].Hostname
	if err := store.UpdateTenantSurfaceStatus(t.Context(), surface.ID, SurfaceStatusActive); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkTenantHostnameVerified(t.Context(), removed.Hostname); err != nil {
		t.Fatal(err)
	}
	// Legacy handoff leaves a managed surface unlinked. Linking it alone
	// would give its oversized serving policy a new binding identity.
	unlink(surface.ID)
	legacy := publicationLegacyRule(account, app, removed.Hostname)
	legacy.Kind, legacy.Action.Kind, legacy.Action.Route = EdgeRuleKindHeaders, EdgeRuleKindHeaders, nil
	legacy.Action.Headers = &EdgeRuleHeadersAction{ResponseHeaders: []EdgeRuleHeaderOp{{Name: "X-Private", Action: "set", Value: "private-final-policy"}}}
	seed(legacy)
	in.Surfaces[0].Hostnames = in.Surfaces[0].Hostnames[1:]
	before := intent()
	in.DryRun = true
	_, err = store.(PlatformTenantApplyStore).ApplyPlatformTenant(t.Context(), in)
	requireTrafficTenantRefusal(t, err)
	if intent() != before {
		t.Fatal("refused additive dry run published intent")
	}
	recon := PlatformTenantReconciliationParams{TenantID: created.Tenant.ID, ApplyPlatformTenantParams: in}
	plan, err := store.(PlatformTenantReconciliationStore).PlanPlatformTenantReconciliation(t.Context(), recon)
	if err != nil {
		t.Fatalf("final safe topology refused by intermediate binding: %v", err)
	}
	if intent() != before {
		t.Fatal("reconciliation preview published intent")
	}
	applied, err := store.(PlatformTenantReconciliationStore).ApplyPlatformTenantReconciliation(t.Context(), recon, plan.PlanHash)
	if err != nil || !applied.Applied || applied.ReceiptID == "" {
		t.Fatalf("safe combined reconciliation: %v", err)
	}
	if _, err := readTrafficTenantHostnameRow(t, store, surface.ID, removed.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("combined final topology retained unsafe hostname: %v", err)
	}
	if rows, err := store.(PlatformTenantStore).ListPlatformTenantSurfaces(t.Context(), account.ID, created.Tenant.ID); err != nil || len(rows) != 1 || rows[0].ID != surface.ID {
		t.Fatal("combined final topology did not publish the link")
	}
}

func TestMemTrafficTenantReconciliationValidatesFinalTopology(t *testing.T) {
	m, account, _, app, _ := memTrafficFixture(t)
	testTrafficTenantReconciliationFinalTopology(t, m, account, app, func(rule EdgeRule) { m.edgeRules[rule.ID] = rule }, func(id string) { delete(m.platformTenantBySurface, id) }, func() string { return memTrafficTenantIntent(t, m) })
}
