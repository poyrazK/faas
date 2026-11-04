package apidserver_test

import (
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCustomDomainPostgresBulkRejectsForeignWildcard(t *testing.T) {
	t.Setenv("FAAS_TENANT_SURFACES_ENABLED", "true")
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	store := *e.Store()
	claimant, err := store.CreateApp(t.Context(), state.App{AccountID: (*e.Acct()).ID, Slug: "bulk-claimant", Status: state.AppActive, Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	victim, err := store.CreateAccount(t.Context(), "victim@pg.example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: victim.ID, Slug: "bulk-victim", Status: state.AppActive, Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	const wildcard = "*.bulk.pg.example.com"
	if _, err = store.CreateCustomDomain(t.Context(), wildcard, app.ID, "proof"); err != nil {
		t.Fatal(err)
	}
	if err = store.MarkDomainVerified(t.Context(), wildcard); err != nil {
		t.Fatal(err)
	}
	response := e.Do(t, http.MethodPost, "/v1/account/platform-tenants/apply", api.ApplyPlatformTenantRequest{ExternalRef: "pg-bypass", Name: "PG bypass", Surfaces: []api.ApplyPlatformTenantSurfaceRequest{{AppID: claimant.ID, Name: "foreign-zone", Hostnames: []string{"live.bulk.pg.example.com"}}}}, nil)
	if response.Code != http.StatusConflict {
		t.Fatalf("PostgreSQL public bulk endpoint publishes foreign wildcard overlap: %d %s", response.Code, response.Body)
	}
}

func TestCustomDomainTenantCannotClaimForeignExactDomain(t *testing.T) {
	t.Setenv("FAAS_TENANT_SURFACES_ENABLED", "true")
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	claimant := apidserver.MustSeedApp(t, e, "exact-claimant")
	victim := domainRecoveryApp(t, store, "exact-victim@example.com", "exact-victim")
	const host = "exact.victim.example.com"
	domainRecoveryVerified(t, store, host, victim.ID, "victim-proof")
	response := e.Do(t, http.MethodPost, "/v1/apps/exact-claimant/tenant-surfaces", api.CreateTenantSurfaceRequest{AppID: claimant, Name: "another-owner", Hostnames: []string{host}}, nil)
	if response.Code != http.StatusConflict {
		t.Fatalf("unexpected response: %d %s", response.Code, response.Body)
	}
}

func TestCustomDomainTenantReconciliationRejectsForeignReservation(t *testing.T) {
	withIdempotencyHostKey(t)
	t.Setenv("FAAS_TENANT_SURFACES_ENABLED", "true")
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	appID := apidserver.MustSeedApp(t, e, "reconcile-claimant")
	victim := domainRecoveryApp(t, store, "reconcile-victim@example.com", "reconcile-victim")
	const host = "reconcile.victim.example.com"
	domainRecoveryVerified(t, store, host, victim.ID, "victim-proof")
	tenant, _, err := store.CreatePlatformTenant(t.Context(), (*e.Acct()).ID, "reconcile-foreign", "Reconcile", 100)
	if err != nil {
		t.Fatal(err)
	}
	surfaces := []api.ApplyPlatformTenantSurfaceRequest{{AppID: appID, Name: "foreign-host", Hostnames: []string{host}}}
	path := "/v1/account/platform-tenants/" + tenant.ID + "/reconciliation-plan"
	response := e.Do(t, http.MethodPost, path, api.PlanPlatformTenantReconciliationRequest{Surfaces: surfaces}, nil)
	if response.Code != http.StatusConflict {
		t.Fatalf("public plan reserves foreign exact host: %d %s", response.Code, response.Body)
	}
	// An old/internal plan must still be refused at the public apply boundary.
	limits, _ := api.LimitsFor(api.PlanPro)
	in := state.PlatformTenantReconciliationParams{TenantID: tenant.ID, ApplyPlatformTenantParams: state.ApplyPlatformTenantParams{AccountID: tenant.AccountID, ExternalRef: tenant.ExternalRef, Name: tenant.Name, TenantLimit: 100, Limits: limits, Surfaces: []state.ApplyPlatformTenantSurface{{AppID: appID, Name: "foreign-host", CertKind: state.CertKindPerHostSAN, Hostnames: []state.ApplyPlatformTenantHostname{{Hostname: host, ChallengeToken: "unproved"}}}}}}
	plan, err := store.PlanPlatformTenantReconciliation(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	apply := e.Do(t, http.MethodPost, path+"/apply", api.ApplyPlatformTenantReconciliationRequest{Surfaces: surfaces, ExpectedPlanHash: plan.PlanHash}, map[string]string{"Idempotency-Key": "foreign-reconcile"})
	if apply.Code != http.StatusConflict {
		t.Fatalf("public apply publishes foreign exact host: %d %s", apply.Code, apply.Body)
	}
}
