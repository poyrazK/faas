// ADR-1708: access policies are immutable catalog revisions, independent of prices.
package state

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/productcontrol"
)

func TestProductControlAccessMemory(t *testing.T) { productAccessContract(t, NewMemStore()) }

func productAccessContract(t *testing.T, s productSetupTestStore) {
	t.Helper()
	ctx := t.Context()
	acct, err := s.CreateAccount(ctx, uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, App{AccountID: acct.ID, Slug: "access-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	spec := api.ProductCatalogSpec{Currency: "EUR", Features: map[string]api.ProductFeature{"export": {Label: "Export", Operation: &api.ProductOperationBinding{Name: "export", Scope: api.DefaultEnvScope}}}, Plans: map[string]api.ProductPlan{"starter": {Label: "Starter", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 10, UnitPriceMillicents: 25000}}}}}
	before, err := s.PutProductCatalog(ctx, acct.ID, app.ID, spec)
	if err != nil {
		t.Fatal(err)
	}
	tenant, _, err := s.CreatePlatformTenant(ctx, acct.ID, "customer", "Customer", 100)
	if err != nil {
		t.Fatal(err)
	}
	assigned, err := s.SetProductCustomer(ctx, acct.ID, app.ID, api.ProductCustomer{TenantID: tenant.ID, Plan: "starter", AuthOrganizationID: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	policy := productcontrol.DefaultAccess()
	policy.Roles["operator"] = []api.ProductPermission{api.ProductPermissionRunWork}
	spec.Access = &policy
	first, err := s.PublishProductCatalog(ctx, acct.ID, app.ID, spec, before.Revision)
	if err != nil {
		t.Fatal(err)
	}
	policy.Roles["operator"][0] = api.ProductPermissionViewBilling
	first.Spec.Access.Roles["operator"][0] = api.ProductPermissionViewBilling
	stored, err := s.GetProductCatalog(ctx, acct.ID, app.ID, "")
	if err != nil || !productcontrol.EffectiveAccess(stored.Spec.Access, "operator").RunWork {
		t.Fatal("stored policy aliases caller/result", err)
	}
	if retry, err := s.PublishProductCatalog(ctx, acct.ID, app.ID, stored.Spec, before.Revision); err != nil || retry.Revision != stored.Revision {
		t.Fatalf("policy lost-response retry: %+v %v", retry, err)
	}
	old, err := s.GetProductCatalog(ctx, acct.ID, app.ID, before.Revision)
	if err != nil || old.Spec.Access != nil {
		t.Fatal("historical omitted policy was rewritten", err)
	}
	updated := cloneProductBilling(stored.Spec)
	updated.Access.Roles["operator"] = []api.ProductPermission{}
	head, err := s.PublishProductCatalog(ctx, acct.ID, app.ID, updated, stored.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.PublishProductCatalog(ctx, acct.ID, app.ID, stored.Spec, before.Revision); !errors.Is(err, ErrProductCatalogStale) {
		t.Fatal("old retry restored revoked access", err)
	}
	latest, err := s.GetProductCatalog(ctx, acct.ID, app.ID, "")
	if err != nil || latest.Revision != head.Revision || productcontrol.EffectiveAccess(latest.Spec.Access, "operator") != (api.ProductPermissions{}) {
		t.Fatal("revocation not retained", err)
	}
	customer, err := s.GetProductCustomer(ctx, acct.ID, app.ID, tenant.ID)
	if err != nil || customer != assigned {
		t.Fatal("permission publication changed commercial assignment", err)
	}
	invalid := cloneProductBilling(latest.Spec)
	invalid.Access.Roles["member"] = []api.ProductPermission{"*"}
	if _, err = s.PutProductCatalog(ctx, acct.ID, app.ID, invalid); !errors.Is(err, ErrInvalidArgument) {
		t.Fatal("invalid permission persisted", err)
	}
}
