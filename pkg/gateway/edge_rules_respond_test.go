// spec: §4.1
package gateway

import "testing"

func TestPickFirstRespondMatch(t *testing.T) {
	rules := []EdgeRuleRespondResolved{
		{ID: "post", Priority: 20, PathGlob: "/shipping/*", Methods: map[string]bool{"POST": true}},
		{ID: "get", Priority: 30, PathGlob: "/shipping/*", Methods: map[string]bool{"GET": true}},
	}
	if got := PickFirstRespondMatch(rules, "/shipping/estimate", "GET"); got == nil || got.ID != "get" {
		t.Fatalf("GET match = %#v, want get", got)
	}
	if got := PickFirstRespondMatch(rules, "/shipping/estimate", "DELETE"); got != nil {
		t.Fatalf("DELETE match = %#v, want nil", got)
	}
}
