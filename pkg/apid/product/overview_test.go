package product

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type overviewProducts struct {
	state.ProductControlStore
	usage func(context.Context, string, string, string, string) (api.ProductUsage, error)
}

func (p overviewProducts) GetProductUsage(ctx context.Context, account, app, tenant, period string) (api.ProductUsage, error) {
	return p.usage(ctx, account, app, tenant, period)
}

func overviewFixture(t *testing.T) (*Service, *state.MemStore, state.Account, state.App, api.ProductCustomer) {
	t.Helper()
	ctx, store := t.Context(), state.NewMemStore()
	acct, err := store.CreateAccount(ctx, uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "overview-product"})
	if err != nil {
		t.Fatal(err)
	}
	auth, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "overview-auth"})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:overview"})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	def, err := store.PutOperationDefinition(ctx, state.OperationDefinition{AccountID: acct.ID, OperationDefinitionResponse: api.OperationDefinitionResponse{AppID: app.ID, DeploymentID: dep.ID, Scope: dep.Scope, Spec: api.OperationDefinitionSpec{Name: "export", Method: "POST", Path: "/export", Owner: api.OperationOwnerPlatformTenant, ProgressStages: []string{"exporting"}, InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.PutProductCatalog(ctx, acct.ID, app.ID, api.ProductCatalogSpec{AuthApp: auth.Slug, Currency: "EUR", Features: map[string]api.ProductFeature{"export": {Label: "Export", DefinitionID: def.ID}}, Plans: map[string]api.ProductPlan{"starter": {Label: "Starter", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 1, UnitPriceMillicents: 25000}}}}})
	if err != nil {
		t.Fatal(err)
	}
	tenant, _, err := store.CreatePlatformTenant(ctx, acct.ID, "alice", "Alice <internal>", 100)
	if err != nil {
		t.Fatal(err)
	}
	customer, err := store.SetProductCustomer(ctx, acct.ID, app.ID, api.ProductCustomer{TenantID: tenant.ID, Plan: "starter", AuthOrganizationID: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{Host: customerTestHost{store}}
	s.AuthOrigin = func(context.Context, state.Account, string) (state.App, string, error) {
		return auth, "https://overview-auth.example.com", nil
	}
	s.OperationDoctor = func(_ context.Context, _ state.Account, d state.OperationDefinition, tenant state.PlatformTenant) (api.OperationDoctorResponse, error) {
		return api.OperationDoctorResponse{AppID: d.AppID, DeploymentID: d.DeploymentID, PlatformTenantID: tenant.ID, Checks: []api.OperationDoctorCheck{{Check: "trust", Status: "configured", Impact: "submission", Code: "trust_configured"}}}, nil
	}
	return s, store, acct, app, customer
}

func TestProductControlCustomerOverview(t *testing.T) {
	s, store, acct, app, customer := overviewFixture(t)
	ctx := t.Context()
	page, err := s.customerOverview(ctx, acct, app, store, "")
	if err != nil || len(page.Customers) != 1 || page.Customers[0].Readiness.ConfigurationState != "observed" || page.Customers[0].Activation.State != "not_recorded" || page.QualificationState != "unverified" {
		t.Fatalf("overview: %+v %v", page, err)
	}
	// Reading the overview must not create an onboarding receipt or claim a visit.
	if _, err = store.ProductOnboardingByTenant(ctx, acct.ID, app.ID, customer.TenantID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("overview created activation")
	}
	wrapped := overviewProducts{ProductControlStore: store, usage: func(context.Context, string, string, string, string) (api.ProductUsage, error) {
		return api.ProductUsage{Lines: []api.ProductUsageLine{{Feature: "export", Starts: 1}}}, nil
	}}
	page, err = s.customerOverview(ctx, acct, app, wrapped, "")
	if err != nil || page.Customers[0].Readiness.ConfigurationState != "blocked" {
		t.Fatalf("allowance ignored: %+v %v", page, err)
	}
	found := false
	for _, c := range page.Customers[0].Readiness.Checks {
		found = found || c.Code == "allowance_exhausted"
	}
	if !found {
		t.Fatal("exhausted feature missing")
	}
	for i := 0; i < api.ProductControlOverviewPageSize+1; i++ {
		tenant, _, err := store.CreatePlatformTenant(ctx, acct.ID, uuid.NewString(), "Pilot", 100)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = store.SetProductCustomer(ctx, acct.ID, app.ID, api.ProductCustomer{TenantID: tenant.ID, Plan: "starter"}); err != nil {
			t.Fatal(err)
		}
	}
	seen, cursor := map[string]bool{}, ""
	for {
		page, err = s.customerOverview(ctx, acct, app, store, cursor)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Customers) > api.ProductControlOverviewPageSize {
			t.Fatal("page bound exceeded")
		}
		for _, row := range page.Customers {
			if seen[row.Customer.TenantID] {
				t.Fatal("pagination repeated customer")
			}
			seen[row.Customer.TenantID] = true
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if len(seen) != api.ProductControlOverviewPageSize+2 {
		t.Fatalf("pagination skipped customers: %d", len(seen))
	}
	if _, err = s.customerOverview(ctx, acct, app, store, "malformed"); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("bad cursor: %v", err)
	}
}

func TestProductControlOverviewChangedAndUnverified(t *testing.T) {
	s, store, acct, app, customer := overviewFixture(t)
	s.OperationDoctor = nil
	page, err := s.customerOverview(t.Context(), acct, app, store, "")
	if err != nil || page.Customers[0].Readiness.ConfigurationState != "unverified" {
		t.Fatalf("missing diagnostics declared ready: %+v %v", page, err)
	}
	products := overviewProducts{ProductControlStore: store, usage: func(ctx context.Context, _, _, _, _ string) (api.ProductUsage, error) {
		changed := customer
		changed.Suspended = true
		_, err := store.SetProductCustomer(ctx, acct.ID, app.ID, changed)
		return api.ProductUsage{}, err
	}}
	if _, err = s.customerOverview(t.Context(), acct, app, products, ""); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("mixed assignments presented as one observation: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err = s.customerOverview(ctx, acct, app, store, ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled check returned success: %v", err)
	}
	row := api.ProductCustomerOverview{Customer: customer, Readiness: api.ProductDoctorReport{ConfigurationState: "blocked"}, Activation: api.ProductCustomerActivation{State: "setup_changed"}, Work: api.ProductCustomerWorkSummary{Unresolved: 1, NeedsReview: 1, ReviewPeriod: "2026-09"}}
	actions := customerOverviewActions(app.Slug, row, "2026-10")
	if len(actions) != 3 || !strings.Contains(actions[2].URL, "period=2026-09") {
		t.Fatalf("review does not target historical work: %+v", actions)
	}
}

func TestProductControlOverviewUsesRecordedActivation(t *testing.T) {
	s, store, acct, app, customer := overviewFixture(t)
	ctx := t.Context()
	auth, _, err := s.AuthOrigin(ctx, acct, "overview-auth")
	if err != nil {
		t.Fatal(err)
	}
	p, err := store.PreviewProductOnboarding(ctx, acct.ID, app.ID, api.ProductOnboardingRequest{ID: uuid.NewString(), TenantID: customer.TenantID, Plan: customer.Plan, AuthApp: auth.Slug, AuthAppID: auth.ID, OrganizationID: customer.AuthOrganizationID, OrganizationName: "Alice workspace"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ApplyProductOnboarding(ctx, acct.ID, app.ID, api.ProductOnboardingConfirmation{Request: p.Request, ExpectedHash: p.Hash}); err != nil {
		t.Fatal(err)
	}
	page, err := s.customerOverview(ctx, acct, app, store, "")
	if err != nil || page.Customers[0].Activation.State != "awaiting_workspace_visit" || page.Customers[0].Activation.AccessVerifiedAt != nil {
		t.Fatalf("setup was mistaken for customer access: %+v %v", page, err)
	}
	if err = store.ObserveProductCustomerAccess(ctx, acct.ID, app.ID, customer.TenantID, auth.Slug, customer.AuthOrganizationID); err != nil {
		t.Fatal(err)
	}
	page, err = s.customerOverview(ctx, acct, app, store, "")
	if err != nil || page.Customers[0].Activation.State != "awaiting_first_success" || page.Customers[0].Activation.AccessVerifiedAt == nil || page.Customers[0].Activation.FirstSuccess != nil {
		t.Fatalf("visit was mistaken for success: %+v %v", page, err)
	}
	raw, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), p.Hash) || strings.Contains(string(raw), "preview_hash") {
		t.Fatal("private review payload leaked into overview")
	}
	customer.Suspended = true
	if _, err = store.SetProductCustomer(ctx, acct.ID, app.ID, customer); err != nil {
		t.Fatal(err)
	}
	page, err = s.customerOverview(ctx, acct, app, store, "")
	if err != nil || page.Customers[0].Activation.State != "setup_changed" || page.Customers[0].Readiness.ConfigurationState != "blocked" {
		t.Fatalf("stale setup declared active: %+v %v", page, err)
	}
}

func TestProductControlOverviewResponseBoundKeepsContinuation(t *testing.T) {
	s, store, acct, app, _ := overviewFixture(t)
	for range 2 {
		tenant, _, err := store.CreatePlatformTenant(t.Context(), acct.ID, uuid.NewString(), "Large diagnostics", 100)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = store.SetProductCustomer(t.Context(), acct.ID, app.ID, api.ProductCustomer{TenantID: tenant.ID, Plan: "starter"}); err != nil {
			t.Fatal(err)
		}
	}
	s.OperationDoctor = func(_ context.Context, _ state.Account, d state.OperationDefinition, tenant state.PlatformTenant) (api.OperationDoctorResponse, error) {
		checks := make([]api.OperationDoctorCheck, 100)
		for i := range checks {
			checks[i] = api.OperationDoctorCheck{Check: "trust", Status: "configured", Impact: "submission", Code: "trust_configured", Message: strings.Repeat("diagnostic", 1800)}
		}
		return api.OperationDoctorResponse{AppID: d.AppID, DeploymentID: d.DeploymentID, PlatformTenantID: tenant.ID, Checks: checks}, nil
	}
	seen, cursor := map[string]bool{}, ""
	for {
		page, err := s.customerOverview(t.Context(), acct, app, store, cursor)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(page)
		if err != nil || len(raw)+1 > api.ProductControlOverviewMaxResponseBytes || len(page.Customers) != 1 {
			t.Fatalf("oversized overview page: %d bytes, %d rows, %v", len(raw), len(page.Customers), err)
		}
		id := page.Customers[0].Customer.TenantID
		if seen[id] {
			t.Fatal("response size pagination repeated a customer")
		}
		seen[id] = true
		if page.NextCursor == "" {
			break
		}
		if page.NextCursor != id {
			t.Fatal("response size pagination skipped a customer")
		}
		cursor = page.NextCursor
	}
	if len(seen) != 3 {
		t.Fatalf("large report hid customers: %d", len(seen))
	}
}
