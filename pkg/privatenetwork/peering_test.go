package privatenetwork

import (
	"net/netip"
	"reflect"
	"strings"
	"testing"
)

func testPeeringSpec() PeeringSpec {
	return PeeringSpec{
		ID:        "app-data",
		AccountID: "acct-1",
		Region:    "fra1",
		Left:      FabricSpec{AccountID: "acct-1", NetworkID: "frontend", Region: "fra1", CIDR: netip.MustParsePrefix("10.42.0.0/20")},
		Right:     FabricSpec{AccountID: "acct-1", NetworkID: "data", Region: "fra1", CIDR: netip.MustParsePrefix("10.42.16.0/20")},
	}
}

func TestBuildPeeringPlanCanonicalizesAndAddsBothDirections(t *testing.T) {
	spec := testPeeringSpec()
	spec.Left, spec.Right = spec.Right, spec.Left
	plan, err := BuildPeeringPlan(spec)
	if err != nil {
		t.Fatalf("BuildPeeringPlan: %v", err)
	}
	if plan.Spec.Left.NetworkID != "data" || plan.Spec.Right.NetworkID != "frontend" {
		t.Fatalf("endpoint order = %s,%s; want canonical order", plan.Spec.Left.NetworkID, plan.Spec.Right.NetworkID)
	}
	want := []PeeringRoute{
		{PeeringID: "app-data", FromNetworkID: "data", DestinationNetwork: "frontend", DestinationCIDR: netip.MustParsePrefix("10.42.0.0/20")},
		{PeeringID: "app-data", FromNetworkID: "frontend", DestinationNetwork: "data", DestinationCIDR: netip.MustParsePrefix("10.42.16.0/20")},
	}
	if !reflect.DeepEqual(plan.Routes, want) {
		t.Fatalf("routes = %#v, want %#v", plan.Routes, want)
	}
}

func TestBuildPeeringPlanRejectsUnsafeTopology(t *testing.T) {
	tests := []struct {
		name string
		edit func(*PeeringSpec)
		want string
	}{
		{name: "self", edit: func(s *PeeringSpec) { s.Right.NetworkID = s.Left.NetworkID }, want: "cannot peer with itself"},
		{name: "different account", edit: func(s *PeeringSpec) { s.Right.AccountID = "acct-2" }, want: "same account"},
		{name: "different region", edit: func(s *PeeringSpec) { s.Right.Region = "ams1" }, want: "same region"},
		{name: "overlap", edit: func(s *PeeringSpec) { s.Right.CIDR = netip.MustParsePrefix("10.42.8.0/21") }, want: "overlap"},
		{name: "invalid id", edit: func(s *PeeringSpec) { s.ID = "Bad_ID" }, want: "must match"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spec := testPeeringSpec()
			tc.edit(&spec)
			if _, err := BuildPeeringPlan(spec); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("BuildPeeringPlan error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestBuildPeeringRoutesSortsAndRejectsDuplicateRoutes(t *testing.T) {
	first := testPeeringSpec()
	second := PeeringSpec{
		ID:        "cache-data",
		AccountID: "acct-1",
		Region:    "fra1",
		Left:      first.Left,
		Right:     FabricSpec{AccountID: "acct-1", NetworkID: "cache", Region: "fra1", CIDR: netip.MustParsePrefix("10.42.32.0/20")},
	}
	routes, err := BuildPeeringRoutes([]PeeringSpec{first, second})
	if err != nil {
		t.Fatalf("BuildPeeringRoutes: %v", err)
	}
	if len(routes) != 4 || routes[0].FromNetworkID != "cache" || routes[1].FromNetworkID != "data" || routes[2].FromNetworkID != "frontend" {
		t.Fatalf("routes = %#v, want stable source ordering", routes)
	}
	second.ID = first.ID
	if _, err := BuildPeeringRoutes([]PeeringSpec{first, second}); err == nil || !strings.Contains(err.Error(), "duplicate peering ID") {
		t.Fatalf("duplicate ID error = %v", err)
	}
	second.ID = "cache-data"
	second.Right = first.Right
	if _, err := BuildPeeringRoutes([]PeeringSpec{first, second}); err == nil || !strings.Contains(err.Error(), "duplicate route") {
		t.Fatalf("duplicate route error = %v", err)
	}
	second.Right = FabricSpec{AccountID: "acct-1", NetworkID: "cache", Region: "fra1", CIDR: netip.MustParsePrefix("10.42.16.0/20")}
	if _, err := BuildPeeringRoutes([]PeeringSpec{first, second}); err == nil || !strings.Contains(err.Error(), "overlapping destinations") {
		t.Fatalf("overlapping destination error = %v", err)
	}
}
