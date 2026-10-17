// ADR-1543: a customer confirms reviewed terms without resetting consumed starts.
package state

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type productPlanTestStore interface {
	productContractStore
	ProductSelfServiceStore
	SetPlatformTenantStatus(context.Context, string, string, string) (PlatformTenant, error)
	UpdateAccountStatus(context.Context, string, AccountStatus) error
}

type productPlanFixture struct {
	scope     ProductPlanScope
	spec      api.ProductCatalogSpec
	customer  api.ProductCustomer
	admission OperationAdmission
}

func newProductPlanFixture(ctx context.Context, t *testing.T, s productPlanTestStore) productPlanFixture {
	t.Helper()
	a, err := s.CreateAccount(ctx, uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, App{AccountID: a.ID, Slug: "plans-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	auth, err := s.CreateApp(ctx, App{AccountID: a.ID, Slug: "auth-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := s.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:plans"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	def, err := s.PutOperationDefinition(ctx, OperationDefinition{AccountID: a.ID, OperationDefinitionResponse: api.OperationDefinitionResponse{AppID: app.ID, DeploymentID: dep.ID, Scope: dep.Scope, Spec: api.OperationDefinitionSpec{Name: "export", Method: "POST", Path: "/exports", Owner: api.OperationOwnerPlatformTenant, ProgressStages: []string{"exporting"}, InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	spec := api.ProductCatalogSpec{AuthApp: auth.Slug, Currency: "EUR", Features: map[string]api.ProductFeature{"export": {Label: "Export", DefinitionID: def.ID}}, Plans: map[string]api.ProductPlan{"starter": {Label: "Starter", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 1, UnitPriceMillicents: 25000}}}, "pro": {Label: "Pro", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 10, UnitPriceMillicents: 10000}}}}, Access: &api.ProductAccessPolicy{Roles: map[string][]api.ProductPermission{"manager": {api.ProductPermissionManagePlan}, "billing": {api.ProductPermissionViewBilling}, "operator": {api.ProductPermissionRunWork}}}, SelfService: &api.ProductPlanPolicy{Transitions: map[string][]string{"starter": {"pro"}, "pro": {"starter"}}}}
	if _, err = s.PutProductCatalog(ctx, a.ID, app.ID, spec); err != nil {
		t.Fatal(err)
	}
	tenant, _, err := s.CreatePlatformTenant(ctx, a.ID, "customer", "Customer", 100)
	if err != nil {
		t.Fatal(err)
	}
	org := uuid.NewString()
	c, err := s.SetProductCustomer(ctx, a.ID, app.ID, api.ProductCustomer{TenantID: tenant.ID, Plan: "starter", AuthOrganizationID: org})
	if err != nil {
		t.Fatal(err)
	}
	return productPlanFixture{scope: ProductPlanScope{AccountID: a.ID, AppID: app.ID, TenantID: tenant.ID, AuthApp: auth.Slug, AuthAppID: auth.ID, OrganizationID: org, Role: "manager"}, spec: spec, customer: c, admission: OperationAdmission{AccountID: a.ID, PlatformTenantID: tenant.ID, DefinitionID: def.ID, ProductFeature: "export", ProductControlEnabled: true, Input: json.RawMessage(`{}`)}}
}

func productPlanReview(ctx context.Context, t *testing.T, s ProductSelfServiceStore, scope ProductPlanScope, plan string) api.ProductPlanChangeConfirmation {
	t.Helper()
	p, err := s.PreviewProductPlanChange(ctx, scope, api.ProductPlanSelection{ID: uuid.NewString(), Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	return api.ProductPlanChangeConfirmation{Request: p.Request, ExpectedHash: p.Hash}
}

func TestProductControlSelfServiceMemory(t *testing.T) { productPlansContract(t, NewMemStore()) }

func productPlansContract(t *testing.T, s productPlanTestStore) {
	t.Helper()
	ctx := t.Context()
	t.Run("reviewed concurrency, pinned prices and carried usage", func(t *testing.T) {
		f := newProductPlanFixture(ctx, t, s)
		a := f.admission
		a.IdempotencyKey = "before-upgrade"
		old, fresh, err := s.AdmitOperation(ctx, a)
		if err != nil || !fresh {
			t.Fatal("first start", err)
		}
		options, err := s.GetProductPlanOptions(ctx, f.scope)
		if err != nil || len(options.Offers) != 1 || options.Offers[0].Plan != "pro" || options.Current.Features[0].Starts != 1 || options.Offers[0].Features[0].RemainingStarts != 9 {
			t.Fatalf("choices: %+v %v", options, err)
		}
		confirm := productPlanReview(ctx, t, s, f.scope, "pro")
		if got, err := s.GetProductCustomer(ctx, f.scope.AccountID, f.scope.AppID, f.scope.TenantID); err != nil || got != f.customer {
			t.Fatal("preview mutated customer", err)
		}
		var wg sync.WaitGroup
		results := make(chan api.ProductPlanChangeReceipt, 8)
		for range 8 {
			wg.Go(func() {
				r, e := s.ApplyProductPlanChange(ctx, f.scope, confirm)
				if e != nil {
					t.Error(e)
				}
				results <- r
			})
		}
		wg.Wait()
		close(results)
		var receipt api.ProductPlanChangeReceipt
		for r := range results {
			if receipt.ID == "" {
				receipt = r
			} else if !reflect.DeepEqual(receipt, r) {
				t.Fatal("retry changed receipt")
			}
		}
		if receipt.ID != confirm.Request.ID || receipt.Preview.AfterCustomer.AuthOrganizationID != f.scope.OrganizationID || receipt.Preview.After.Features[0].Starts != 1 {
			t.Fatalf("receipt: %+v", receipt)
		}
		a.IdempotencyKey = "after-upgrade"
		current, fresh, err := s.AdmitOperation(ctx, a)
		if err != nil || !fresh || current.ProductControl.UnitPriceMillicents != 10000 {
			t.Fatal("new start did not use new terms", err)
		}
		evidence, err := s.GetProductReceipt(ctx, f.scope.AccountID, f.scope.AppID, old.ID)
		if err != nil || evidence.Receipt.UnitPriceMillicents != 25000 {
			t.Fatal("accepted price changed", err)
		}
		down := productPlanReview(ctx, t, s, f.scope, "starter")
		r, err := s.ApplyProductPlanChange(ctx, f.scope, down)
		if err != nil || r.Preview.After.Features[0].Starts != 2 || r.Preview.After.Features[0].RemainingStarts != 0 {
			t.Fatalf("downgrade reset usage: %+v %v", r, err)
		}
		a.IdempotencyKey = "after-downgrade"
		if _, _, err = s.AdmitOperation(ctx, a); !errors.Is(err, ErrOperationQuota) {
			t.Fatal("downgrade allowed exhausted allowance", err)
		}
		if retry, e := s.ApplyProductPlanChange(ctx, f.scope, confirm); e != nil || !reflect.DeepEqual(retry, receipt) {
			t.Fatal("old retry did not recover receipt", e)
		}
		got, err := s.GetProductCustomer(ctx, f.scope.AccountID, f.scope.AppID, f.scope.TenantID)
		if err != nil || got.Plan != "starter" {
			t.Fatal("old retry reapplied upgrade", err)
		}
		changed := confirm
		changed.ExpectedHash = productOwnerHash("different")
		if _, err = s.ApplyProductPlanChange(ctx, f.scope, changed); !errors.Is(err, ErrConflict) {
			t.Fatal("ID reuse accepted", err)
		}
		receipt.Preview.After.Features[0].Starts = 999
		stored, err := s.GetProductPlanChange(ctx, f.scope, confirm.Request.ID)
		if err != nil || stored.Preview.After.Features[0].Starts != 1 {
			t.Fatal("receipt aliases caller", err)
		}
		page, err := s.ListProductPlanChanges(ctx, f.scope, "")
		if err != nil || len(page.Changes) != 2 || page.Changes[0].ID != down.Request.ID {
			t.Fatal("duplicate or missing history", err)
		}
	})
	t.Run("fresh guard at commit", func(t *testing.T) {
		for _, what := range []string{"permission", "offers", "realm", "organization", "customer suspension", "tenant suspension", "account suspension", "usage", "owner assignment"} {
			t.Run(what, func(t *testing.T) {
				f := newProductPlanFixture(ctx, t, s)
				confirm := productPlanReview(ctx, t, s, f.scope, "pro")
				switch what {
				case "permission":
					f.spec.Access.Roles["manager"] = []api.ProductPermission{}
					_, err := s.PutProductCatalog(ctx, f.scope.AccountID, f.scope.AppID, f.spec)
					if err != nil {
						t.Fatal(err)
					}
				case "offers":
					f.spec.SelfService.Transitions = map[string][]string{}
					_, err := s.PutProductCatalog(ctx, f.scope.AccountID, f.scope.AppID, f.spec)
					if err != nil {
						t.Fatal(err)
					}
				case "realm":
					f.spec.AuthApp = "another-auth"
					_, err := s.PutProductCatalog(ctx, f.scope.AccountID, f.scope.AppID, f.spec)
					if err != nil {
						t.Fatal(err)
					}
				case "organization":
					f.customer.AuthOrganizationID = uuid.NewString()
					_, err := s.SetProductCustomer(ctx, f.scope.AccountID, f.scope.AppID, f.customer)
					if err != nil {
						t.Fatal(err)
					}
				case "customer suspension":
					f.customer.Suspended = true
					_, err := s.SetProductCustomer(ctx, f.scope.AccountID, f.scope.AppID, f.customer)
					if err != nil {
						t.Fatal(err)
					}
				case "tenant suspension":
					_, err := s.SetPlatformTenantStatus(ctx, f.scope.AccountID, f.scope.TenantID, PlatformTenantSuspended)
					if err != nil {
						t.Fatal(err)
					}
				case "account suspension":
					if err := s.UpdateAccountStatus(ctx, f.scope.AccountID, AccountSuspended); err != nil {
						t.Fatal(err)
					}
				case "usage":
					a := f.admission
					a.IdempotencyKey = "racing-start"
					if _, _, err := s.AdmitOperation(ctx, a); err != nil {
						t.Fatal(err)
					}
				case "owner assignment":
					f.customer.Plan = "pro"
					_, err := s.SetProductCustomer(ctx, f.scope.AccountID, f.scope.AppID, f.customer)
					if err != nil {
						t.Fatal(err)
					}
				}
				if _, err := s.ApplyProductPlanChange(ctx, f.scope, confirm); err == nil {
					t.Fatal("stale review was applied")
				}
				got, err := s.GetProductCustomer(ctx, f.scope.AccountID, f.scope.AppID, f.scope.TenantID)
				if err != nil || got != f.customer {
					t.Fatal("failed apply changed assignment", err)
				}
			})
		}
	})
	t.Run("separate permission, allowed transitions, and scoped receipts", func(t *testing.T) {
		f := newProductPlanFixture(ctx, t, s)
		for _, role := range []string{"owner", "billing", "operator", "member", "unknown"} {
			scope := f.scope
			scope.Role = role
			if _, err := s.GetProductPlanOptions(ctx, scope); !errors.Is(err, ErrProductAccess) {
				t.Fatal("ungranted role", role, err)
			}
		}
		if _, err := s.PreviewProductPlanChange(ctx, f.scope, api.ProductPlanSelection{ID: uuid.NewString(), Plan: "starter"}); !errors.Is(err, ErrProductPlanNotOffered) {
			t.Fatal("no-op offered", err)
		}
		wrong := f.scope
		wrong.AuthAppID = uuid.NewString()
		if _, err := s.GetProductPlanOptions(ctx, wrong); !errors.Is(err, ErrProductAccess) {
			t.Fatal("Auth app replacement accepted", err)
		}
		confirm := productPlanReview(ctx, t, s, f.scope, "pro")
		if _, err := s.ApplyProductPlanChange(ctx, f.scope, confirm); err != nil {
			t.Fatal(err)
		}
		other, _, err := s.CreatePlatformTenant(ctx, f.scope.AccountID, "other", "Other", 100)
		if err != nil {
			t.Fatal(err)
		}
		otherScope := f.scope
		otherScope.TenantID = other.ID
		otherScope.OrganizationID = uuid.NewString()
		if _, err = s.SetProductCustomer(ctx, f.scope.AccountID, f.scope.AppID, api.ProductCustomer{TenantID: other.ID, Plan: "starter", AuthOrganizationID: otherScope.OrganizationID}); err != nil {
			t.Fatal(err)
		}
		if _, err = s.GetProductPlanChange(ctx, otherScope, confirm.Request.ID); !errors.Is(err, ErrNotFound) {
			t.Fatal("foreign receipt exposed", err)
		}
		collision := productPlanReview(ctx, t, s, otherScope, "pro")
		collision.Request.ID = confirm.Request.ID
		p, err := s.PreviewProductPlanChange(ctx, otherScope, collision.Request)
		if err != nil {
			t.Fatal(err)
		}
		collision.ExpectedHash = p.Hash
		if _, err = s.ApplyProductPlanChange(ctx, otherScope, collision); !errors.Is(err, ErrConflict) {
			t.Fatal("foreign change ID reused", err)
		}
		got, err := s.GetProductCustomer(ctx, f.scope.AccountID, f.scope.AppID, other.ID)
		if err != nil || got.Plan != "starter" {
			t.Fatal("receipt collision leaked assignment write", err)
		}
		f.spec.SelfService = nil
		if _, err = s.PutProductCatalog(ctx, f.scope.AccountID, f.scope.AppID, f.spec); err != nil {
			t.Fatal(err)
		}
		if _, err = s.GetProductPlanChange(ctx, f.scope, confirm.Request.ID); err != nil {
			t.Fatal("closed offers prevented read recovery", err)
		}
		f.spec.Access = nil
		if _, err = s.PutProductCatalog(ctx, f.scope.AccountID, f.scope.AppID, f.spec); err != nil {
			t.Fatal(err)
		}
		defaults := f.scope
		defaults.Role = "owner"
		if _, err = s.GetProductPlanOptions(ctx, defaults); !errors.Is(err, ErrProductAccess) {
			t.Fatal("Manage plan granted by default", err)
		}
		if _, err = s.ApplyProductPlanChange(ctx, f.scope, confirm); !errors.Is(err, ErrProductAccess) {
			t.Fatal("revoked permission recovered private receipt", err)
		}
	})
	t.Run("competing reviews and paginated immutable history", func(t *testing.T) {
		f := newProductPlanFixture(ctx, t, s)
		first, second := productPlanReview(ctx, t, s, f.scope, "pro"), productPlanReview(ctx, t, s, f.scope, "pro")
		var wg sync.WaitGroup
		outcomes := make(chan error, 2)
		for _, confirmation := range []api.ProductPlanChangeConfirmation{first, second} {
			wg.Go(func() { _, err := s.ApplyProductPlanChange(ctx, f.scope, confirmation); outcomes <- err })
		}
		wg.Wait()
		close(outcomes)
		success := 0
		for err := range outcomes {
			if err == nil {
				success++
			} else if !errors.Is(err, ErrProductPlanNotOffered) {
				t.Fatal(err)
			}
		}
		if success != 1 {
			t.Fatal("competing reviews both applied")
		}
		for i := range api.ProductControlOwnerPageSize {
			plan := "starter"
			if i%2 == 1 {
				plan = "pro"
			}
			c := productPlanReview(ctx, t, s, f.scope, plan)
			if _, err := s.ApplyProductPlanChange(ctx, f.scope, c); err != nil {
				t.Fatal(err)
			}
		}
		page, err := s.ListProductPlanChanges(ctx, f.scope, "")
		if err != nil || len(page.Changes) != api.ProductControlOwnerPageSize || page.NextCursor == "" {
			t.Fatalf("first page: %d %v", len(page.Changes), err)
		}
		next, err := s.ListProductPlanChanges(ctx, f.scope, page.NextCursor)
		if err != nil || len(next.Changes) != 1 || next.NextCursor != "" {
			t.Fatalf("next page: %+v %v", next, err)
		}
		for _, r := range page.Changes {
			if r.ID == next.Changes[0].ID {
				t.Fatal("cursor repeated change")
			}
		}
		wrong := f.scope
		wrong.TenantID = uuid.NewString()
		if _, err = s.ListProductPlanChanges(ctx, wrong, page.NextCursor); !errors.Is(err, ErrInvalidArgument) {
			t.Fatal("cross-tenant cursor accepted", err)
		}
		page.Changes[0].Preview.After.Features[0].Label = "tampered"
		stored, err := s.GetProductPlanChange(ctx, f.scope, page.Changes[0].ID)
		if err != nil || stored.Preview.After.Features[0].Label == "tampered" {
			t.Fatal("history aliases retained receipt", err)
		}
	})
}

func TestProductControlPlanReviewBindsUTCMonth(t *testing.T) {
	f := api.ProductFeature{Label: "Export", DefinitionID: uuid.NewString()}
	spec := api.ProductCatalogSpec{Features: map[string]api.ProductFeature{"export": f}, Plans: map[string]api.ProductPlan{"starter": {Label: "Starter"}, "pro": {Label: "Pro"}}, SelfService: &api.ProductPlanPolicy{Transitions: map[string][]string{"starter": {"pro"}}}}
	v := productPlanView{head: api.ProductCatalog{Revision: productOwnerHash("head"), Spec: spec}, current: api.ProductCatalog{Spec: spec}, customer: api.ProductCustomer{Plan: "starter"}, starts: map[string]int64{}, period: "2026-10"}
	selection := api.ProductPlanSelection{ID: uuid.NewString(), Plan: "pro"}
	before, err := productPlanPreview(ProductPlanScope{}, v, selection)
	if err != nil {
		t.Fatal(err)
	}
	v.period = time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC).Format("2006-01")
	after, err := productPlanPreview(ProductPlanScope{}, v, selection)
	if err != nil || after.Hash == before.Hash {
		t.Fatal("month rollover did not invalidate review", err)
	}
}
