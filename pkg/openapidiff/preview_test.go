package openapidiff

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// TestBuildRoutePolicyPreview pins API-hosting roadmap deliverable 11:
// declared-vs-observed drift is deterministic and policy coverage includes
// method-aware, wildcard, and disabled rules without performing any writes.
func TestBuildRoutePolicyPreview(t *testing.T) {
	spec, err := LoadBytes([]byte(`{
      "openapi":"3.1.0",
      "info":{"title":"preview","version":"1"},
      "paths":{
        "/users":{"get":{},"post":{}},
        "/healthz":{"get":{}}
      }
    }`))
	if err != nil {
		t.Fatal(err)
	}
	rules := []state.EdgeRule{
		{ID: "z-disabled", MatchPath: "/users", MatchMethods: []string{"POST"}, Priority: 20, Enabled: false, Kind: state.EdgeRuleKindHeaders},
		{ID: "a-wildcard", MatchPath: "/", Priority: 10, Enabled: true, Kind: state.EdgeRuleKindRoute},
		{ID: "b-get", MatchPath: "/users", MatchMethods: []string{"GET"}, Priority: 5, Enabled: true, Kind: state.EdgeRuleKindValidate},
	}
	got := BuildRoutePolicyPreview(spec, []RouteRow{
		{Route: "GET /users"},
		{Route: "GET /metrics"},
		{Route: "__route_other__"},
		{Route: "malformed"},
	}, rules)
	if len(got) != 4 {
		t.Fatalf("route count: got %d, want 4: %+v", len(got), got)
	}
	if got[0].Path != "/healthz" || got[0].Status != "declared_only" || !got[0].Covered {
		t.Fatalf("healthz row: %+v", got[0])
	}
	if got[1].Path != "/metrics" || got[1].Status != "observed_only" {
		t.Fatalf("metrics row: %+v", got[1])
	}
	if got[2].Path != "/users" || got[2].Method != "get" || got[2].Status != "matched" || len(got[2].Rules) != 2 {
		t.Fatalf("users GET row: %+v", got[2])
	}
	if got[3].Path != "/users" || got[3].Method != "post" || len(got[3].Rules) != 2 {
		t.Fatalf("users POST row: %+v", got[3])
	}
	if got[3].Rules[0].ID != "a-wildcard" || got[3].Rules[1].ID != "z-disabled" {
		t.Fatalf("rule ordering: %+v", got[3].Rules)
	}
}
