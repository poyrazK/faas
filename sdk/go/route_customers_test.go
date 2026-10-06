package faas_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	faas "github.com/poyrazK/faas/sdk/go"
)

func TestRouteCustomerUsageClientScopesWindowAndRetainsCoverage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/apps/demo/analytics/route-customers" || r.URL.Query().Get("deployment_id") != "deployment" || r.URL.Query().Get("since") != "7d" || r.URL.Query().Get("until") != "2026-10-03T00:00:00Z" {
			t.Errorf("request %s %s", r.Method, r.URL)
		}
		_, _ = w.Write([]byte(`{"slug":"demo","deployment_id":"deployment","from":"2026-10-01T00:00:00Z","until":"2026-10-03T00:00:00Z","as_of":"2026-10-03T00:00:00Z","window_clamped":true,"coverage":"observed_only","routes_limit":200,"customers_limit":20,"routes_truncated":true,"routes":[{"route":"GET /orders","method":"GET","requests":15,"identified_requests":10,"anonymous_requests":5,"unresolved_identity_requests":0,"consumer_count":3,"platform_tenant_count":1,"last_observed_at":"2026-10-02T00:00:00Z","customers_truncated":true,"other_customer_requests":2,"customers":[{"consumer_id":"consumer","platform_tenant_id":"historical-tenant","requests":8,"last_observed_at":"2026-10-02T00:00:00Z"}]}]}`))
	}))
	defer srv.Close()
	c, err := faas.NewClient(srv.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	out, err := c.GetAppRouteCustomerUsage(context.Background(), "demo", faas.RouteCustomerUsageOptions{DeploymentID: "deployment", Since: "7d", Until: "2026-10-03T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	if !out.WindowClamped || !out.RoutesTruncated || out.Coverage != "observed_only" || len(out.Routes) != 1 || !out.Routes[0].CustomersTruncated || out.Routes[0].OtherCustomerRequests != 2 || out.Routes[0].Customers[0].PlatformTenantID != "historical-tenant" {
		t.Fatalf("%+v", out)
	}
}
