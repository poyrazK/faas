package routehealth

import (
	"fmt"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func usageRow(method, path string, tenants, consumers, requests int64) api.RouteCustomerUsage {
	return api.RouteCustomerUsage{Route: method + " " + path, Method: method, Requests: requests, PlatformTenantCount: tenants, ConsumerCount: consumers}
}

func TestRankRouteUsageOrdersByReachThenRequests(t *testing.T) {
	rows := []api.RouteCustomerUsage{
		usageRow("GET", "/b", 1, 9, 100),
		usageRow("GET", "/a", 1, 9, 100),
		usageRow("POST", "/checkout", 5, 1, 10),
		usageRow("POST", "/login", 1, 2, 500),
		usageRow("GET", "/a", 9, 9, 999),        // duplicate label keeps the first row
		usageRow("GET", "/search?q", 99, 99, 9), // queries are not exact selectors
		usageRow("TRACE", "/x", 99, 99, 9),      // unsupported method
		usageRow("GET", "/idle", 99, 99, 0),     // no traffic
		{Route: "/mismatch", Method: "GET", Requests: 9},
	}
	got := RankRouteUsage(rows, "tenant")
	want := []string{"POST /checkout", "POST /login", "GET /a", "GET /b"}
	if len(got) != len(want) {
		t.Fatalf("ranked %d routes, want %d: %+v", len(got), len(want), got)
	}
	for i, item := range got {
		if label := item.Selector.Method + " " + item.Selector.Path; label != want[i] {
			t.Fatalf("rank %d = %s, want %s", i+1, label, want[i])
		}
	}
	if byConsumer := RankRouteUsage(rows, "consumer"); byConsumer[len(byConsumer)-1].Selector.Path != "/checkout" {
		t.Fatalf("consumer ranking last = %+v, want /checkout", byConsumer[len(byConsumer)-1])
	}
}

func TestSeedSelectorsBoundsAndValidates(t *testing.T) {
	if got := SeedSelectors(nil, api.RouteHealthSeedRoutes); got == nil || len(got) != 0 {
		t.Fatalf("empty usage = %#v, want a non-nil empty slice", got)
	}
	rows := make([]api.RouteCustomerUsage, 0, 40)
	for i := range 40 {
		rows = append(rows, usageRow("GET", fmt.Sprintf("/r%02d", i), int64(i), 0, 100))
	}
	got := SeedSelectors(rows, api.RouteHealthSeedRoutes)
	if len(got) != api.RouteHealthSeedRoutes || got[0].Path != "/r39" {
		t.Fatalf("seeded %d routes starting %+v, want %d starting /r39", len(got), got[0], api.RouteHealthSeedRoutes)
	}
	if over := SeedSelectors(rows, 1000); len(over) != api.RouteHealthMaxRoutes {
		t.Fatalf("limit above the gate cap seeded %d routes, want %d", len(over), api.RouteHealthMaxRoutes)
	}
	zero := int64(0)
	if err := Validate(api.SetRouteHealthGateRequest{Mode: "report", ExpectedRevision: &zero, Routes: got}); err != nil {
		t.Fatalf("seeded selectors are not a valid gate: %v", err)
	}
}
