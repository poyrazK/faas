package apidserver_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProductControlCustomerPortalPilot(t *testing.T) {
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	ctx := t.Context()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "product-customers@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "product-customers", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	authApp, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "product-customer-auth", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:product-customer"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	def, err := store.PutOperationDefinition(ctx, state.OperationDefinition{AccountID: account.ID, OperationDefinitionResponse: api.OperationDefinitionResponse{AppID: app.ID, DeploymentID: dep.ID, Scope: dep.Scope, Spec: api.OperationDefinitionSpec{Name: "customer-export", Method: "POST", Path: "/exports", Owner: api.OperationOwnerPlatformTenant, ProgressStages: []string{"generating"}, InputSchema: json.RawMessage(`{"type":"object","required":["count"],"properties":{"count":{"type":"integer","minimum":1,"maximum":1000}},"additionalProperties":false}`), OutputSchema: json.RawMessage(`{"type":"object"}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	spec := api.ProductCatalogSpec{AuthApp: authApp.Slug, Currency: "EUR", Features: map[string]api.ProductFeature{"export": {Label: "CSV export", Operation: &api.ProductOperationBinding{Name: def.Spec.Name, Scope: dep.Scope}}}, Plans: map[string]api.ProductPlan{
		"starter": {Label: "Starter", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 10, UnitPriceMillicents: 25000}}},
		"pro":     {Label: "Pro", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 20, UnitPriceMillicents: 10000}}},
	}}
	if _, err := store.PutProductCatalog(ctx, account.ID, app.ID, spec); err != nil {
		t.Fatal(err)
	}
	tenants := make([]state.PlatformTenant, 3)
	orgs := []string{uuid.NewString(), uuid.NewString(), uuid.NewString()}
	for i := range tenants {
		tenants[i], _, err = store.CreatePlatformTenant(ctx, account.ID, "customer-"+strconv.Itoa(i), "Customer "+strconv.Itoa(i), 100)
		if err != nil {
			t.Fatal(err)
		}
		plan := "starter"
		if i == 1 {
			plan = "pro"
		}
		if _, err := store.SetProductCustomer(ctx, account.ID, app.ID, api.ProductCustomer{TenantID: tenants[i].ID, Plan: plan, AuthOrganizationID: orgs[i]}); err != nil {
			t.Fatal(err)
		}
	}
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/auth/v1/token" {
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			for i := range tenants {
				if body["email"] == "customer-"+strconv.Itoa(i)+"@example.com" && body["password"] == "password" {
					_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "customer-" + strconv.Itoa(i) + ".access.jwt", "refresh_token": "refresh-" + strconv.Itoa(i), "expires_in": 600})
					return
				}
			}
			w.WriteHeader(400)
			return
		}
		for i := range tenants {
			if r.Header.Get("Authorization") == "Bearer customer-"+strconv.Itoa(i)+".access.jwt" {
				_ = json.NewEncoder(w).Encode(map[string]any{"organizations": []map[string]string{{"id": orgs[i], "name": tenants[i].Name, "role": "owner"}}})
				return
			}
		}
		w.WriteHeader(401)
	}))
	defer upstream.Close()
	srv := apidserver.NewServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "example.test", apidserver.NoopNotifier{}).WithProductControlEnabled(true)
	srv.Operation().AdmissionEnabled = true
	srv.ProductControl().AuthClient = upstream.Client()
	srv.ProductControl().AuthOrigin = func(_ context.Context, acct state.Account, slug string) (state.App, string, error) {
		if acct.ID != account.ID || slug != authApp.Slug {
			return state.App{}, "", state.ErrNotFound
		}
		return authApp, upstream.URL, nil
	}
	handler := srv.Handler()
	base := "/v1/product-control/customer/" + app.Slug
	cookies := make([][]*http.Cookie, 3)
	call := func(method, path string, customer int, body any, key string) *httptest.ResponseRecorder {
		t.Helper()
		var data []byte
		if body != nil {
			data, err = json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
		}
		r := httptest.NewRequest(method, "https://portal.example"+path, strings.NewReader(string(data)))
		r.Header.Set("Origin", "https://portal.example")
		r.Header.Set("Content-Type", "application/json")
		if key != "" {
			r.Header.Set("Idempotency-Key", key)
		}
		if customer >= 0 {
			for _, cookie := range cookies[customer] {
				r.AddCookie(cookie)
			}
			r.Header.Set("X-Product-Organization", orgs[customer])
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	check := func(w *httptest.ResponseRecorder, status int) {
		t.Helper()
		if w.Code != status {
			t.Fatalf("want %d got %d: %s", status, w.Code, w.Body.String())
		}
	}
	views := make([]api.ProductPortal, 3)
	for i := range tenants {
		login := call("POST", base+"/session", -1, map[string]string{"email": "customer-" + strconv.Itoa(i) + "@example.com", "password": "password"}, "")
		check(login, 200)
		cookies[i] = login.Result().Cookies()
		w := call("GET", base+"/workspaces", i, nil, "")
		check(w, 200)
		if !strings.Contains(w.Body.String(), tenants[i].ID) {
			t.Fatal("automatic customer identity failed")
		}
		w = call("GET", base+"/product", i, nil, "")
		check(w, 200)
		if err := json.Unmarshal(w.Body.Bytes(), &views[i]); err != nil {
			t.Fatal(err)
		}
		if views[i].Customer.TenantID != tenants[i].ID || len(views[i].Catalog.Spec.Plans) != 1 || len(views[i].Quotes["export"]) != 64 {
			t.Fatalf("incorrect customer view: %+v", views[i])
		}
		w = call("GET", base+"/billing?period=2026-09&tenant_id="+tenants[(i+1)%len(tenants)].ID, i, nil, "")
		check(w, 200)
		var billing api.ProductCustomerBilling
		if err := json.Unmarshal(w.Body.Bytes(), &billing); err != nil || billing.Review.TenantID != tenants[i].ID {
			t.Fatalf("cookie billing identity: %s %v", w.Body.String(), err)
		}
	}
	check(call("GET", base+"/billing", -1, nil, ""), 401)
	check(call("GET", base+"/product", -1, nil, ""), 401)
	start := api.ProductStartRequest{Feature: "export", ExpectedQuote: views[0].Quotes["export"], Input: json.RawMessage(`{"count":2}`)}
	accepted := call("POST", base+"/start", 0, start, "saved-export")
	check(accepted, 202)
	var first api.OperationAcceptedResponse
	if err := json.Unmarshal(accepted.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if first.StatusURL != base+"/operations/"+first.ID || first.EventsURL != first.StatusURL+"/events" {
		t.Fatalf("acceptance escaped cookie namespace: %+v", first)
	}
	op, err := store.OperationByID(ctx, account.ID, tenants[0].ID, first.ID)
	if err != nil || op.ProductControl.Admission.ConfirmedQuote != start.ExpectedQuote {
		t.Fatalf("missing customer price consent: %+v %v", op, err)
	}
	check(call("GET", base+"/operations/"+first.ID, 0, nil, ""), 200)
	check(call("GET", base+"/operations/"+first.ID, 1, nil, ""), 404)
	check(call("GET", base+"/operations/"+first.ID+"/artifacts/"+uuid.NewString(), 1, nil, ""), 404)
	lookup := call("POST", base+"/submissions/lookup", 0, map[string]string{"feature": "export", "idempotency_key": "saved-export"}, "")
	check(lookup, 200)
	var observed api.OperationSubmissionLookupResponse
	if err := json.Unmarshal(lookup.Body.Bytes(), &observed); err != nil || observed.Receipt.ID != first.ID {
		t.Fatalf("lost acknowledgement: %s %v", lookup.Body.String(), err)
	}
	// Customer B cannot present customer A's quote, even with a valid login.
	check(call("POST", base+"/start", 1, start, "foreign-consent"), 409)
	start.ExpectedQuote = views[1].Quotes["export"]
	accepted = call("POST", base+"/start", 1, start, "pro-export")
	check(accepted, 202)
	var second api.OperationAcceptedResponse
	_ = json.Unmarshal(accepted.Body.Bytes(), &second)
	op, err = store.OperationByID(ctx, account.ID, tenants[1].ID, second.ID)
	if err != nil || op.ProductControl.UnitPriceMillicents != 10000 {
		t.Fatalf("wrong customer price: %+v %v", op, err)
	}
	check(call("POST", base+"/operations/"+second.ID+"/cancel", 1, api.OperationCancellationRequest{ExpectedGeneration: 1}, ""), 200)
	// Change terms after the page was loaded. A new request must not silently
	// consume allowance or use the new price.
	plan := spec.Plans["starter"]
	grant := plan.Features["export"]
	grant.UnitPriceMillicents = 50000
	plan.Features["export"] = grant
	spec.Plans["starter"] = plan
	if _, err := store.PutProductCatalog(ctx, account.ID, app.ID, spec); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetProductCustomer(ctx, account.ID, app.ID, api.ProductCustomer{TenantID: tenants[2].ID, Plan: "starter", AuthOrganizationID: orgs[2]}); err != nil {
		t.Fatal(err)
	}
	start.ExpectedQuote = views[2].Quotes["export"]
	check(call("POST", base+"/start", 2, start, "stale-price"), 409)
	w := call("GET", base+"/product", 2, nil, "")
	check(w, 200)
	var current api.ProductPortal
	_ = json.Unmarshal(w.Body.Bytes(), &current)
	if len(current.Usage.Lines) != 0 {
		t.Fatal("rejected quote consumed a start")
	}
	start.ExpectedQuote = current.Quotes["export"]
	check(call("POST", base+"/start", 2, start, "stale-price"), 202)
	// Force an organization unlink between HTTP authentication and the
	// Operation transaction. The transaction must reject that old identity.
	originalStart := srv.ProductControl().StartOperation
	srv.ProductControl().StartOperation = func(w http.ResponseWriter, r *http.Request, acct state.Account, req api.ProductStartRequest) {
		if _, err := store.SetProductCustomer(r.Context(), acct.ID, app.ID, api.ProductCustomer{TenantID: tenants[0].ID, Plan: "starter", Revision: views[0].Customer.Revision}); err != nil {
			t.Fatal(err)
		}
		originalStart(w, r, acct, req)
	}
	start.ExpectedQuote = views[0].Quotes["export"]
	check(call("POST", base+"/start", 0, start, "saved-export"), 403)
	srv.WithProductControlEnabled(false)
	for _, path := range []string{base + "/product", base + "/workspaces", base + "/operations/" + first.ID, "/v1/product-control/portal-core.js"} {
		check(call("GET", path, -1, nil, ""), 404)
	}
	check(call("POST", base+"/session", -1, map[string]any{}, ""), 404)
}
