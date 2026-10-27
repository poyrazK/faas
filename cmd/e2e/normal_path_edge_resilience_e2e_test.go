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

	// The dead sibling is evicted after its failure; bring it back so a POST
	// can land on it, still failing.
	notifyNormalPathInstanceChanged(t, f, dead.ID, string(state.StateRunning))
	for _, withKey := range []bool{false, true} {
		hit := false
		for i := 0; i < 10 && !hit; i++ {
			path := fmt.Sprintf("/edge-retry/post/%t/%d", withKey, i)
			headers := map[string]string{}
			if withKey {
				headers["Idempotency-Key"] = fmt.Sprintf("order-%d", i)
			}
			_, body, code := doReqHeaders(t, f.h, f.host, http.MethodPost, path, map[string]string{"order": "1"}, headers)
			seen := normalPathInstancesFor(f, path)
			if len(seen) == 0 || seen[0] != dead.ID {
				continue
			}
			hit = true
			switch {
			case withKey && (code != http.StatusOK || len(seen) != 2 || seen[1] != healthy.ID):
				t.Fatalf("POST with Idempotency-Key: status=%d body=%q attempts=%v, want a replay on the healthy sibling", code, body, seen)
			case !withKey && (code != http.StatusServiceUnavailable || len(seen) != 1):
				t.Fatalf("POST without Idempotency-Key: status=%d attempts=%v, want one attempt and 503", code, seen)
			}
			notifyNormalPathInstanceChanged(t, f, dead.ID, string(state.StateRunning))
		}
		if !hit {
			t.Fatalf("no POST (Idempotency-Key=%t) reached the dead sibling", withKey)
		}
	}
}

// TestE2E_NormalPath_EdgeCircuitBreakerKeepsFailedInstanceOut: a
// kind=circuit_breaker rule with min_requests=1 opens an instance's circuit
// on its first transport failure and keeps it out of rotation for
// open_seconds, even after the instance comes back. The default breaker
// needs five failures, so without the rule one failure does not open it and
// the instance serves again once it rejoins.
func TestE2E_NormalPath_EdgeCircuitBreakerKeepsFailedInstanceOut(t *testing.T) {
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
			}
			// The gateway loads an app's breaker tuning in the background on
			// first use; let one request start the load and give it time.
			doReqHeaders(t, f.h, f.host, http.MethodGet, "/edge-circuit/warmup", nil)
			time.Sleep(time.Second)

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

			// The instance recovers and rejoins the target set.
			f.vmmd.FailAll(flaky.ID, nil)
			notifyNormalPathInstanceChanged(t, f, flaky.ID, string(state.StateRunning))
			time.Sleep(500 * time.Millisecond)
			reached := 0
			for i := 0; i < 10; i++ {
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
			}
			if tuned && reached != 0 {
				t.Fatalf("the open circuit let %d requests reach the failed instance within open_seconds", reached)
			}
			if !tuned && reached == 0 {
				t.Fatal("without the rule one failure kept the recovered instance out; the control does not show the rule's effect")
			}
		})
	}
}
