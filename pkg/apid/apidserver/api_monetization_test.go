package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// monetizationRoutes samples every family of API monetization routes.
var monetizationRoutes = []struct{ method, path string }{
	{http.MethodGet, "/v1/apps/shop/rate-cards"},
	{http.MethodPost, "/v1/apps/shop/consumer-plans"},
	{http.MethodPut, "/v1/apps/shop/consumer-billing-policy"},
	{http.MethodGet, "/v1/apps/shop/consumer-overdue-holds"},
	{http.MethodGet, "/v1/apps/shop/consumers/c1/usage/quote"},
	{http.MethodGet, "/v1/apps/shop/consumers/c1/usage-statements"},
	{http.MethodPost, "/v1/apps/shop/consumers/c1/plan-assignments"},
	{http.MethodPost, "/v1/apps/shop/consumers/c1/credits"},
	{http.MethodPut, "/v1/apps/shop/consumers/c1/spend-cap"},
	{http.MethodDelete, "/v1/apps/shop/consumers/c1/trial"},
	{http.MethodPut, "/v1/account/billing-connections/stripe"},
	{http.MethodPost, "/v1/account/billing-runs"},
	{http.MethodGet, "/v1/account/billing-run-schedule"},
	{http.MethodGet, "/v1/account/statement-invoices"},
	{http.MethodGet, "/v1/account/platform-tenant-plans"},
	{http.MethodPut, "/v1/account/platform-tenant-billing-policy"},
	{http.MethodGet, "/v1/account/platform-tenants/t1/rate-cards"},
	{http.MethodPost, "/v1/account/platform-tenants/t1/usage-statements"},
	{http.MethodGet, "/v1/consumer-billing/shop"},
	{http.MethodGet, "/v1/platform-tenant-self/usage-statements"},
}

// adr: 20261018090210 — until API monetization is released its routes answer
// like unregistered ones, with or without credentials and for any method; its
// webhook events are not offered; the served spec omits it. Released, all of
// it returns.
func TestAPIMonetizationHiddenUntilReleased(t *testing.T) {
	t.Setenv("FAAS_API_MONETIZATION_ENABLED", "0")
	e := apidserver.Setup(t, api.PlanPro)
	apidserver.MustSeedApp(t, e, "shop")
	for _, route := range monetizationRoutes {
		for _, authenticated := range []bool{true, false} {
			req := httptest.NewRequest(route.method, route.path, nil)
			if authenticated {
				req.Header.Set("Authorization", "Bearer "+*e.Key())
			}
			req.Header.Set("Origin", "https://customer.example")
			res := httptest.NewRecorder()
			(*e.H()).ServeHTTP(res, req)
			if res.Code != http.StatusNotFound || !strings.Contains(res.Body.String(), "no such API route") ||
				res.Header().Get("Cache-Control") != "no-store" || res.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Errorf("%s %s (auth=%t): %d %v %s", route.method, route.path, authenticated, res.Code, res.Header(), res.Body)
			}
		}
	}
	if res := e.Do(t, http.MethodGet, "/v1/apps/shop/consumers", nil, nil); res.Code != http.StatusOK {
		t.Errorf("consumers stay available: %d %s", res.Code, res.Body)
	}
	hook := api.CreateAppWebhookRequest{TargetURL: "https://example.com/hook", WebhookSecret: "shh-test",
		EventFilter: []string{"usage_statement.finalized"}}
	if res := e.Do(t, http.MethodPost, "/v1/apps/shop/webhooks", hook, nil); res.Code == http.StatusCreated ||
		strings.Contains(res.Body.String(), "consumer.usage_threshold") {
		t.Errorf("monetization webhook event offered: %d %s", res.Code, res.Body)
	}
	tenant := tenantJSON[api.PlatformTenantResponse](t, e, http.MethodPost, "/v1/account/platform-tenants",
		api.CreatePlatformTenantRequest{ExternalRef: "hidden-co", Name: "Hidden Co"}, http.StatusCreated)
	tenantHook := api.CreatePlatformTenantWebhookRequest{TargetURL: "https://example.com/billing", WebhookSecret: "test-secret"}
	if res := e.Do(t, http.MethodPost, "/v1/account/platform-tenants/"+tenant.ID+"/webhooks", tenantHook, nil); res.Code == http.StatusCreated {
		t.Errorf("tenant webhook defaulted to the statement event: %s", res.Body)
	}
	tenantHook.EventFilter = []string{"platform_tenant.hostname.verified"}
	if res := e.Do(t, http.MethodPost, "/v1/account/platform-tenants/"+tenant.ID+"/webhooks", tenantHook, nil); res.Code != http.StatusCreated {
		t.Errorf("tenant hostname webhook: %d %s", res.Code, res.Body)
	}
	if paths := servedPaths(t, e); paths["/v1/apps/{slug}/rate-cards"] || !paths["/v1/apps/{slug}/consumers"] {
		t.Error("served spec does not follow the release gate")
	}

	t.Setenv("FAAS_API_MONETIZATION_ENABLED", "1")
	e = apidserver.Setup(t, api.PlanPro)
	apidserver.MustSeedApp(t, e, "shop")
	if res := e.Do(t, http.MethodGet, "/v1/apps/shop/rate-cards", nil, nil); res.Code != http.StatusOK {
		t.Errorf("released rate cards: %d %s", res.Code, res.Body)
	}
	if res := e.Do(t, http.MethodPost, "/v1/apps/shop/webhooks", hook, nil); res.Code != http.StatusCreated {
		t.Errorf("released statement webhook: %d %s", res.Code, res.Body)
	}
	if !servedPaths(t, e)["/v1/apps/{slug}/rate-cards"] {
		t.Error("released spec omits rate cards")
	}
}

func servedPaths(t *testing.T, e apidserver.TestEnv) map[string]bool {
	t.Helper()
	res := httptest.NewRecorder()
	(*e.H()).ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/v1/openapi.json", nil))
	var spec struct {
		Paths map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &spec); err != nil {
		t.Fatalf("decode served spec: %v", err)
	}
	paths := make(map[string]bool, len(spec.Paths))
	for path := range spec.Paths {
		paths[path] = true
	}
	return paths
}
