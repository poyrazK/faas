package main

import (
	"math"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestAllocateRequestAnalyticsRouteCost_ReconcilesLargestRemainder(t *testing.T) {
	routes := []api.RequestAnalyticsRoute{
		{Route: "/small", Requests: 1},
		{Route: "/large", Requests: 2},
		{Route: "__other__", Requests: 3},
	}

	requests, allocated, other := allocateRequestAnalyticsRouteCost(routes, 3, 10)
	if requests != 9 || allocated != 10 || other != 3 {
		t.Fatalf("allocation totals = requests %d, cost %d, other cost %d; want 9, 10, 3", requests, allocated, other)
	}
	if routes[0].EstimatedComputeCostMillicents != 1 || routes[1].EstimatedComputeCostMillicents != 2 || routes[2].EstimatedComputeCostMillicents != 4 {
		t.Errorf("route cost shares = [%d %d %d], want [1 2 4]", routes[0].EstimatedComputeCostMillicents, routes[1].EstimatedComputeCostMillicents, routes[2].EstimatedComputeCostMillicents)
	}
	if math.Abs(routes[0].RequestSharePct-100.0/9) > 0.0001 || math.Abs(routes[1].RequestSharePct-200.0/9) > 0.0001 || math.Abs(routes[2].RequestSharePct-100.0/3) > 0.0001 {
		t.Errorf("route request shares = [%v %v %v], want [11.11 22.22 33.33]", routes[0].RequestSharePct, routes[1].RequestSharePct, routes[2].RequestSharePct)
	}
}

func TestAllocateRequestAnalyticsRouteCost_LeavesCostUnallocatedWithoutRequests(t *testing.T) {
	routes := []api.RequestAnalyticsRoute{{Route: "/unused", Requests: 0}}

	requests, allocated, other := allocateRequestAnalyticsRouteCost(routes, 0, 123)
	if requests != 0 || allocated != 0 || other != 0 {
		t.Fatalf("allocation totals = requests %d, cost %d, other cost %d; want 0, 0, 0", requests, allocated, other)
	}
	if routes[0].EstimatedComputeCostMillicents != 0 || routes[0].RequestSharePct != 0 {
		t.Errorf("zero-request route was allocated: %+v", routes[0])
	}
}

// ADR-743: a slow route holds instances longer, so it carries more cost than
// a fast route with many more requests.
func TestAllocateRequestAnalyticsRouteCostByTime_WeightsByRequestTime(t *testing.T) {
	routes := []api.RequestAnalyticsRoute{
		{Route: "/health", Requests: 900, RequestTimeMS: 900 * 5},  // 4.5 s total
		{Route: "/reports", Requests: 10, RequestTimeMS: 10 * 4000}, // 40 s total
	}
	// The omitted routes add 90 requests and 5.5 s.
	requests, allocated, other, method := allocateRequestAnalyticsRouteCostByTime(routes, 90, 5500, 1000)
	if method != routeCostByRequestTime {
		t.Fatalf("method = %q, want %q", method, routeCostByRequestTime)
	}
	if requests != 1000 || allocated != 1000 {
		t.Fatalf("requests=%d allocated=%d, want 1000 and the full 1000 millicents", requests, allocated)
	}
	if routes[0].EstimatedComputeCostMillicents != 90 || routes[1].EstimatedComputeCostMillicents != 800 || other != 110 {
		t.Fatalf("cost = /health %d, /reports %d, other %d; want 90, 800, 110 (time shares 9%%, 80%%, 11%%)",
			routes[0].EstimatedComputeCostMillicents, routes[1].EstimatedComputeCostMillicents, other)
	}
	if math.Abs(routes[1].RequestTimeSharePct-80) > 1e-9 || math.Abs(routes[1].RequestSharePct-1) > 1e-9 {
		t.Fatalf("/reports shares = time %v%%, requests %v%%; want 80 and 1", routes[1].RequestTimeSharePct, routes[1].RequestSharePct)
	}
}

func TestAllocateRequestAnalyticsRouteCostByTime_FallsBackWithoutTiming(t *testing.T) {
	routes := []api.RequestAnalyticsRoute{{Route: "/a", Requests: 1}, {Route: "/b", Requests: 3}}
	_, allocated, _, method := allocateRequestAnalyticsRouteCostByTime(routes, 0, 0, 100)
	if method != routeCostByRequests || allocated != 100 {
		t.Fatalf("method=%q allocated=%d, want request_share and 100", method, allocated)
	}
	if routes[0].EstimatedComputeCostMillicents != 25 || routes[1].EstimatedComputeCostMillicents != 75 {
		t.Fatalf("fallback split = %d/%d, want 25/75 by requests", routes[0].EstimatedComputeCostMillicents, routes[1].EstimatedComputeCostMillicents)
	}
	if routes[0].RequestTimeSharePct != 0 {
		t.Fatalf("time share must stay zero without timing: %v", routes[0].RequestTimeSharePct)
	}
}

func TestMillicentsAsEUR(t *testing.T) {
	for _, tc := range []struct {
		value int64
		want  string
	}{
		{value: 0, want: "0.00000"},
		{value: 1_234_567, want: "12.34567"},
	} {
		if got := millicentsAsEUR(tc.value); got != tc.want {
			t.Errorf("millicentsAsEUR(%d) = %q, want %q", tc.value, got, tc.want)
		}
	}
}
