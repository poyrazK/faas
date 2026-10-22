package state

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/productcontrol"
)

func TestProductIncludedStartsMemory(t *testing.T) { productIncludedStartsContract(t, NewMemStore()) }

// adr: 20261021154511 — a grant's included starts are admitted at price 0 and
// later starts at the unit price up to the monthly limit. The quote binds the
// next start's price, so the start that crosses into overage needs a fresh
// confirmation, and a plan change keeps counting the month's starts.
func productIncludedStartsContract(t *testing.T, s productContractStore) {
	t.Helper()
	ctx := t.Context()
	account, err := s.CreateAccount(ctx, "product-included-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, App{AccountID: account.ID, Slug: "product-included", Manifest: AppManifest{RevisionPinTTLSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := s.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:included"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	tenant, _, err := s.CreatePlatformTenant(ctx, account.ID, "included", "Included", 100)
	if err != nil {
		t.Fatal(err)
	}
	def, err := s.PutOperationDefinition(ctx, OperationDefinition{AccountID: account.ID, OperationDefinitionResponse: api.OperationDefinitionResponse{AppID: app.ID, DeploymentID: dep.ID, Scope: dep.Scope,
		Spec: api.OperationDefinitionSpec{Name: "export", Method: "POST", Path: "/exports", Owner: api.OperationOwnerPlatformTenant, ProgressStages: []string{"generating"},
			InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	spec := api.ProductCatalogSpec{Currency: "EUR", Features: map[string]api.ProductFeature{"export": {Label: "Export", DefinitionID: def.ID}}, Plans: map[string]api.ProductPlan{
		"starter": {Label: "Starter", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 3, UnitPriceMillicents: 20000, IncludedStarts: 2}}},
		"pro":     {Label: "Pro", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 10, UnitPriceMillicents: 15000, IncludedStarts: 5}}}}}
	catalog, err := s.PutProductCatalog(ctx, account.ID, app.ID, spec)
	if err != nil {
		t.Fatal(err)
	}
	customer, err := s.SetProductCustomer(ctx, account.ID, app.ID, api.ProductCustomer{TenantID: tenant.ID, Plan: "starter"})
	if err != nil {
		t.Fatal(err)
	}
	period := productcontrol.Period(time.Now())
	quote := func(starts int64) string {
		return productcontrol.Quote(account.ID, app.ID, tenant.ID, "export", catalog, customer, period, starts, time.Now())
	}
	admit := func(key, quote string) (Operation, error) {
		op, _, err := s.AdmitOperation(ctx, OperationAdmission{AccountID: account.ID, PlatformTenantID: tenant.ID, DefinitionID: def.ID, ProductFeature: "export",
			ProductControlEnabled: true, RequireProductQuote: true, ExpectedProductQuote: quote, Input: json.RawMessage(`{}`), IdempotencyKey: key})
		return op, err
	}
	price := func(op Operation) int64 { return op.ProductControl.UnitPriceMillicents }

	firstQuote := quote(0)
	for i, key := range []string{"included-1", "included-2"} {
		op, err := admit(key, firstQuote)
		if err != nil || price(op) != 0 || op.ProductControl.Admission.IncludedStarts != 2 || op.ProductControl.Admission.StartsBefore != int64(i) {
			t.Fatalf("included start %d: %+v %v", i+1, op.ProductControl, err)
		}
	}
	// Both included starts are used: the price the customer confirmed no
	// longer applies.
	if _, err := admit("overage-1", firstQuote); !errors.Is(err, ErrProductQuoteChanged) {
		t.Fatalf("overage on an included-price quote: %v", err)
	}
	if op, err := admit("overage-1", quote(2)); err != nil || price(op) != 20000 {
		t.Fatalf("overage start: %+v %v", op.ProductControl, err)
	}
	if _, err := admit("over-limit", quote(3)); !errors.Is(err, ErrOperationQuota) {
		t.Fatalf("start beyond monthly_starts: %v", err)
	}

	// Pro includes five starts: three of this month's are used.
	if customer, err = s.SetProductCustomer(ctx, account.ID, app.ID, api.ProductCustomer{TenantID: tenant.ID, Plan: "pro"}); err != nil {
		t.Fatal(err)
	}
	if op, err := admit("pro-included", quote(3)); err != nil || price(op) != 0 || op.ProductControl.Plan != "pro" {
		t.Fatalf("pro start within its included starts: %+v %v", op.ProductControl, err)
	}
	if op, err := admit("pro-included-2", quote(4)); err != nil || price(op) != 0 {
		t.Fatalf("last pro included start: %+v %v", op.ProductControl, err)
	}
	if op, err := admit("pro-overage", quote(5)); err != nil || price(op) != 15000 {
		t.Fatalf("pro overage start: %+v %v", op.ProductControl, err)
	}
}
