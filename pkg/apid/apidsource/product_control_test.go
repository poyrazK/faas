package apidsource

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProductControlFollowsSourceDeployments(t *testing.T) {
	t.Setenv("FAAS_STORAGE_BACKEND", "local")
	t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
	ctx := t.Context()
	store := state.NewMemStore()
	app := mustSeedApp(t, store)
	spec := api.ProductCatalogSpec{Currency: "EUR", Features: map[string]api.ProductFeature{"export": {Label: "Export", Operation: &api.ProductOperationBinding{Name: "export", Scope: api.DefaultEnvScope}}}, Plans: map[string]api.ProductPlan{"starter": {Label: "Starter", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 100, UnitPriceMillicents: 25000}}}}}
	catalog, err := store.PutProductCatalog(ctx, app.AccountID, app.ID, spec)
	if err != nil {
		t.Fatal(err)
	}
	tenant, _, err := store.CreatePlatformTenant(ctx, app.AccountID, "pilot", "Pilot", 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetProductCustomer(ctx, app.AccountID, app.ID, api.ProductCustomer{TenantID: tenant.ID, Plan: "starter"}); err != nil {
		t.Fatal(err)
	}
	var previous string
	for range 2 {
		source, size := writeHandlerOnlyTarball(t)
		definition := api.OperationDefinitionSpec{Name: "export", Method: "POST", Path: "/exports", Owner: api.OperationOwnerPlatformTenant, InputSchema: []byte(`true`), OutputSchema: []byte(`true`), ProgressStages: []string{"generating"}}
		result, err := Enqueue(ctx, store, &recordingNotifier{}, EnqueueParams{AppID: app.ID, Kind: state.DeploymentKindTarball, SourcePath: source, SourceBytes: size, LogSpool: t.TempDir(), Log: quietLogger(), OperationDefinitions: []api.OperationDefinitionSpec{definition}, OperationAdmissionEnabled: true})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLive(ctx, result.DeploymentID); err != nil {
			t.Fatal(err)
		}
		resolved, err := store.ResolveProductFeature(ctx, state.ProductFeatureRequest{AccountID: app.AccountID, AppID: app.ID, TenantID: tenant.ID, Feature: "export"})
		if err != nil || resolved.DeploymentID != result.DeploymentID || resolved.ID == previous {
			t.Fatalf("source deployment not bound automatically: %+v %v", resolved, err)
		}
		previous = resolved.ID
		customer, err := store.GetProductCustomer(ctx, app.AccountID, app.ID, tenant.ID)
		if err != nil || customer.Revision != catalog.Revision {
			t.Fatalf("source deploy migrated commercial terms: %+v %v", customer, err)
		}
	}
}
