package state_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type productSetupTestStore interface {
	state.ProductContractStore
	state.ProductSetupStore
}

func TestProductControlSetupMemory(t *testing.T) { productSetupContract(t, state.NewMemStore()) }

func productSetupContract(t *testing.T, s productSetupTestStore) {
	t.Helper()
	ctx := t.Context()
	acct, err := s.CreateAccount(ctx, uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "setup-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	spec := api.ProductCatalogSpec{Currency: "EUR", Features: map[string]api.ProductFeature{"export": {Label: "Export", Operation: &api.ProductOperationBinding{Name: "export", Scope: api.DefaultEnvScope}}}, Plans: map[string]api.ProductPlan{"starter": {Label: "Starter", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 10, UnitPriceMillicents: 25000}}}}}
	first, err := s.PublishProductCatalog(ctx, acct.ID, app.ID, spec, "")
	if err != nil {
		t.Fatal(err)
	}
	tenant, _, err := s.CreatePlatformTenant(ctx, acct.ID, "alice", "Alice", 100)
	if err != nil {
		t.Fatal(err)
	}
	assigned, err := s.SetProductCustomer(ctx, acct.ID, app.ID, api.ProductCustomer{TenantID: tenant.ID, Plan: "starter", AuthOrganizationID: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	if retry, err := s.PublishProductCatalog(ctx, acct.ID, app.ID, spec, ""); err != nil || retry.Revision != first.Revision {
		t.Fatalf("first lost response: %+v %v", retry, err)
	}
	var wg sync.WaitGroup
	type result struct {
		catalog api.ProductCatalog
		spec    api.ProductCatalogSpec
		err     error
	}
	results := make(chan result, 2)
	for _, price := range []int64{20000, 10000} {
		wg.Go(func() {
			next := state.CloneProductBilling(spec)
			plan := next.Plans["starter"]
			grant := plan.Features["export"]
			grant.UnitPriceMillicents = price
			plan.Features["export"] = grant
			next.Plans["starter"] = plan
			catalog, err := s.PublishProductCatalog(ctx, acct.ID, app.ID, next, first.Revision)
			results <- result{catalog, next, err}
		})
	}
	wg.Wait()
	close(results)
	var winner result
	success, stale := 0, 0
	for r := range results {
		if r.err == nil {
			success++
			winner = r
		} else if errors.Is(r.err, state.ErrProductCatalogStale) {
			stale++
		} else {
			t.Fatal(r.err)
		}
	}
	if success != 1 || stale != 1 {
		t.Fatalf("publication race: %d applied, %d stale", success, stale)
	}
	if got, err := s.PublishProductCatalog(ctx, acct.ID, app.ID, winner.spec, first.Revision); err != nil || got.Revision != winner.catalog.Revision {
		t.Fatalf("retained current publication: %+v %v", got, err)
	}
	later := state.CloneProductBilling(spec)
	plan := later.Plans["starter"]
	grant := plan.Features["export"]
	grant.UnitPriceMillicents = 30000
	plan.Features["export"] = grant
	later.Plans["starter"] = plan
	head, err := s.PutProductCatalog(ctx, acct.ID, app.ID, later)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PublishProductCatalog(ctx, acct.ID, app.ID, winner.spec, first.Revision); !errors.Is(err, state.ErrProductCatalogStale) {
		t.Fatalf("old retry overwrote later head: %v", err)
	}
	got, err := s.GetProductCatalog(ctx, acct.ID, app.ID, "")
	if err != nil || got.Revision != head.Revision {
		t.Fatal("publication changed latest catalog")
	}
	customer, err := s.GetProductCustomer(ctx, acct.ID, app.ID, tenant.ID)
	if err != nil || customer != assigned {
		t.Fatalf("setup migrated customer identity or terms: %+v %v", customer, err)
	}
	later.Currency = "USD"
	if _, err := s.PublishProductCatalog(ctx, acct.ID, app.ID, later, head.Revision); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("currency changed: %v", err)
	}
	if _, err := s.PublishProductCatalog(ctx, uuid.NewString(), app.ID, spec, head.Revision); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("foreign publication: %v", err)
	}
	if _, err := s.PublishProductCatalog(ctx, acct.ID, app.ID, spec, "bad"); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("invalid preview: %v", err)
	}
}
