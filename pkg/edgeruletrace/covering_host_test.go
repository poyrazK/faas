package edgeruletrace_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/edgeruletrace"
)

// prod hunt 2026-11-21: a trace of one app listed every sibling app's
// exact-host rules as skipped rows (9 of 14). Sibling rules that cannot
// match the traced host are dropped; the app's own rules always stay.
func TestCoveringRulesForHostDropsSiblingRulesForOtherHosts(t *testing.T) {
	rules := []api.EdgeRuleResponse{
		{ID: "own-other-host", AppID: "a1", MatchHost: "shop.example.test"},
		{ID: "own-app", AppID: "a1", MatchHost: "@app"},
		{ID: "sibling-exact", AppID: "a2", MatchHost: "signalmesh-gateway.gregale.dev"},
		{ID: "sibling-wildcard", AppID: "a2", MatchHost: "*.gregale.dev"},
		{ID: "sibling-same-host", AppID: "a3", MatchHost: "bh20-app.gregale.dev"},
	}
	got := edgeruletrace.CoveringRulesForHost(rules, "a1", "bh20-app.gregale.dev")
	var ids []string
	for _, rule := range got {
		ids = append(ids, rule.ID)
	}
	want := []string{"own-other-host", "own-app", "sibling-wildcard", "sibling-same-host"}
	if len(ids) != len(want) {
		t.Fatalf("got %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("got %v, want %v", ids, want)
		}
	}
}
