package billing

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func routeAt(minute time.Time, route string, units int64) state.APIConsumerRouteUsageBucket {
	return state.APIConsumerRouteUsageBucket{WindowStart: minute, Route: route, BillableUnits: units}
}

// adr: 939
func TestWeightAPIConsumerUsageCountsWeightedRoutes(t *testing.T) {
	plain := allowanceCard(allowanceStart, 10, 0)
	weighted := allowanceCard(allowanceStart.Add(time.Hour), 10, 0)
	weighted.RouteWeights = map[string]int64{"POST /generate": 20, "GET /search": 2}
	m0, m1 := allowanceStart, allowanceStart.Add(time.Hour)
	usage := []state.APIConsumerUsageBucket{usageAt(m0, 5), usageAt(m1, 5)}
	routes := []state.APIConsumerRouteUsageBucket{
		routeAt(m0, "POST /generate", 2), // the plain card ignores weights
		routeAt(m1, "POST /generate", 2), // 2 x 20
		routeAt(m1, "GET /search", 1),    // 1 x 2
		routeAt(m1, "GET /items", 2),     // unweighted: 2 x 1
	}
	out, err := WeightAPIConsumerUsage([]state.APIConsumerRateCard{weighted, plain}, usage, routes)
	if err != nil {
		t.Fatal(err)
	}
	if out[0].BillableUnits != 5 || out[1].BillableUnits != 44 {
		t.Fatalf("weighted units = %d, %d; want 5 and 44", out[0].BillableUnits, out[1].BillableUnits)
	}
	if usage[1].BillableUnits != 5 {
		t.Fatal("caller usage mutated")
	}
}

func TestWeightAPIConsumerUsageNeverCountsMoreRequestsThanTheMinute(t *testing.T) {
	card := allowanceCard(allowanceStart, 10, 0)
	card.RouteWeights = map[string]int64{"POST /generate": 10}
	out, err := WeightAPIConsumerUsage([]state.APIConsumerRateCard{card},
		[]state.APIConsumerUsageBucket{usageAt(allowanceStart, 3)},
		[]state.APIConsumerRouteUsageBucket{routeAt(allowanceStart, "POST /generate", 7)})
	if err != nil {
		t.Fatal(err)
	}
	if out[0].BillableUnits != 30 {
		t.Fatalf("weighted units = %d, want 30 (3 requests at weight 10)", out[0].BillableUnits)
	}
}

func TestValidateRouteWeights(t *testing.T) {
	for name, weights := range map[string]map[string]int64{
		"no method":   {"/generate": 2},
		"query":       {"GET /x?y=1": 2},
		"zero weight": {"GET /x": 0},
		"too heavy":   {"GET /x": state.MaxAPIConsumerRouteWeight + 1},
	} {
		if err := state.ValidateAPIConsumerRouteWeights(weights); err == nil {
			t.Errorf("%s: accepted %v", name, weights)
		}
	}
	if err := state.ValidateAPIConsumerRouteWeights(map[string]int64{"POST /generate": 20, "GET /items/{id}": 1}); err != nil {
		t.Fatalf("valid weights rejected: %v", err)
	}
}

func TestQuotePlatformTenantUsageRejectsAppRouteWeights(t *testing.T) {
	card := allowanceCard(allowanceStart, 10, 0)
	card.RouteWeights = map[string]int64{"POST /generate": 2}
	if _, err := QuotePlatformTenantUsage([]state.APIConsumerRateCard{card}, nil, []state.APIConsumerUsageBucket{usageAt(allowanceStart, 1)}); err == nil {
		t.Fatal("tenant statements must not apply per-consumer route weights")
	}
}
