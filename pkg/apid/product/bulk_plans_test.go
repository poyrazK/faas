package product

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/productcontrol"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

type bulkPlanLostResponseStore struct {
	MemStore *state.MemStore
	loseFor  string
}

func (s *bulkPlanLostResponseStore) ApplyProductCustomerChange(ctx context.Context, account, app string, req api.ProductCustomerChangeRequest) (api.ProductCustomerChangeReceipt, error) {
	receipt, err := s.MemStore.ApplyProductCustomerChange(ctx, account, app, req)
	if err == nil && req.Customer.TenantID == s.loseFor {
		s.loseFor = ""
		return api.ProductCustomerChangeReceipt{}, errors.New("confirmation response lost after commit")
	}
	return receipt, err
}

type bulkPlanTestHost struct {
	customerTestHost
	stores *statetest.Mem
}

func (h bulkPlanTestHost) Store() state.Store { return h.stores }

func TestProductControlBulkPlanReview(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "bulk-plan@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "bulk-plans"})
	if err != nil {
		t.Fatal(err)
	}
	spec := api.ProductCatalogSpec{Currency: "EUR", Features: map[string]api.ProductFeature{
		"export":  {Label: "Export", Operation: &api.ProductOperationBinding{Name: "export", Scope: api.DefaultEnvScope}},
		"archive": {Label: "Archive", Operation: &api.ProductOperationBinding{Name: "archive", Scope: api.DefaultEnvScope}},
	}, Plans: map[string]api.ProductPlan{"starter": {Label: "Starter", Features: map[string]api.ProductFeatureGrant{
		"export": {MonthlyStarts: 5, UnitPriceMillicents: 25_000}, "archive": {MonthlyStarts: 5},
	}}}}
	old, err := store.PutProductCatalog(ctx, acct.ID, app.ID, spec)
	if err != nil {
		t.Fatal(err)
	}
	customers := map[string]api.ProductCustomer{}
	for _, name := range []string{"alice", "bob", "unselected"} {
		tenant, _, err := store.CreatePlatformTenant(ctx, acct.ID, name, name, 100)
		if err != nil {
			t.Fatal(err)
		}
		customer, err := store.SetProductCustomer(ctx, acct.ID, app.ID, api.ProductCustomer{TenantID: tenant.ID, Plan: "starter", AuthOrganizationID: uuid.NewString()})
		if err != nil {
			t.Fatal(err)
		}
		customers[name] = customer
	}
	alice, bob, unselected := customers["alice"], customers["bob"], customers["unselected"]
	now := time.Now().UTC()
	previousRun := uuid.NewString()
	store.SeedProductOperationForTest(state.Operation{
		AccountID: acct.ID, AppID: app.ID, PlatformTenantID: alice.TenantID,
		OperationResponse: api.OperationResponse{ID: previousRun, State: api.OperationAccepted},
		ProductControl:    &api.ProductOperationReceipt{Revision: old.Revision, Feature: "export", Plan: "starter", Currency: "EUR", Period: productcontrol.Period(now), AdmittedAt: now, UnitPriceMillicents: 25_000},
	})
	spec.Plans["starter"] = api.ProductPlan{Label: "Starter", MonthlyFeeMillicents: 900_000, Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 1, UnitPriceMillicents: 50_000}}}
	head, err := store.PutProductCatalog(ctx, acct.ID, app.ID, spec)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := session.NewEphemeralManager(time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	var view dashboard.ProductControlData
	s := &Service{Host: bulkPlanTestHost{customerTestHost{store}, statetest.NewMem(&bulkPlanLostResponseStore{MemStore: store, loseFor: alice.TenantID})},
		OwnerSessions: func() *session.Manager { return manager },
		OwnerRender: func(w http.ResponseWriter, _ *http.Request, _ state.Account, data dashboard.ProductControlData) {
			view = data
			if err := dashboard.Render(w, slog.Default(), "test", dashboard.Page{Title: "Product Control", Body: "product_control", Data: data}); err != nil {
				t.Fatal(err)
			}
		},
	}
	jar := map[string]*http.Cookie{}
	call := func(method string, handler func(http.ResponseWriter, *http.Request, state.Account), form url.Values, origin string, status int) string {
		t.Helper()
		view = dashboard.ProductControlData{}
		r := httptest.NewRequest(method, "https://gregale.test/", strings.NewReader(form.Encode()))
		r.SetPathValue("slug", app.Slug)
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", origin)
		for _, cookie := range jar {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		handler(w, r, acct)
		if w.Code != status {
			t.Fatalf("%s: status %d, want %d: %s", method, w.Code, status, view.Error)
		}
		for _, cookie := range w.Result().Cookies() {
			jar[cookie.Name] = cookie
		}
		return w.Body.String()
	}
	current := func(tenant, revision string) api.ProductCustomer {
		t.Helper()
		got, err := store.GetProductCustomer(ctx, acct.ID, app.ID, tenant)
		if err != nil || got.Revision != revision || got.Plan != "starter" {
			t.Fatalf("unexpected assignment: %+v %v", got, err)
		}
		return got
	}
	const origin = "https://gregale.test"
	call("GET", s.OwnerBulkPlans, nil, "", 200)
	list := view.BulkPlans
	if list.Target != head.Revision || len(list.Groups) != 1 || len(list.Groups[0].Customers) != 3 {
		t.Fatalf("unexpected candidate groups: %+v", list)
	}
	selection := url.Values{"plan": {"starter"}, "source": {old.Revision}, "target": {head.Revision}, "tenant": {alice.TenantID, bob.TenantID}, "csrf_token": {list.Token}}
	call("POST", s.OwnerBulkPlansReview, selection, "https://other.test", 403)
	selection.Add("tenant", alice.TenantID)
	call("POST", s.OwnerBulkPlansReview, selection, origin, 400)
	selection["tenant"] = []string{alice.TenantID, bob.TenantID}
	page := call("POST", s.OwnerBulkPlansReview, selection, origin, 200)
	for _, want := range []string{"Confirm 2 changes", "Plan fee for", "export next run", "1 used · 0 remaining", "Feature access removed for archive"} {
		if !strings.Contains(page, want) {
			t.Fatalf("rendered review omitted %q", want)
		}
	}
	review := view.BulkPlans
	if len(review.Reviews) != 2 || review.ConfirmCount != 2 || review.Payload == "" {
		t.Fatalf("unexpected review: %+v", review)
	}
	for _, row := range review.Reviews {
		if row.TenantID == alice.TenantID && !strings.Contains(strings.Join(row.Warnings, " "), "Feature access removed for archive. No starts left under the proposed allowance for export.") {
			t.Fatalf("missing customer warnings: %+v", row)
		}
	}
	current(alice.TenantID, old.Revision)
	current(bob.TenantID, old.Revision)
	form := url.Values{"payload": {review.Payload + "A"}, "csrf_token": {review.Token}}
	call("POST", s.OwnerBulkPlansConfirm, form, origin, 403)
	current(alice.TenantID, old.Revision)

	bob.Suspended = true
	if _, err := store.SetProductCustomer(ctx, acct.ID, app.ID, bob); err != nil {
		t.Fatal(err)
	}
	form.Set("payload", review.Payload)
	page = call("POST", s.OwnerBulkPlansConfirm, form, origin, 200)
	if !strings.Contains(page, "Retry this confirmation") || !strings.Contains(page, "Review this workspace again") {
		t.Fatal("rendered results omitted retry and fresh review actions")
	}
	if view.BulkPlans.Payload != review.Payload || len(view.BulkPlans.Results) != 2 {
		t.Fatalf("missing recovery for lost confirmation: %+v", view.BulkPlans)
	}
	if got := current(alice.TenantID, head.Revision); got.AuthOrganizationID != alice.AuthOrganizationID || got.Suspended {
		t.Fatalf("bulk change modified customer access: %+v", got)
	}
	if got := current(bob.TenantID, old.Revision); !got.Suspended {
		t.Fatalf("stale customer change applied: %+v", got)
	}
	current(unselected.TenantID, old.Revision)

	call("POST", s.OwnerBulkPlansConfirm, form, origin, 200)
	var receiptID string
	for _, row := range view.BulkPlans.Results {
		if row.TenantID == alice.TenantID {
			receiptID = row.ReceiptID
		} else if row.ReceiptID != "" || !strings.Contains(row.Message, "Review this workspace again") {
			t.Fatalf("stale customer was not reported: %+v", row)
		}
	}
	if receiptID == "" || view.BulkPlans.Payload != "" {
		t.Fatalf("retry did not return the saved outcome: %+v", view.BulkPlans)
	}
	if _, err := store.SetProductCustomer(ctx, acct.ID, app.ID, alice); err != nil {
		t.Fatal(err)
	}
	call("POST", s.OwnerBulkPlansConfirm, form, origin, 200)
	for _, row := range view.BulkPlans.Results {
		if row.TenantID == alice.TenantID && row.ReceiptID != receiptID {
			t.Fatal("retry created a different receipt")
		}
	}
	current(alice.TenantID, old.Revision)
	accepted, err := store.GetProductReceipt(ctx, acct.ID, app.ID, previousRun)
	if err != nil || accepted.Receipt.Revision != old.Revision || accepted.Receipt.UnitPriceMillicents != 25_000 {
		t.Fatalf("bulk change repriced accepted work: %+v %v", accepted, err)
	}
}
