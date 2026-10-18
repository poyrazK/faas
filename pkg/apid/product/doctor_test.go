package product

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProductControlDoctorObservations(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "doctor-product@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "doctor-product"})
	if err != nil {
		t.Fatal(err)
	}
	s := Service{Host: customerTestHost{store}}
	missing, err := s.doctorReport(ctx, acct, app, store, "")
	if err != nil || missing.ConfigurationState != "blocked" || missing.QualificationState != "unverified" {
		t.Fatalf("missing catalog: %+v %v", missing, err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:doctor"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	def, err := store.PutOperationDefinition(ctx, state.OperationDefinition{AccountID: acct.ID, OperationDefinitionResponse: api.OperationDefinitionResponse{AppID: app.ID, DeploymentID: dep.ID, Scope: dep.Scope, Spec: api.OperationDefinitionSpec{Name: "export", Method: "POST", Path: "/export", Owner: api.OperationOwnerPlatformTenant, ProgressStages: []string{"exporting"}, InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	spec := api.ProductCatalogSpec{AuthApp: "product-auth", Currency: "EUR", Features: map[string]api.ProductFeature{"export": {Label: "Export", DefinitionID: def.ID}}, Plans: map[string]api.ProductPlan{"starter": {Label: "Starter", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 10, UnitPriceMillicents: 25000}}}}}
	if _, err := store.PutProductCatalog(ctx, acct.ID, app.ID, spec); err != nil {
		t.Fatal(err)
	}
	tenant, _, err := store.CreatePlatformTenant(ctx, acct.ID, "alice", "Alice", 100)
	if err != nil {
		t.Fatal(err)
	}
	customer, err := store.SetProductCustomer(ctx, acct.ID, app.ID, api.ProductCustomer{TenantID: tenant.ID, Plan: "starter", AuthOrganizationID: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	s.AuthOrigin = func(context.Context, state.Account, string) (state.App, string, error) {
		return state.App{}, "https://product-auth.example.com", nil
	}
	status := "configured"
	s.OperationDoctor = func(_ context.Context, _ state.Account, d state.OperationDefinition, tenant state.PlatformTenant) (api.OperationDoctorResponse, error) {
		return api.OperationDoctorResponse{AppID: d.AppID, DeploymentID: d.DeploymentID, PlatformTenantID: tenant.ID, Checks: []api.OperationDoctorCheck{{Check: "workload_trust", Status: status, Impact: "submission", Code: "workload_trust_configured"}, {Check: "native_lifecycle", Status: "unknown", Impact: "qualification", Code: "native_lifecycle_unverified"}}}, nil
	}
	for _, want := range []string{"configured", "unknown", "blocked"} {
		status = want
		report, err := s.doctorReport(ctx, acct, app, store, tenant.ID)
		expected := want
		if want == "configured" {
			expected = "observed"
		}
		if want == "unknown" {
			expected = "unverified"
		}
		if err != nil || report.ConfigurationState != expected || report.QualificationState != "unverified" {
			t.Fatalf("node status %s: %+v %v", want, report, err)
		}
	}
	status = "configured"
	unscoped, err := s.doctorReport(ctx, acct, app, store, "")
	if err != nil || unscoped.ConfigurationState != "unverified" {
		t.Fatalf("unscoped inferred customer readiness: %+v %v", unscoped, err)
	}
	before, err := store.OperationPendingCount(ctx, acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.doctorReport(ctx, acct, app, store, uuid.NewString()); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("foreign tenant: %v", err)
	}
	customer.Suspended = true
	if _, err := store.SetProductCustomer(ctx, acct.ID, app.ID, customer); err != nil {
		t.Fatal(err)
	}
	report, err := s.doctorReport(ctx, acct, app, store, tenant.ID)
	if err != nil || report.ConfigurationState != "blocked" {
		t.Fatalf("suspension missed: %+v %v", report, err)
	}
	for _, check := range report.Checks {
		if check.Check == "binding" && check.Status != "observed" {
			t.Fatal("suspension was mislabeled as binding failure")
		}
	}
	after, err := store.OperationPendingCount(ctx, acct.ID)
	if err != nil || after != before {
		t.Fatal("diagnostics created work")
	}
}
