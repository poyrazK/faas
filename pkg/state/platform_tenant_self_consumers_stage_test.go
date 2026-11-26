package state_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261104120000 — a stage copy of a tenant's consumer does not count
// against the tenant's production consumer quota.
func TestPgPlatformTenantSelfConsumerQuotaIgnoresStageCopies(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	suffix := uuid.NewString()[:8]
	account, err := store.CreateAccount(ctx, "self-consumer-stage-"+suffix+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	tenant, _, err := store.CreatePlatformTenant(ctx, account.ID, "self-consumer-stage", "Stage", 100)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "self-consumer-stage-" + suffix})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "self-consumer-stage-" + suffix,
		Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 128, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	surface, err := store.CreateTenantSurfaceIfUnderQuota(ctx, state.CreateTenantSurfaceParams{AccountID: account.ID, AppID: app.ID,
		Name: "surface-" + suffix}, api.MustLimitsFor(api.PlanPro))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateTenantSurfaceStatus(ctx, surface.ID, state.SurfaceStatusActive); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LinkPlatformTenantSurface(ctx, account.ID, tenant.ID, surface.ID); err != nil {
		t.Fatal(err)
	}
	// A stage consumer carrying the tenant, as a full clone's copy would.
	staged, err := store.CreateAPIConsumerInEnvironment(ctx, account.ID, app.ID, "staging", "stage-customer", "Stage customer")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update api_consumers set platform_tenant_id = $1::uuid where id = $2::uuid`, tenant.ID, staged.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetPlatformTenantConsumerProvisioningPolicy(ctx, account.ID, tenant.ID, true, 1); err != nil {
		t.Fatal(err)
	}
	created, wasCreated, err := store.CreatePlatformTenantSelfConsumer(ctx, state.CreatePlatformTenantSelfConsumerParams{AccountID: account.ID,
		TenantID: tenant.ID, SurfaceID: surface.ID, ExternalRef: "customer-1", Name: "Customer 1"})
	if err != nil || !wasCreated || created.EnvironmentID != "" {
		t.Fatalf("production consumer = %+v created=%v err=%v; want it created under the quota", created, wasCreated, err)
	}
}
