package journeys

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

// TestEdgeTrafficPolicies covers the traffic rules of row 74 that
// TestEdgeRules does not: a route rule that serves one app's path from
// another app, a throttle that answers 429 rate_limited, and a budget whose
// execution budget and total deadline answer request_budget_exceeded. Each
// change is checked on the public URL as soon as the CLI returns.
func TestEdgeTrafficPolicies(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	slug, base := tg.deployProbe(t, "policies")
	target := tg.app(t, "routed")
	s := newStory(t)

	s.step("deploy_route_target", func(t *testing.T) {
		tg.deployVersion(t, target, "v2")
	})
	s.check("route", func(t *testing.T) {
		createEdgeRule(t, tg, slug, "--kind", "route", "--match-path", "/status", "--route-target-slug", target)
		routed := func() (bool, string) {
			seen := map[string]int{}
			for range edgeProbeRequests {
				r := edgeFetch(http.MethodGet, base+"/status?code=200")
				var doc struct {
					Version string `json:"version"`
				}
				if r.status != http.StatusOK || json.Unmarshal(r.body, &doc) != nil {
					seen[fmt.Sprintf("HTTP %d %s", r.status, failureCode(r))]++
					continue
				}
				seen[doc.Version]++
			}
			return seen["v2"] == edgeProbeRequests, fmt.Sprint(seen)
		}
		if ok, seen := routed(); !ok {
			// Tell a rule that applies late from one that never applies.
			eventually(t, "the route rule applies", time.Minute, 5*time.Second, routed)
			t.Fatalf("the route rule was not applied when the CLI returned: %s", seen)
		}
		// Only the matched path moves; the rest of the app still serves v1.
		expectVersion(t, base, "v1", time.Minute)
	})
	s.check("throttle", func(t *testing.T) {
		createEdgeRule(t, tg, slug, "--kind", "throttle", "--match-path", "/ready",
			"--throttle-requests-per-second", "0.1", "--throttle-burst", "2")
		admitted, limited := 0, 0
		var refusal httpResult
		for range 10 {
			switch r := edgeFetch(http.MethodGet, base+"/ready"); {
			case r.status == http.StatusOK:
				admitted++
			case r.status == http.StatusTooManyRequests && failureCode(r) == "rate_limited":
				limited++
				refusal = r
			default:
				t.Fatalf("throttled route answered %s", r)
			}
		}
		t.Logf("metric throttle_admitted=%d", admitted)
		// The bucket is shared by every gateway serving the app: a burst of 2
		// at 0.1/s admits 2 of 10 back-to-back requests, 3 if one refills.
		if admitted == 0 || admitted > 3 || limited == 0 {
			t.Fatalf("10 requests at burst 2: %d admitted, %d limited", admitted, limited)
		}
		if refusal.header.Get("X-RouteRateLimit-Limit") == "" {
			t.Fatalf("429 rate_limited without X-RouteRateLimit-Limit: %v", refusal.header)
		}
	})
	var budget string
	s.check("budget", func(t *testing.T) {
		budget = createEdgeRule(t, tg, slug, "--kind", "budget", "--match-path", "/slow", "--budget-ms", "1000")
		expectBudgetExceeded(t, base+"/slow?ms=4000", 3*time.Second)
		if r := fetch(http.MethodGet, base+"/slow?ms=100", nil); r.status != http.StatusOK {
			t.Fatalf("a request inside its budget: %s", r)
		}
	})
	s.check("total_deadline", func(t *testing.T) {
		if budget == "" {
			t.Skip("no budget rule to update")
		}
		// A generous execution budget leaves only the total deadline to cut.
		tg.ok(t, time.Minute, "edge", "rule", "update", budget, "--kind", "budget",
			"--budget-ms", "10000", "--total-deadline-ms", "1500")
		expectBudgetExceeded(t, base+"/slow?ms=4000", 3*time.Second)
	})
}

// createEdgeRule creates a rule on slug's own host and returns its id.
func createEdgeRule(t *testing.T, tg *target, slug string, args ...string) string {
	t.Helper()
	res := tg.ok(t, time.Minute, append([]string{"edge", "rule", "create", "--app", slug, "--match-host", "@app"}, args...)...)
	id := str(object(t, res.stdout), "id")
	if id == "" {
		t.Fatalf("create printed no rule id: %s", res.stdout)
	}
	return id
}

// expectBudgetExceeded checks that every gateway cuts a request to rawURL
// with request_budget_exceeded well before the app would answer. The public
// edge sends the gateway's 504 as 503 so Cloudflare keeps the problem body
// (ADR-20261030115139).
func expectBudgetExceeded(t *testing.T, rawURL string, within time.Duration) {
	t.Helper()
	for i := range edgeProbeRequests {
		r := fetch(http.MethodGet, rawURL, nil)
		if r.status != http.StatusServiceUnavailable || failureCode(r) != "request_budget_exceeded" || r.elapsed > within {
			t.Fatalf("request %d of %d past its budget: %s", i+1, edgeProbeRequests, r)
		}
	}
}
