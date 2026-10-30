package journeys

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestProductionDebugger is the customer's debugging loop on a failing
// request (inventory rows 134, 135; ADR-127, ADR-1447): the app answers a
// request with a 500, and the customer finds that request by the public
// request ID the response carried, opens its evidence, sees it on
// `gregale debug status`, reads the release explanation and the debugger's
// coverage. Every command is the released CLI against the deployed API.
func TestProductionDebugger(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	hidden := hiddenCapabilities(t, tg)
	skipHidden(t, hidden, "production-debugger")
	slug, base := tg.deployProbe(t, "debugger")
	marker := "f-" + tg.runID + "-" + randomHex(t, 8)
	s := newStory(t)
	var requestID string

	s.step("failing_request", func(t *testing.T) {
		for range 5 {
			if r := get(base + "/"); r.status != http.StatusOK {
				t.Fatalf("GET /: %s", r)
			}
		}
		r := get(base + "/fail?marker=" + marker)
		if r.status != http.StatusInternalServerError {
			t.Fatalf("GET /fail: %s, want 500", r)
		}
		if requestID = r.header.Get("X-Faas-Request-Id"); requestID == "" {
			t.Fatalf("GET /fail carried no X-Faas-Request-Id: %s", r)
		}
	})
	s.step("request_by_public_id", func(t *testing.T) {
		eventually(t, "the failed request in `gregale debug requests get`", 3*time.Minute, 5*time.Second, func() (bool, string) {
			res := tg.run(t, time.Minute, "debug", "requests", "get", slug, requestID)
			skipDebuggerGated(t, res)
			if res.exit != 0 {
				return false, res.problem()
			}
			doc := object(t, res.stdout)
			ok := str(doc, "request_id") == requestID && num(doc, "status") == http.StatusInternalServerError &&
				strings.Contains(str(doc, "route"), "/fail")
			return ok, "request: " + compact(doc)
		})
	})
	s.check("evidence", func(t *testing.T) {
		doc := object(t, tg.ok(t, time.Minute, "debug", "requests", "evidence", slug, requestID).stdout)
		if str(doc, "request.request_id") != requestID || num(doc, "request.status") != http.StatusInternalServerError {
			t.Fatalf("evidence for %s: %s", requestID, tail(compact(doc), 600))
		}
	})
	s.check("status_reports_the_failure", func(t *testing.T) {
		eventually(t, "the failure on `gregale debug status`", 3*time.Minute, 5*time.Second, func() (bool, string) {
			res := tg.run(t, time.Minute, "debug", "status", slug, "--since", "1h")
			skipDebuggerGated(t, res)
			if res.exit != 0 {
				return false, res.problem()
			}
			doc := object(t, res.stdout)
			verdict := str(doc, "verdict")
			if verdict != "degraded" && verdict != "failing" || num(doc, "health.server_errors") < 1 {
				return false, "status: " + tail(compact(doc), 600)
			}
			deployments, _ := field(doc, "deployments").([]any)
			failures, _ := field(doc, "recent_failures").([]any)
			for _, failure := range failures {
				if num(failure, "status") == http.StatusInternalServerError && strings.Contains(str(failure, "route"), "/fail") {
					return len(deployments) > 0, "status lists no serving deployment: " + tail(compact(doc), 600)
				}
			}
			return false, "no /fail among recent failures: " + tail(compact(doc), 600)
		})
	})
	s.check("release_explanation", func(t *testing.T) {
		// A first deploy has no canary to explain; the answer says so.
		doc := object(t, tg.ok(t, time.Minute, "debug", "rollout", slug).stdout)
		if state := str(doc, "state"); state != "none" && state != "complete" || str(doc, "headline") == "" {
			t.Fatalf("debug rollout: %s", tail(compact(doc), 600))
		}
	})
	s.check("coverage", func(t *testing.T) {
		doc := object(t, tg.ok(t, time.Minute, "debug", "coverage", slug, "--since", "1h").stdout)
		if num(doc, "telemetry_rows") < 1 || num(doc, "error_requests") < 1 {
			t.Fatalf("debug coverage: %s", tail(compact(doc), 600))
		}
	})
}

// skipDebuggerGated skips when the debugger answers that this account's
// plan does not include it, which is the documented answer, not a failure.
func skipDebuggerGated(t *testing.T, res cliResult) {
	t.Helper()
	if res.exit != 0 && res.problemCode() == "plan_feature_gated" {
		t.Skipf("the debugger is not included in this account's plan: %s", res.problem())
	}
}
