package journeys

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// edgeProbeRequests is how many requests check a rule change. The public
// edge spreads requests over the fleet's gateways, so a change the CLI
// reports applied must hold on every one of them, straight away (#2683).
const edgeProbeRequests = 12

// TestEdgeRules creates, changes, rolls back and deletes edge rules on an app
// of its own with the CLI, and checks each change on the app's public URL as
// soon as the command returns (row 74). It covers a redirect, a response
// header and its update, an IP refusal that also covers HEAD, a rule keyed on
// an account list and the list's edit, a rollback, and deletion.
func TestEdgeRules(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	list := tg.slug("edge-list")
	// Registered first so it runs after the app is deleted: the rules go
	// with the app, and the list, which is the account's, is then unused.
	t.Cleanup(func() {
		if res := tg.run(t, time.Minute, "edge", "rule-list", "delete", list); res.exit != 0 && !strings.Contains(res.problem(), "not_found") {
			t.Logf("warning: could not delete edge rule list %s: %s", list, res.problem())
		}
	})
	slug, base := tg.deployProbe(t, "edge")
	s := newStory(t)

	create := func(t *testing.T, args ...string) string {
		t.Helper()
		res := tg.ok(t, time.Minute, append([]string{"edge", "rule", "create", "--app", slug, "--match-host", "@app"}, args...)...)
		id := str(object(t, res.stdout), "id")
		if id == "" {
			t.Fatalf("create printed no rule id: %s", res.stdout)
		}
		return id
	}
	// every checks each of edgeProbeRequests responses straight after a change.
	every := func(t *testing.T, what, method, path string, want func(httpResult) bool) {
		t.Helper()
		for i := range edgeProbeRequests {
			if r := edgeFetch(method, base+path); !want(r) {
				t.Fatalf("%s: request %d of %d after the change returned: %s", what, i+1, edgeProbeRequests, r)
			}
		}
	}
	header := func(value string) func(httpResult) bool {
		return func(r httpResult) bool { return r.status == http.StatusOK && r.header.Get("X-Edge-Journey") == value }
	}
	var headerRule string
	var headerVersion int

	s.step("redirect", func(t *testing.T) {
		create(t, "--kind", "redirect", "--match-path", "/old", "--redirect-status", "302", "--redirect-to", "/")
		every(t, "redirect", http.MethodGet, "/old", func(r httpResult) bool {
			return r.status == http.StatusFound && r.header.Get("Location") == "/"
		})
	})
	s.step("response_header", func(t *testing.T) {
		headerRule = create(t, "--kind", "headers", "--match-path", "/**", "--headers-response-set", "X-Edge-Journey:v1")
		every(t, "header set", http.MethodGet, "/", header("v1"))
		headerVersion = latestEdgeRuleVersion(t, tg, slug)
	})
	s.step("update", func(t *testing.T) {
		tg.ok(t, time.Minute, "edge", "rule", "update", headerRule, "--kind", "headers", "--headers-response-set", "X-Edge-Journey:v2")
		every(t, "header updated", http.MethodGet, "/", header("v2"))
	})
	s.check("ip_refusal_covers_head", func(t *testing.T) {
		create(t, "--kind", "ip", "--match-path", "/blocked/**", "--match-method", "GET", "--ip-deny", "0.0.0.0/0", "--ip-deny", "::/0")
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			every(t, method+" refused", method, "/blocked/x", func(r httpResult) bool { return r.status == http.StatusForbidden })
		}
	})
	s.check("list", func(t *testing.T) {
		tg.ok(t, time.Minute, "edge", "rule-list", "create", list, "--kind", "ip", "--item", "0.0.0.0/0", "--item", "::/0")
		create(t, "--kind", "ip", "--match-path", "/listed/**", "--ip-deny", "0.0.0.0/0", "--ip-deny", "::/0",
			"--match", fmt.Sprintf(`{"field":"client_ip","op":"in_list","list":%q}`, list))
		every(t, "listed client refused", http.MethodGet, "/listed/x", func(r httpResult) bool { return r.status == http.StatusForbidden })
		// TEST-NET-1 (RFC 5737) never holds this machine, so the edit lets it
		// through to the app, which answers the unknown path itself.
		tg.ok(t, time.Minute, "edge", "rule-list", "update", list, "--remove", "0.0.0.0/0", "--remove", "::/0", "--add", "192.0.2.0/24")
		every(t, "list edit applied", http.MethodGet, "/listed/x", func(r httpResult) bool { return r.status == http.StatusNotFound })
	})
	s.step("rollback", func(t *testing.T) {
		tg.ok(t, 2*time.Minute, "edge", "rule", "rollback", "--app", slug, "--to", fmt.Sprint(headerVersion), "--quiet")
		every(t, "rolled back header", http.MethodGet, "/", header("v1"))
		every(t, "rules added later removed", http.MethodGet, "/blocked/x", func(r httpResult) bool { return r.status == http.StatusNotFound })
	})
	s.step("delete", func(t *testing.T) {
		res := tg.ok(t, time.Minute, "edge", "rule", "list", "--app", slug)
		for _, rule := range objects(t, res.stdout) {
			if id := str(rule, "id"); id != "" {
				tg.ok(t, time.Minute, "edge", "rule", "delete", id, "--yes")
			}
		}
		every(t, "rules deleted", http.MethodGet, "/old", func(r httpResult) bool {
			return r.status == http.StatusNotFound && r.header.Get("X-Edge-Journey") == ""
		})
	})
}

// latestEdgeRuleVersion returns the app's current rule-set version.
func latestEdgeRuleVersion(t *testing.T, tg *target, slug string) int {
	t.Helper()
	res := tg.ok(t, time.Minute, "edge", "rule", "history", "--app", slug)
	latest := 0
	for _, v := range objects(t, res.stdout) {
		latest = max(latest, int(num(v, "version")))
	}
	if latest == 0 {
		t.Fatalf("no rule-set version recorded: %s", res.stdout)
	}
	return latest
}

// edgeFetch retries the gateway's transient 503s (ADR-570) as a client
// would. Neither one applies a rule set, so a retry cannot hide a gateway
// serving an old one.
func edgeFetch(method, rawURL string) httpResult {
	r := fetch(method, rawURL, nil)
	for range 3 {
		if code := failureCode(r); r.status != http.StatusServiceUnavailable ||
			(code != "rate_limit_unavailable" && code != "traffic_policy_unavailable") {
			return r
		}
		time.Sleep(time.Second)
		r = fetch(method, rawURL, nil)
	}
	return r
}
