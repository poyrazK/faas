package state_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/productcontrol"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProductControlCustomerConsentMemory(t *testing.T) {
	productCustomerConsent(t, state.NewMemStore())
}
func TestProductControlCustomerConsentPostgres(t *testing.T) {
	productCustomerConsent(t, state.XNewPgStore(t))
}

func productCustomerConsent(t *testing.T, s state.ProductContractStore) {
	t.Helper()
	ctx := t.Context()
	account, err := s.CreateAccount(ctx, uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "product-consent", Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:consent"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	def, err := s.PutOperationDefinition(ctx, state.OperationDefinition{AccountID: account.ID, OperationDefinitionResponse: api.OperationDefinitionResponse{AppID: app.ID, DeploymentID: dep.ID, Scope: dep.Scope, Spec: api.OperationDefinitionSpec{Name: "export", Method: "POST", Path: "/exports", Owner: api.OperationOwnerPlatformTenant, ProgressStages: []string{"generating"}, InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	tenant, _, err := s.CreatePlatformTenant(ctx, account.ID, "alice", "Alice", 100)
	if err != nil {
		t.Fatal(err)
	}
	other, _, err := s.CreatePlatformTenant(ctx, account.ID, "bob", "Bob", 100)
	if err != nil {
		t.Fatal(err)
	}
	spec := api.ProductCatalogSpec{AuthApp: "product-auth", Currency: "EUR", Features: map[string]api.ProductFeature{"export": {Label: "Export", DefinitionID: def.ID}}, Plans: map[string]api.ProductPlan{"starter": {Label: "Starter", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 10, UnitPriceMillicents: 25000}}}}}
	catalog, err := s.PutProductCatalog(ctx, account.ID, app.ID, spec)
	if err != nil {
		t.Fatal(err)
	}
	org := uuid.NewString()
	customer, err := s.SetProductCustomer(ctx, account.ID, app.ID, api.ProductCustomer{TenantID: tenant.ID, Plan: "starter", AuthOrganizationID: org})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetProductCustomer(ctx, account.ID, app.ID, api.ProductCustomer{TenantID: other.ID, Plan: "starter", AuthOrganizationID: org}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("duplicate organization binding: %v", err)
	}
	for _, id := range []string{uuid.Nil.String(), "ABCDEF00-1234-4567-89ab-0123456789ab", "not-a-uuid"} {
		if _, err := s.SetProductCustomer(ctx, account.ID, app.ID, api.ProductCustomer{TenantID: other.ID, Plan: "starter", AuthOrganizationID: id}); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("invalid organization %q: %v", id, err)
		}
	}
	mapped, err := s.ProductCustomerByAuthOrganization(ctx, account.ID, app.ID, org)
	if err != nil || mapped.TenantID != tenant.ID {
		t.Fatalf("organization mapping: %+v %v", mapped, err)
	}
	if _, err := s.ProductCustomerByAuthOrganization(ctx, uuid.NewString(), app.ID, org); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("foreign mapping: %v", err)
	}
	quote := productcontrol.Quote(account.ID, app.ID, tenant.ID, "export", catalog, customer, productcontrol.Period(time.Now()))
	req := state.OperationAdmission{AccountID: account.ID, PlatformTenantID: tenant.ID, DefinitionID: def.ID, ProductFeature: "export", ProductControlEnabled: true, RequireProductQuote: true, ProductAuthOrganizationID: org, ProductAuthApp: spec.AuthApp, Input: json.RawMessage(`{}`), IdempotencyKey: "consented"}
	if _, _, err := s.AdmitOperation(ctx, req); !errors.Is(err, state.ErrProductQuoteChanged) {
		t.Fatalf("unconfirmed admission: %v", err)
	}
	req.ExpectedProductQuote = productcontrol.Quote(account.ID, app.ID, other.ID, "export", catalog, customer, productcontrol.Period(time.Now()))
	if _, _, err := s.AdmitOperation(ctx, req); !errors.Is(err, state.ErrProductQuoteChanged) {
		t.Fatalf("foreign quote admission: %v", err)
	}
	req.ExpectedProductQuote = quote
	first, created, err := s.AdmitOperation(ctx, req)
	if err != nil || !created || first.ProductControl.Admission.ConfirmedQuote != quote {
		t.Fatalf("confirmed admission: %+v %t %v", first, created, err)
	}
	plan := spec.Plans["starter"]
	grant := plan.Features["export"]
	grant.UnitPriceMillicents = 50000
	plan.Features["export"] = grant
	spec.Plans["starter"] = plan
	newer, err := s.PutProductCatalog(ctx, account.ID, app.ID, spec)
	if err != nil {
		t.Fatal(err)
	}
	customer.Revision = newer.Revision
	customer, err = s.SetProductCustomer(ctx, account.ID, app.ID, customer)
	if err != nil {
		t.Fatal(err)
	}
	stale := req
	stale.IdempotencyKey = "stale-price"
	if _, _, err := s.AdmitOperation(ctx, stale); !errors.Is(err, state.ErrProductQuoteChanged) {
		t.Fatalf("changed terms silently accepted: %v", err)
	}
	usage, err := s.GetProductUsage(ctx, account.ID, app.ID, tenant.ID, productcontrol.Period(time.Now()))
	if err != nil || len(usage.Lines) != 1 || usage.Lines[0].Starts != 1 {
		t.Fatalf("rejection consumed quota: %+v %v", usage, err)
	}
	retry, created, err := s.AdmitOperation(ctx, req)
	if err != nil || created || retry.ID != first.ID || retry.ProductControl.UnitPriceMillicents != 25000 {
		t.Fatalf("retry repriced or duplicated: %+v %t %v", retry, created, err)
	}
	lookup := state.ProductFeatureRequest{AccountID: account.ID, AppID: app.ID, TenantID: tenant.ID, Feature: "export", IdempotencyKey: req.IdempotencyKey}
	observation, err := s.LookupProductSubmission(ctx, lookup)
	if err != nil || observation.State != "accepted" || observation.Receipt.ID != first.ID {
		t.Fatalf("lost acknowledgement: %+v %v", observation, err)
	}
	lookup.TenantID = other.ID
	observation, err = s.LookupProductSubmission(ctx, lookup)
	if err != nil || observation.State != "unresolved" || observation.Receipt != nil {
		t.Fatalf("foreign lookup disclosed receipt: %+v %v", observation, err)
	}
	// A mapping changed after HTTP authentication is checked again under the
	// admission locks, including retained duplicate requests.
	customer.AuthOrganizationID = ""
	if _, err := s.SetProductCustomer(ctx, account.ID, app.ID, customer); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AdmitOperation(ctx, req); !errors.Is(err, state.ErrProductAccess) {
		t.Fatalf("unlinked organization retained admission: %v", err)
	}
	customer.AuthOrganizationID = org
	if _, err := s.SetProductCustomer(ctx, account.ID, app.ID, customer); err != nil {
		t.Fatal(err)
	}
	stale.ExpectedProductQuote = productcontrol.Quote(account.ID, app.ID, tenant.ID, "export", newer, customer, productcontrol.Period(time.Now()))
	second, created, err := s.AdmitOperation(ctx, stale)
	if err != nil || !created || second.ProductControl.UnitPriceMillicents != 50000 {
		t.Fatalf("reconfirmed admission: %+v %t %v", second, created, err)
	}
	spec.AuthApp = "replacement-auth"
	if _, err := s.PutProductCatalog(ctx, account.ID, app.ID, spec); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AdmitOperation(ctx, req); !errors.Is(err, state.ErrProductAccess) {
		t.Fatalf("old realm retained admission: %v", err)
	}
}
