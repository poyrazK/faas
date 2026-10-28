package gateway

import (
	"net/http"
	"testing"
)

// adr: 091 — amendment 2026-10-28 (review round 3): a rule that refuses or
// limits requests and lists GET also covers HEAD, which servers answer with
// the GET handler. A GET-only jwt rule on /admin/* let HEAD /admin/x reach
// the app unauthenticated. Other kinds keep exact method matching, and a
// HEAD-only rule does not widen to GET.
func TestProtectiveRulesCoverHEADWhenTheyListGET(t *testing.T) {
	get := map[string]bool{http.MethodGet: true}
	head := map[string]bool{http.MethodHead: true}
	const p = "/admin/x"
	for _, tc := range []struct {
		kind  string
		match func(methods map[string]bool, method string) bool
	}{
		{"jwt", func(m map[string]bool, method string) bool {
			return PickFirstJWTMatch([]EdgeRuleJWTResolved{{ID: "r", PathGlob: "/admin/*", Methods: m}}, p, method) != nil
		}},
		{"ip", func(m map[string]bool, method string) bool {
			return PickFirstIPMatch([]EdgeRuleIPResolved{{ID: "r", PathGlob: "/admin/*", Methods: m}}, p, method) != nil
		}},
		{"geo", func(m map[string]bool, method string) bool {
			return PickFirstGeoMatch([]EdgeRuleGeoResolved{{ID: "r", PathGlob: "/admin/*", Methods: m}}, p, method) != nil
		}},
		{"validate", func(m map[string]bool, method string) bool {
			return PickFirstValidateMatch([]EdgeRuleValidateResolved{{ID: "r", PathGlob: "/admin/*", Methods: m}}, p, method) != nil
		}},
		{"limit", func(m map[string]bool, method string) bool {
			return PickFirstLimitMatch([]EdgeRuleLimitResolved{{ID: "r", PathGlob: "/admin/*", Methods: m}}, p, method) != nil
		}},
		{"throttle", func(m map[string]bool, method string) bool {
			return PickFirstThrottleMatch([]EdgeRuleThrottleResolved{{ID: "r", PathGlob: "/admin/*", Methods: m}}, p, method) != nil
		}},
		{"maintenance", func(m map[string]bool, method string) bool {
			return PickFirstMaintenanceMatch([]EdgeRuleMaintenanceResolved{{ID: "r", PathGlob: "/admin/*", Methods: m}}, p, method) != nil
		}},
		{"waf", func(m map[string]bool, method string) bool {
			return PickFirstWAFMatch([]EdgeRuleWAFResolved{{ID: "r", PathGlob: "/admin/*", Methods: m}}, p, method) != nil
		}},
		{"challenge", func(m map[string]bool, method string) bool {
			return PickFirstChallengeMatch([]EdgeRuleChallengeResolved{{ID: "r", PathGlob: "/admin/*", Methods: m}}, p, method) != nil
		}},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			if !tc.match(get, http.MethodHead) {
				t.Error("a GET rule does not cover HEAD")
			}
			if tc.match(head, http.MethodGet) {
				t.Error("a HEAD rule covers GET")
			}
			if tc.match(get, http.MethodPost) {
				t.Error("a GET rule covers POST")
			}
		})
	}
	headers := []EdgeRuleHeadersResolved{{ID: "r", PathGlob: "/admin/*", Methods: get}}
	if PickFirstHeadersMatch(headers, p, http.MethodHead) != nil {
		t.Error("a GET headers rule widened to HEAD; only refusing kinds widen")
	}
}
