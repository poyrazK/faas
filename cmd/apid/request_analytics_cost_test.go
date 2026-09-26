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
