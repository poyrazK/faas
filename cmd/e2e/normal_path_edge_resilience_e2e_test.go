// normal_path_edge_resilience_e2e_test.go — kind=retry and
// kind=circuit_breaker end to end (ADR-201; ADR-091 amendment 2026-10-27, GA
// hardening). The rules are created through the API and act on real gateway
// traffic to two RUNNING instances behind the fake vmmd, one of which fails
// at the transport layer. Production turns both gates on
// (FAAS_GATEWAY_RETRY, FAAS_GATEWAY_CIRCUIT_BREAKER in the gatewayd-internal
// drop-in), so these fixtures do too.

package e2e_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// newNormalPathSiblings boots a Hobby app with two RUNNING instances of one
// deployment, both routable, and returns (first, second). Hobby allows the
// retry and circuit_breaker rules and two instances; a Pro app would put its
// public routes behind the plan's default API-key gate.
func newNormalPathSiblings(t *testing.T, slug string, env ...string) (*normalPathFixture, state.Instance, state.Instance) {
	t.Helper()
	f := newNormalPathFixtureWithPlanAndEnv(t, slug, api.PlanHobby, env...)
	if f == nil {
		return nil, state.Instance{}, state.Instance{}
	}
	deployment, first := createNormalPathLiveDeployment(t, f, f.app.ID, slug+"-a")
	second := createNormalPathRunningSibling(t, f, deployment.ID)
	f.vmmd.SetVersion(first.ID, "a")
	f.vmmd.SetVersion(second.ID, "b")
	notifyNormalPathInstanceChanged(t, f, second.ID, string(state.StateRunning))
	waitForNormalPathBodyPrefix(t, f, "normal-path:", 10*time.Second)
	return f, first, second
}

// normalPathInstancesFor lists the instances the fake vmmd saw for one
// request URI, in arrival order.
func normalPathInstancesFor(f *normalPathFixture, requestURI string) []string {
	var out []string
	for _, capture := range f.vmmd.Requests() {
		if capture.Init.GetRequestUri() == requestURI {
			out = append(out, capture.Init.GetInstance())
		}
	}
	return out
}

func createNormalPathEdgeRule(t *testing.T, f *normalPathFixture, req map[string]any) {
	t.Helper()
	body, code := doReq(t, f.h, f.key, http.MethodPost, "/v1/apps/"+f.app.Slug+"/edge-rules", req)
	if code != http.StatusCreated {
		t.Fatalf("create %v rule: status=%d body=%s", req["kind"], code, body)
	}
}

// TestE2E_NormalPath_EdgeRetryReplaysTransportFailure: with a kind=retry
// rule, a GET that reaches a dead instance is replayed on its healthy
// sibling and succeeds, and a POST is replayed only with an Idempotency-Key
// and allow_non_idempotent. Without the rule the same failure is a 503
// (TestE2E_NormalPath_StaleInstanceFailsOverAndReplacementRejoins).
func TestE2E_NormalPath_EdgeRetryReplaysTransportFailure(t *testing.T) {
	f, dead, healthy := newNormalPathSiblings(t, "normal-edge-retry", "FAAS_GATEWAY_RETRY=true")
	if f == nil {
		return
	}
	createNormalPathEdgeRule(t, f, map[string]any{
		"kind": "retry", "match_host": api.EdgeRuleAppHosts, "match_path": "/**",
		"action": map[string]any{"max_attempts": 2, "allow_non_idempotent": true, "budget_percent": 100, "budget_min_retries": 32},
	})
	f.vmmd.FailAll(dead.ID, status.Error(codes.Unavailable, "simulated dead sibling"))

	replayed := false
	for i := 0; i < 6 && !replayed; i++ {
		path := fmt.Sprintf("/edge-retry/get/%d", i)
		_, body, code := doReqHeaders(t, f.h, f.host, http.MethodGet, path, nil)
		if code != http.StatusOK || string(body) != "normal-path:b\n" {
			t.Fatalf("GET %s status=%d body=%q, want the healthy sibling's 200", path, code, body)
		}
		seen := normalPathInstancesFor(f, path)
		replayed = len(seen) == 2 && seen[0] == dead.ID && seen[1] == healthy.ID
	}
	if !replayed {
		t.Fatal("no GET reached the dead sibling first and was replayed on the healthy one")
	}

	// A transport failure quarantines the instance for
	// api.TrafficStaleTargetQuarantine, so each POST case gets its own freshly
	// RUNNING, failing sibling, which joins the target set at once.
	for i, withKey := range []bool{false, true} {
		failing := createNormalPathRunningSibling(t, f, dead.DeploymentID)
		f.vmmd.SetVersion(failing.ID, fmt.Sprintf("c%d", i))
		f.vmmd.FailAll(failing.ID, status.Error(codes.Unavailable, "simulated dead sibling"))
		notifyNormalPathInstanceChanged(t, f, failing.ID, string(state.StateRunning))
		hit := false
		deadline := time.Now().Add(10 * time.Second)
		for j := 0; !hit && time.Now().Before(deadline); j++ {
			path := fmt.Sprintf("/edge-retry/post/%t/%d", withKey, j)
			headers := map[string]string{}
			if withKey {
				headers["Idempotency-Key"] = fmt.Sprintf("order-%d", j)
			}
			_, body, code := doReqHeaders(t, f.h, f.host, http.MethodPost, path, map[string]string{"order": "1"}, headers)
			seen := normalPathInstancesFor(f, path)
			if len(seen) == 0 || seen[0] != failing.ID {
				time.Sleep(100 * time.Millisecond)
				continue
			}
			hit = true
			switch {
			case withKey && (code != http.StatusOK || len(seen) != 2 || seen[1] != healthy.ID):
				t.Fatalf("POST with Idempotency-Key: status=%d body=%q attempts=%v, want a replay on the healthy sibling", code, body, seen)
			case !withKey && (code != http.StatusServiceUnavailable || len(seen) != 1):
				t.Fatalf("POST without Idempotency-Key: status=%d attempts=%v, want one attempt and 503", code, seen)
			}
		}
		if !hit {
			t.Fatalf("no POST (Idempotency-Key=%t) reached the failing sibling within 10s", withKey)
		}
	}
}

// TestE2E_NormalPath_EdgeCircuitBreakerKeepsFailedInstanceOut: a
// kind=circuit_breaker rule with min_requests=1 opens an instance's circuit
// on its first transport failure and keeps it out of rotation for
// open_seconds, even after the instance comes back. The default breaker
// needs five failures, so without the rule one failure does not open it and
// the instance serves again once it rejoins.
//
// Two waits are inherent: the gateway caches an app's breaker tuning for
// 30 s (circuitConfigTTL) and the fixture's first requests cached "no rule";
// and a transport failure quarantines the instance for
// api.TrafficStaleTargetQuarantine whatever the breaker says, so only after
// that does the breaker decide whether the instance serves again.
func TestE2E_NormalPath_EdgeCircuitBreakerKeepsFailedInstanceOut(t *testing.T) {
	const circuitTuningTTL = 30 * time.Second
	for _, tuned := range []bool{false, true} {
		t.Run(fmt.Sprintf("tuned=%t", tuned), func(t *testing.T) {
			f, flaky, healthy := newNormalPathSiblings(t, fmt.Sprintf("normal-edge-circuit-%t", tuned), "FAAS_GATEWAY_CIRCUIT_BREAKER=true")
			if f == nil {
				return
			}
			if tuned {
				createNormalPathEdgeRule(t, f, map[string]any{
					"kind": "circuit_breaker", "match_host": api.EdgeRuleAppHosts, "match_path": "/**",
					"action": map[string]any{"failure_threshold": 0.5, "min_requests": 1, "window_seconds": 60,
						"open_seconds": 300, "max_open_seconds": 600},
				})
				// Let the cached "no rule" tuning expire; one request then
				// starts the background reload.
				time.Sleep(circuitTuningTTL + time.Second)
				doReqHeaders(t, f.h, f.host, http.MethodGet, "/edge-circuit/reload", nil)
				time.Sleep(2 * time.Second)
			}

			f.vmmd.FailAll(flaky.ID, status.Error(codes.Unavailable, "simulated flaky sibling"))
			failed := false
			for i := 0; i < 6 && !failed; i++ {
				path := fmt.Sprintf("/edge-circuit/fail/%d", i)
				doReqHeaders(t, f.h, f.host, http.MethodGet, path, nil)
				seen := normalPathInstancesFor(f, path)
				failed = len(seen) > 0 && seen[0] == flaky.ID
			}
			if !failed {
				t.Fatal("no request reached the flaky sibling")
			}

			// The instance recovers; once its quarantine ends it rejoins.
			f.vmmd.FailAll(flaky.ID, nil)
			time.Sleep(api.TrafficStaleTargetQuarantine + 2*time.Second)
			notifyNormalPathInstanceChanged(t, f, flaky.ID, string(state.StateRunning))
			reached := 0
			deadline := time.Now().Add(8 * time.Second)
			for i := 0; time.Now().Before(deadline) && (tuned || reached == 0); i++ {
				path := fmt.Sprintf("/edge-circuit/after/%d", i)
				_, body, code := doReqHeaders(t, f.h, f.host, http.MethodGet, path, nil)
				if code != http.StatusOK {
					t.Fatalf("GET %s status=%d body=%q, want 200", path, code, body)
				}
				for _, id := range normalPathInstancesFor(f, path) {
					if id == flaky.ID {
						reached++
					} else if id != healthy.ID {
						t.Fatalf("GET %s used unexpected instance %q", path, id)
					}
				}
				time.Sleep(200 * time.Millisecond)
			}
			if tuned && reached != 0 {
				t.Fatalf("the open circuit let %d requests reach the failed instance within open_seconds", reached)
			}
			if !tuned && reached == 0 {
				t.Fatal("without the rule the recovered instance never served again; the control does not show the rule's effect")
			}
		})
	}
}
