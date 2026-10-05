package routehealth

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCustomerHealthSignalsAndAdvisoryIsolation(t *testing.T) {
	for _, scenario := range []string{"errors", "latency", "sparse", "missing_side", "mixed", "healthy", "unattributed", "truncated", "anchor", "unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			now := time.Now().UTC()
			anchor := now.Add(-time.Hour)
			windows := Windows(now)
			latency := scenario == "latency"
			for i := range windows {
				w := &windows[i]
				w.Candidate.Requests, w.Stable.Requests = 100, 100
				switch scenario {
				case "errors":
					w.Candidate.ServerErrors = 10
				case "latency":
					a, b := 300.0, 100.0
					w.Candidate.P95LatencyMS, w.Stable.P95LatencyMS = &a, &b
				case "sparse":
					w.Candidate.Requests = 19
				case "missing_side":
					w.Stable.Requests = 0
				case "mixed":
					if i == 0 {
						w.Candidate.ServerErrors = 10
					}
				}
			}
			r := api.RouteHealthReport{Mode: "enforce", Status: "healthy", Reason: "aggregate_healthy", ObservationAnchor: &anchor, Routes: []api.RouteHealthFinding{{Method: "POST", Path: "/checkout", CheckLatency: latency}}, Customers: &api.RouteCustomerHealthReport{GroupBy: "tenant", Routes: []api.RouteCustomerHealthRoute{{Method: "POST", Path: "/checkout", ObservedCustomers: 1, Customers: []api.RouteCustomerHealthCohort{{CustomerID: "private", Health: api.RouteHealthFinding{Method: "POST", Path: "/checkout", Windows: windows}}}}}}}
			if scenario == "unattributed" {
				r.Customers.Routes[0].Candidate.UnattributedRequests = 100
			}
			if scenario == "truncated" {
				r.Customers.Routes[0].CustomersTruncated = true
			}
			if scenario == "anchor" {
				anchor = now
			}
			unavailable := ""
			if scenario == "unavailable" {
				unavailable = "telemetry_unavailable"
			}
			before := Decision(r)
			EvaluateCustomers(&r, unavailable)
			want := "unknown"
			if scenario == "errors" || scenario == "latency" {
				want = "regressed"
			}
			if scenario == "healthy" {
				want = "healthy"
			}
			if r.Customers.Status != want {
				t.Fatalf("got %+v, want %s", r.Customers, want)
			}
			if r.Customers.Routes[0].Customers[0].CustomerID != "" || r.Customers.Coverage != "observed_only" {
				t.Fatal("identity or coverage")
			}
			if Decision(r) != before || r.Status != "healthy" {
				t.Fatal("customer finding changed enforcement")
			}
		})
	}
}
