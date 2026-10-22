// ADR-1707: verified workspace onboarding, retained setup and activation evidence.
package state_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type productOnboardingTestStore interface {
	state.ProductOnboardingStore
	state.ProductControlStore
	CreateAccount(context.Context, string, api.Plan) (state.Account, error)
	CreateApp(context.Context, state.App) (state.App, error)
	CreatePlatformTenant(context.Context, string, string, string, int) (state.PlatformTenant, bool, error)
	ResolvePlatformTenantExternalRef(context.Context, string, string) (state.PlatformTenant, error)
	GetPlatformTenant(context.Context, string, string) (state.PlatformTenant, error)
	SetPlatformTenantStatus(context.Context, string, string, string) (state.PlatformTenant, error)
}

func TestProductControlOnboardingMemory(t *testing.T) {
	s := state.NewMemStore()
	insert := func(op state.Operation) {
		(*s.Mu()).Lock()
		defer (*s.Mu()).Unlock()
		s.InsertProductOperationLocked(op)
	}
	settle := func(op state.Operation) {
		(*s.Mu()).Lock()
		defer (*s.Mu()).Unlock()
		s.SettleProductOperationLocked(op)
	}
	retireAuth := func(id string) { (*s.Mu()).Lock(); defer (*s.Mu()).Unlock(); delete((*s.Apps()), id) }
	productOnboardingContract(t, s, insert, settle, retireAuth)
}

func productOnboardingContract(t *testing.T, s productOnboardingTestStore, insert, settle func(state.Operation), retireAuth func(string)) {
	t.Helper()
	ctx := t.Context()
	a, err := s.CreateAccount(ctx, uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: a.ID, Slug: "onboard-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	auth, err := s.CreateApp(ctx, state.App{AccountID: a.ID, Slug: "auth-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	spec := api.ProductCatalogSpec{AuthApp: auth.Slug, Currency: "EUR", Features: map[string]api.ProductFeature{"export": {Label: "Export", Operation: &api.ProductOperationBinding{Name: "export", Scope: api.DefaultEnvScope}}}, Plans: map[string]api.ProductPlan{"starter": {Label: "Starter", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 10, UnitPriceMillicents: 25000}}}, "pro": {Label: "Pro", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 20, UnitPriceMillicents: 10000}}}}}
	catalog, err := s.PutProductCatalog(ctx, a.ID, app.ID, spec)
	if err != nil {
		t.Fatal(err)
	}
	request := func(ref string) api.ProductOnboardingRequest {
		return api.ProductOnboardingRequest{ID: uuid.NewString(), ExternalRef: ref, Name: ref, Plan: "starter", AuthApp: auth.Slug, AuthAppID: auth.ID, OrganizationID: uuid.NewString(), OrganizationName: "Customer workspace"}
	}
	preview := func(req api.ProductOnboardingRequest) api.ProductOnboardingConfirmation {
		t.Helper()
		p, e := s.PreviewProductOnboarding(ctx, a.ID, app.ID, req)
		if e != nil {
			t.Fatal(e)
		}
		return api.ProductOnboardingConfirmation{Request: p.Request, ExpectedHash: p.Hash}
	}

	t.Run("atomic creation and retained confirmation", func(t *testing.T) {
		req := request("alice")
		c := preview(req)
		if _, err := s.ResolvePlatformTenantExternalRef(ctx, a.ID, req.ExternalRef); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("preview created tenant: %v", err)
		}
		bad := c
		bad.ExpectedHash = state.ProductOwnerHash("stale")
		if _, err := s.ApplyProductOnboarding(ctx, a.ID, app.ID, bad); !errors.Is(err, state.ErrProductOnboardingStale) {
			t.Fatalf("stale review: %v", err)
		}
		if _, err := s.ResolvePlatformTenantExternalRef(ctx, a.ID, req.ExternalRef); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("failed apply leaked tenant: %v", err)
		}
		var wg sync.WaitGroup
		results := make(chan api.ProductOnboardingReceipt, 8)
		failures := make(chan error, 8)
		for range 8 {
			wg.Go(func() { r, e := s.ApplyProductOnboarding(ctx, a.ID, app.ID, c); results <- r; failures <- e })
		}
		wg.Wait()
		close(results)
		close(failures)
		for e := range failures {
			if e != nil {
				t.Fatal(e)
			}
		}
		var receipt api.ProductOnboardingReceipt
		for r := range results {
			if receipt.ID == "" {
				receipt = r
			} else if !reflect.DeepEqual(receipt, r) {
				t.Fatal("retry changed receipt")
			}
		}
		if !receipt.CreatedTenant || receipt.Customer.Revision != catalog.Revision || receipt.Customer.AuthOrganizationID != req.OrganizationID {
			t.Fatalf("receipt: %+v", receipt)
		}
		tenant, e := s.ResolvePlatformTenantExternalRef(ctx, a.ID, req.ExternalRef)
		if e != nil || tenant.ID != receipt.Tenant.ID {
			t.Fatalf("tenant: %+v %v", tenant, e)
		}
		status, e := s.GetProductOnboarding(ctx, a.ID, app.ID, req.ID)
		if e != nil || !status.SetupCurrent || !status.AccessConfigured || status.AccessVerifiedAt != nil || status.FirstSuccess != nil {
			t.Fatalf("premature activation: %+v %v", status, e)
		}
		if e = s.ObserveProductCustomerAccess(ctx, a.ID, app.ID, tenant.ID, auth.Slug, uuid.NewString()); !errors.Is(e, state.ErrProductAccess) {
			t.Fatalf("foreign access observation: %v", e)
		}
		if e = s.ObserveProductCustomerAccess(ctx, a.ID, app.ID, tenant.ID, auth.Slug, req.OrganizationID); e != nil {
			t.Fatal(e)
		}
		observed, e := s.GetProductOnboarding(ctx, a.ID, app.ID, req.ID)
		if e != nil || observed.AccessVerifiedAt == nil {
			t.Fatalf("access evidence: %+v %v", observed, e)
		}
		if e = s.ObserveProductCustomerAccess(ctx, a.ID, app.ID, tenant.ID, auth.Slug, req.OrganizationID); e != nil {
			t.Fatal(e)
		}
		op := state.Operation{AccountID: a.ID, AppID: app.ID, PlatformTenantID: tenant.ID, OperationResponse: api.OperationResponse{ID: uuid.NewString(), State: api.OperationRequiresReconciliation, UpdatedAt: receipt.CreatedAt.Add(2 * time.Second)}, ProductControl: &api.ProductOperationReceipt{Revision: catalog.Revision, Feature: "export", Plan: "starter", Currency: "EUR", Period: receipt.CreatedAt.Format("2006-01"), UnitPriceMillicents: 25000, AdmittedAt: receipt.CreatedAt.Add(time.Second)}}
		insert(op)
		settle(op)
		status, e = s.GetProductOnboarding(ctx, a.ID, app.ID, req.ID)
		if e != nil || status.FirstSuccess != nil {
			t.Fatalf("unknown run marked successful: %+v %v", status, e)
		}
		op.State = api.OperationSucceeded
		settle(op)
		status, e = s.ProductOnboardingByTenant(ctx, a.ID, app.ID, tenant.ID)
		if e != nil || status.FirstSuccess == nil || status.FirstSuccess.OperationID != op.ID || !status.AccessVerifiedAt.Equal(*observed.AccessVerifiedAt) {
			t.Fatalf("activation: %+v %v", status, e)
		}
		later := receipt.Customer
		later.Plan = "pro"
		later.Suspended = true
		if _, e = s.SetProductCustomer(ctx, a.ID, app.ID, later); e != nil {
			t.Fatal(e)
		}
		retry, e := s.ApplyProductOnboarding(ctx, a.ID, app.ID, c)
		if e != nil || !reflect.DeepEqual(retry, receipt) {
			t.Fatalf("lost-response replay: %+v %v", retry, e)
		}
		current, e := s.GetProductCustomer(ctx, a.ID, app.ID, tenant.ID)
		if e != nil || current != later {
			t.Fatalf("replay changed current plan: %+v %v", current, e)
		}
		status, e = s.GetProductOnboarding(ctx, a.ID, app.ID, req.ID)
		if e != nil || status.SetupCurrent || status.AccessConfigured || status.AccessVerifiedAt != nil || status.FirstSuccess == nil {
			t.Fatalf("changed assignment status: %+v %v", status, e)
		}
		mutated := c
		mutated.Request.Plan = "pro"
		if _, e = s.ApplyProductOnboarding(ctx, a.ID, app.ID, mutated); !errors.Is(e, state.ErrConflict) {
			t.Fatalf("reused ID: %v", e)
		}
		if _, e = s.GetProductOnboarding(ctx, uuid.NewString(), app.ID, req.ID); !errors.Is(e, state.ErrNotFound) {
			t.Fatalf("foreign owner: %v", e)
		}
	})

	t.Run("existing customer keeps pinned terms and gains first link", func(t *testing.T) {
		tenant, _, e := s.CreatePlatformTenant(ctx, a.ID, "existing", "Existing customer", 100)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = s.SetProductCustomer(ctx, a.ID, app.ID, api.ProductCustomer{TenantID: tenant.ID, Plan: "starter"}); e != nil {
			t.Fatal(e)
		}
		changed := state.CloneProductBilling(spec)
		p := changed.Plans["starter"]
		g := p.Features["export"]
		g.UnitPriceMillicents++
		p.Features["export"] = g
		changed.Plans["starter"] = p
		if _, e = s.PutProductCatalog(ctx, a.ID, app.ID, changed); e != nil {
			t.Fatal(e)
		}
		req := request("")
		req.ExternalRef = ""
		req.Name = ""
		req.TenantID = tenant.ID
		c := preview(req)
		if c.Request.Revision != catalog.Revision {
			t.Fatal("existing price revision migrated")
		}
		r, e := s.ApplyProductOnboarding(ctx, a.ID, app.ID, c)
		if e != nil || r.CreatedTenant || r.Customer.Revision != catalog.Revision {
			t.Fatalf("existing setup: %+v %v", r, e)
		}
		other := request("duplicate-org")
		other.OrganizationID = req.OrganizationID
		if _, e = s.PreviewProductOnboarding(ctx, a.ID, app.ID, other); !errors.Is(e, state.ErrConflict) {
			t.Fatalf("shared organization: %v", e)
		}
		req.ID = uuid.NewString()
		req.OrganizationID = uuid.NewString()
		if _, e = s.PreviewProductOnboarding(ctx, a.ID, app.ID, req); !errors.Is(e, state.ErrConflict) {
			t.Fatalf("identity replacement: %v", e)
		}
		if _, e = s.PutProductCatalog(ctx, a.ID, app.ID, spec); e != nil {
			t.Fatal(e)
		}
	})

	t.Run("changed catalog and realm do not create customers", func(t *testing.T) {
		for _, realmChange := range []bool{false, true} {
			req := request("stale-" + uuid.NewString())
			c := preview(req)
			changed := state.CloneProductBilling(spec)
			if realmChange {
				changed.AuthApp = "other-auth"
			} else {
				p := changed.Plans["starter"]
				p.Label = "Changed"
				changed.Plans["starter"] = p
			}
			if _, e := s.PutProductCatalog(ctx, a.ID, app.ID, changed); e != nil {
				t.Fatal(e)
			}
			if _, e := s.ApplyProductOnboarding(ctx, a.ID, app.ID, c); !errors.Is(e, state.ErrProductOnboardingStale) {
				t.Fatalf("stale apply: %v", e)
			}
			if _, e := s.ResolvePlatformTenantExternalRef(ctx, a.ID, req.ExternalRef); !errors.Is(e, state.ErrNotFound) {
				t.Fatalf("stale apply created customer: %v", e)
			}
			if _, e := s.PutProductCatalog(ctx, a.ID, app.ID, spec); e != nil {
				t.Fatal(e)
			}
		}
	})

	t.Run("competing setups cannot duplicate a customer", func(t *testing.T) {
		req := request("concurrent")
		first := preview(req)
		req.ID = uuid.NewString()
		second := preview(req)
		var wg sync.WaitGroup
		results := make(chan error, 2)
		for _, c := range []api.ProductOnboardingConfirmation{first, second} {
			wg.Go(func() { _, e := s.ApplyProductOnboarding(ctx, a.ID, app.ID, c); results <- e })
		}
		wg.Wait()
		close(results)
		passed, stale := 0, 0
		for e := range results {
			if e == nil {
				passed++
			} else if errors.Is(e, state.ErrProductOnboardingStale) {
				stale++
			} else {
				t.Fatal(e)
			}
		}
		if passed != 1 || stale != 1 {
			t.Fatalf("results: passed=%d stale=%d", passed, stale)
		}
	})

	t.Run("suspended and foreign tenants are rejected", func(t *testing.T) {
		tenant, _, e := s.CreatePlatformTenant(ctx, a.ID, "suspended", "Suspended", 100)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = s.SetPlatformTenantStatus(ctx, a.ID, tenant.ID, state.PlatformTenantSuspended); e != nil {
			t.Fatal(e)
		}
		req := request("")
		req.ExternalRef = ""
		req.Name = ""
		req.TenantID = tenant.ID
		if _, e = s.PreviewProductOnboarding(ctx, a.ID, app.ID, req); !errors.Is(e, state.ErrProductAccess) {
			t.Fatalf("suspended: %v", e)
		}
		req.TenantID = uuid.NewString()
		if _, e = s.PreviewProductOnboarding(ctx, a.ID, app.ID, req); !errors.Is(e, state.ErrNotFound) {
			t.Fatalf("foreign tenant: %v", e)
		}
	})

	t.Run("setup receipt survives Auth app retirement", func(t *testing.T) {
		realm, e := s.CreateApp(ctx, state.App{AccountID: a.ID, Slug: "retired-auth-" + uuid.NewString()[:8]})
		if e != nil {
			t.Fatal(e)
		}
		changed := state.CloneProductBilling(spec)
		changed.AuthApp = realm.Slug
		if _, e = s.PutProductCatalog(ctx, a.ID, app.ID, changed); e != nil {
			t.Fatal(e)
		}
		req := request("retired-auth-history")
		req.AuthApp, req.AuthAppID = realm.Slug, realm.ID
		c := preview(req)
		r, e := s.ApplyProductOnboarding(ctx, a.ID, app.ID, c)
		if e != nil {
			t.Fatal(e)
		}
		retireAuth(realm.ID)
		status, e := s.GetProductOnboarding(ctx, a.ID, app.ID, r.ID)
		if e != nil || status.Receipt.ID != r.ID || status.AccessConfigured || !status.SetupCurrent {
			t.Fatalf("retired realm erased/misrepresented setup: %+v %v", status, e)
		}
		retry, e := s.ApplyProductOnboarding(ctx, a.ID, app.ID, c)
		if e != nil || !reflect.DeepEqual(retry, r) {
			t.Fatalf("retired realm blocked retained receipt: %+v %v", retry, e)
		}
	})
}

func TestProductControlOnboardingQuotaBoundary(t *testing.T) {
	req := api.ProductOnboardingRequest{Plan: "starter", AuthApp: "auth"}
	catalog := api.ProductCatalog{Revision: "revision", Spec: api.ProductCatalogSpec{AuthApp: "auth", Plans: map[string]api.ProductPlan{"starter": {}}}}
	_, err := state.ProductOnboardingPreview("account", "app", req, catalog, catalog, nil, nil, 1, 1, time.Now())
	var quota *state.PlatformTenantQuotaError
	if !errors.As(err, &quota) {
		t.Fatalf("new customer bypassed quota: %v", err)
	}
	tenant := state.PlatformTenant{Status: state.PlatformTenantActive}
	if _, err = state.ProductOnboardingPreview("account", "app", req, catalog, catalog, &tenant, nil, 1, 1, time.Now()); err != nil {
		t.Fatalf("existing customer consumed new quota: %v", err)
	}
	boundary := time.Date(2026, time.October, 31, 23, 59, 59, 0, time.UTC)
	before, e := state.ProductOnboardingPreview("account", "app", req, catalog, catalog, &tenant, nil, 1, 1, boundary)
	if e != nil {
		t.Fatal(e)
	}
	after, e := state.ProductOnboardingPreview("account", "app", req, catalog, catalog, &tenant, nil, 1, 1, boundary.Add(2*time.Second))
	if e != nil {
		t.Fatal(e)
	}
	if before.Hash == after.Hash {
		t.Fatal("confirmation hash did not bind the UTC month")
	}
}
