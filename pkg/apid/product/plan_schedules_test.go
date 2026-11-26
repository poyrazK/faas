package product

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProductControlOwnerPlanSchedule(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "plan-schedule@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "schedule-plans"})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := store.PutProductCatalog(ctx, account.ID, app.ID, api.ProductCatalogSpec{
		Currency: "EUR",
		Features: map[string]api.ProductFeature{
			"export": {Label: "Export", Operation: &api.ProductOperationBinding{Name: "export", Scope: api.DefaultEnvScope}},
		},
		Plans: map[string]api.ProductPlan{
			"starter": {Label: "Starter", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 1}}},
			"pro":     {Label: "Pro", MonthlyFeeMillicents: 900_000, Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 3}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	tenant, _, err := store.CreatePlatformTenant(ctx, account.ID, "workspace", "Workspace", 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetProductCustomer(ctx, account.ID, app.ID, api.ProductCustomer{TenantID: tenant.ID, Plan: "starter"}); err != nil {
		t.Fatal(err)
	}
	manager, err := session.NewEphemeralManager(time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	var view dashboard.ProductControlData
	svc := Service{
		Host:          customerTestHost{store},
		OwnerSessions: func() *session.Manager { return manager },
		OwnerRender: func(w http.ResponseWriter, _ *http.Request, _ state.Account, data dashboard.ProductControlData) {
			view = data
			if err := dashboard.Render(w, slog.Default(), "test", dashboard.Page{Title: "Product Control", Body: "product_control", Data: data}); err != nil {
				t.Fatal(err)
			}
		},
	}
	const origin = "https://gregale.test"
	cookies := map[string]*http.Cookie{}
	call := func(handler func(http.ResponseWriter, *http.Request, state.Account), form url.Values, want ...string) {
		t.Helper()
		method, path := http.MethodPost, "/"
		if form == nil {
			method, path = http.MethodGet, "/?tenant="+tenant.ID
		}
		r := httptest.NewRequest(method, origin+path, strings.NewReader(form.Encode()))
		r.SetPathValue("slug", app.Slug)
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", origin)
		for _, cookie := range cookies {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		handler(w, r, account)
		if w.Code != http.StatusOK {
			t.Fatalf("response = %d: %s", w.Code, view.Error)
		}
		for _, cookie := range w.Result().Cookies() {
			cookies[cookie.Name] = cookie
		}
		for _, value := range want {
			if !strings.Contains(w.Body.String(), value) {
				t.Errorf("rendered console omitted %q", value)
			}
		}
	}
	confirm := func(want ...string) {
		t.Helper()
		c := view.Confirmation
		if c == nil {
			t.Fatal("missing reviewed confirmation")
		}
		call(svc.OwnerConfirm, url.Values{"csrf_token": {c.Token}, "payload": {c.Payload}}, want...)
	}
	call(svc.OwnerConsole, nil, "Start plan at midnight UTC")
	call(svc.OwnerReview, url.Values{
		"csrf_token": {view.ReviewToken}, "action": {"plan"}, "tenant": {tenant.ID}, "period": {view.Period},
		"plan": {"pro"}, "revision": {catalog.Revision}, "suspended": {"false"}, "next_month": {"true"},
	}, "Confirm scheduled plan change", "Projected plan fee", "Scheduled start")
	confirm("Scheduled plan change")
	call(svc.OwnerConsole, nil, "Pending", "Review cancellation")
	if view.Customer.Plan != "starter" || view.PlanSchedule.After.Plan != "pro" {
		t.Fatalf("assignment = %+v, schedule = %+v", view.Customer, view.PlanSchedule)
	}
	call(svc.OwnerReview, url.Values{
		"csrf_token": {view.ReviewToken}, "action": {"cancel_schedule"}, "tenant": {tenant.ID}, "period": {view.Period}, "id": {view.PlanSchedule.ID},
	}, "Cancel scheduled plan change")
	confirm("was cancelled")
	call(svc.OwnerConsole, nil, "Cancelled")
}
