package faas_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	faas "github.com/poyrazK/faas/sdk/go"
)

func TestRouteCustomerHealthSDKOptions(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.URL.Path != "/v1/apps/demo/route-health/deployments/candidate" {
			t.Error("deployment binding")
		}
		if calls == 1 {
			if r.URL.Query().Get("customers") != "true" || r.URL.Query().Get("customer_group_by") != "consumer" || r.URL.Query().Get("customer_details") != "true" {
				t.Error("options binding")
			}
		} else if r.URL.RawQuery != "" {
			t.Error("legacy query changed")
		}
		_, _ = w.Write([]byte(`{"deployment_id":"candidate","status":"healthy","customers":{"group_by":"consumer","details_included":true,"coverage":"observed_only","status":"regressed","reason":"consecutive_latency_violation","customers_limit":20,"routes":[{"method":"GET","path":"/orders","observed_customers":1,"customers_truncated":false,"candidate":{"identified_requests":200,"unattributed_requests":7,"unresolved_identity_requests":3,"other_customer_requests":0},"stable":{"identified_requests":200,"unattributed_requests":0,"unresolved_identity_requests":0,"other_customer_requests":0},"customers":[{"customer_id":"customer","health":{"method":"GET","path":"/orders","status":"regressed","latency_status":"regressed","windows":[{"candidate":{"requests":100,"server_errors":0,"error_rate":0,"p95_latency_ms":500},"stable":{"requests":100,"server_errors":0,"error_rate":0,"p95_latency_ms":0}}]}}]}]}}`))
	}))
	defer server.Close()
	c, err := faas.NewClient(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	r, err := c.GetRouteHealthReportWithOptions(context.Background(), "demo", "candidate", faas.RouteHealthReportOptions{Customers: true, CustomerGroupBy: "consumer", CustomerDetails: true})
	if err != nil || r.Status != "healthy" || r.Customers == nil || r.Customers.Status != "regressed" {
		t.Fatalf("%+v %v", r, err)
	}
	cohort := r.Customers.Routes[0].Customers[0]
	if cohort.CustomerID != "customer" || *cohort.Health.Windows[0].Stable.P95LatencyMS != 0 || r.Customers.Routes[0].Candidate.UnresolvedIdentityRequests != 3 {
		t.Fatal("evidence lost")
	}
	if _, err := c.GetRouteHealthReport(context.Background(), "demo", "candidate"); err != nil {
		t.Fatal(err)
	}
	for _, opts := range []faas.RouteHealthReportOptions{{CustomerDetails: true}, {CustomerGroupBy: "consumer"}, {Customers: true, CustomerGroupBy: "wrong"}} {
		if _, err := c.GetRouteHealthReportWithOptions(context.Background(), "demo", "candidate", opts); err == nil {
			t.Fatal("invalid options accepted")
		}
	}
	if calls != 2 {
		t.Fatal("invalid options reached API")
	}
}
