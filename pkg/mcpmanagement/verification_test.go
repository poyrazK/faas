// adr: 20261103202626
package mcpmanagement

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type verificationFixture struct {
	operation               *operationFixture
	mu                      sync.Mutex
	state, healthStatus     string
	wakeID                  string
	completed, evaluated    time.Time
	deny, revokeAfterHealth atomic.Bool
	changeConfigAfterHealth atomic.Bool
	recreateAfterHealth     atomic.Bool
	healthReads             atomic.Int64
}

func newVerificationFixture(t *testing.T) *verificationFixture {
	t.Helper()
	return &verificationFixture{operation: &operationFixture{base: newLocalFixture(t, true, "release-base"), revision: 1, values: map[string]string{}}, state: "queued", healthStatus: "healthy", wakeID: "5c606438-93d7-4cfa-840a-9427c3c57b30", completed: time.Now().Add(-2 * time.Second), evaluated: time.Now().Add(-time.Second)}
}

func (f *verificationFixture) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if f.deny.Load() {
		api.WriteProblem(w, api.NewProblem(403, "forbidden", "Revoked", "access revoked"))
		return
	}
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/alpha/restart":
		f.operation.mu.Lock()
		f.operation.restarts++
		f.operation.mu.Unlock()
		_ = json.NewEncoder(w).Encode(api.AppRestartResponse{WakeID: f.wakeID})
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/apps/alpha/runtime-config-restarts/"):
		f.mu.Lock()
		defer f.mu.Unlock()
		out := api.RuntimeConfigRestartStatusResponse{WakeID: f.wakeID, Status: f.state, RequestedAt: f.completed.Add(-time.Minute)}
		if f.state == "completed" {
			out.CompletedAt = &f.completed
		}
		_ = json.NewEncoder(w).Encode(out)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/alpha/health":
		f.healthReads.Add(1)
		f.operation.base.mu.Lock()
		appID := f.operation.base.app.ID
		if f.recreateAfterHealth.Load() {
			f.operation.base.app.ID = "replacement-app"
		}
		f.operation.base.mu.Unlock()
		f.mu.Lock()
		out := api.AppHealthResponse{AppID: appID, Scope: "default", Status: f.healthStatus, LatestDeploymentID: "release-base", ServingDeploymentIDs: []string{"release-base"}, EvaluatedAt: f.evaluated.UTC().Format(time.RFC3339Nano), ValidForSeconds: 120}
		f.mu.Unlock()
		if f.revokeAfterHealth.Load() {
			f.deny.Store(true)
		}
		if f.changeConfigAfterHealth.Load() {
			f.operation.mu.Lock()
			f.operation.revision++
			f.operation.mu.Unlock()
		}
		_ = json.NewEncoder(w).Encode(out)
	default:
		f.operation.serveHTTP(w, r)
	}
}

func verificationOf(t *testing.T, fields map[string]json.RawMessage, want string) Verification {
	t.Helper()
	var v Verification
	if err := json.Unmarshal(fields["verification"], &v); err != nil || v.Status != want {
		t.Fatalf("verification=%+v want=%s err=%v", v, want, err)
	}
	return v
}

func applyVerificationRestart(t *testing.T, agent *workflowDriver) string {
	t.Helper()
	plan := agent.call("gregale_plan_env_change", map[string]any{"app": "alpha", "reason": "verify restart", "values": map[string]string{"LOG_LEVEL": "debug"}, "restart": true}, false)
	id := planID(t, plan)
	result := agent.call("gregale_apply_env_change", map[string]any{"plan_id": id}, false)
	v := verificationOf(t, result, "required")
	if v.Next == nil || v.Next.Tool != "gregale_verify_plan" || v.Next.Arguments["plan_id"] != id || v.Checks[0].ResourceID == "" {
		t.Fatal("apply omitted exact verification obligation")
	}
	return id
}

func TestAgentWorkflowVerificationReconnectsWhileRestartPending(t *testing.T) {
	store, path, key := testStateStore(t)
	f := newVerificationFixture(t)
	backend := httptest.NewServer(http.HandlerFunc(f.serveHTTP))
	defer backend.Close()
	opts := workflowOptions(backend, store)
	agent := workflowAgent(t, backend, opts)
	if agent.tools["gregale_get_restart"] == nil || agent.tools["gregale_verify_plan"] == nil {
		t.Fatal("core omitted verification reads")
	}
	id := applyVerificationRestart(t, agent)
	completed := agent.call("gregale_get_plan", map[string]any{"plan_id": id}, false)
	if string(completed["status"]) != `"completed"` {
		t.Fatal("mutation completion changed semantics")
	}
	verificationOf(t, completed, "required")
	pending := agent.call("gregale_verify_plan", map[string]any{"plan_id": id}, false)
	v := verificationOf(t, pending, "pending")
	if v.Next == nil || v.Next.AfterSeconds == nil || *v.Next.AfterSeconds != 2 || f.healthReads.Load() != 0 {
		t.Fatal("pending restart was polled incorrectly")
	}
	_ = agent.client.Close()
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStateStore(path, key)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	opts.planStore = reopened
	f.mu.Lock()
	f.state = "completed"
	f.mu.Unlock()
	restored := workflowAgent(t, backend, opts)
	result := restored.call("gregale_verify_plan", map[string]any{"plan_id": id}, false)
	v = verificationOf(t, result, "verified")
	if v.CheckedAt == nil || v.Next != nil || v.Checks[0].ResourceID != f.wakeID {
		t.Fatal("restored verification lost target or observation time")
	}
	f.operation.mu.Lock()
	defer f.operation.mu.Unlock()
	if f.operation.puts != 1 || f.operation.restarts != 1 || agent.calls+restored.calls > 10 {
		t.Fatal("verification repeated writes or exceeded call budget")
	}
}

func TestAgentWorkflowVerificationObservesLateFailureAndStaleHealth(t *testing.T) {
	for _, scenario := range []string{"restart_failed", "health_stale", "health_before_restart", "health_unhealthy", "config_changed", "config_changed_during_health"} {
		t.Run(scenario, func(t *testing.T) {
			f := newVerificationFixture(t)
			backend := httptest.NewServer(http.HandlerFunc(f.serveHTTP))
			defer backend.Close()
			agent := workflowAgent(t, backend, workflowOptions(backend, nil))
			id := applyVerificationRestart(t, agent)
			f.mu.Lock()
			f.state = "completed"
			want := "unknown"
			switch scenario {
			case "restart_failed":
				f.state = "failed"
				want = "failed"
			case "health_stale":
				f.evaluated = time.Now().Add(-time.Hour)
			case "health_before_restart":
				f.evaluated = f.completed.Add(-time.Second)
			case "health_unhealthy":
				f.healthStatus = "unhealthy"
				want = "failed"
			case "config_changed":
				f.operation.mu.Lock()
				f.operation.revision++
				f.operation.mu.Unlock()
				want = "blocked"
			case "config_changed_during_health":
				f.changeConfigAfterHealth.Store(true)
				want = "blocked"
			}
			f.mu.Unlock()
			result := agent.call("gregale_verify_plan", map[string]any{"plan_id": id}, false)
			verificationOf(t, result, want)
			if f.operation.puts != 1 || f.operation.restarts != 1 {
				t.Fatal("failed verification repeated a mutation")
			}
		})
	}
}

func TestAgentWorkflowVerificationWithholdsResultsOnRevocation(t *testing.T) {
	for _, scenario := range []string{"before_read", "during_health_read", "identity_changed_during_health"} {
		t.Run(scenario, func(t *testing.T) {
			f := newVerificationFixture(t)
			backend := httptest.NewServer(http.HandlerFunc(f.serveHTTP))
			defer backend.Close()
			agent := workflowAgent(t, backend, workflowOptions(backend, nil))
			id := applyVerificationRestart(t, agent)
			f.mu.Lock()
			f.state = "completed"
			f.mu.Unlock()
			f.revokeAfterHealth.Store(scenario == "during_health_read")
			f.deny.Store(scenario == "before_read")
			f.recreateAfterHealth.Store(scenario == "identity_changed_during_health")
			result := agent.call("gregale_verify_plan", map[string]any{"plan_id": id}, true)
			recoveryOf(t, result, "request_access")
			if len(result["verification"]) != 0 || f.operation.puts != 1 || f.operation.restarts != 1 {
				t.Fatal("revoked verification exposed success or repeated a write")
			}
		})
	}
}

func TestAgentWorkflowVerificationRequiresExplicitPromotion(t *testing.T) {
	f, _, _ := newReleaseFixture(t, true)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/builds/") {
			t.Error("verification requested account-level build details")
			w.WriteHeader(403)
			return
		}
		if r.URL.Path == "/v1/apps/alpha/health" {
			f.mu.Lock()
			defer f.mu.Unlock()
			health := watchHealthy(time.Now())
			health.AppID = f.app.ID
			health.Scope = "default"
			health.LatestDeploymentID = f.latest
			health.ServingDeploymentIDs = []string{f.current}
			_ = json.NewEncoder(w).Encode(health)
			return
		}
		f.serveHTTP(w, r)
	}))
	defer backend.Close()
	agent := workflowAgent(t, backend, Options{APIBase: backend.URL, Token: testToken, AllowWrites: true, Toolsets: []string{ToolsetAll}})
	plan := agent.call("gregale_plan_deploy", map[string]any{"app": "alpha", "reason": "verify staged release", "image": testImage}, false)
	id := planID(t, plan)
	agent.call("gregale_apply_deploy", map[string]any{"plan_id": id}, false)
	f.mu.Lock()
	d := f.deployments["release-new"]
	d.RolloutState = "complete"
	d.BuildID = "private-build"
	f.deployments[d.ID] = d
	writes := f.writes
	f.mu.Unlock()
	result := agent.call("gregale_verify_plan", map[string]any{"plan_id": id}, false)
	v := verificationOf(t, result, "blocked")
	if v.Checks[0].Reason != "explicit_promotion_required" || v.Next != nil {
		t.Fatal("staged release was treated as serving")
	}
	f.mu.Lock()
	if f.writes != writes {
		t.Fatal("verification promoted a release")
	}
	f.mu.Unlock()
	agent.call("gregale_promote_deployment", map[string]any{"plan_id": id}, false)
	result = agent.call("gregale_verify_plan", map[string]any{"plan_id": id}, false)
	verificationOf(t, result, "verified")
}

func TestVerificationContractsRequireResultsAndPagination(t *testing.T) {
	v := Verification{Status: "unknown", Goal: "manual_review", Checks: []verificationCheck{}}
	for _, name := range []string{"gregale_apply_deploy", "gregale_apply_local_deploy", "gregale_apply_env_change", "gregale_apply_operation", "gregale_apply_provision", "gregale_apply_environment_promotion", "gregale_apply_environment_rollback", "gregale_apply_route_budgets", "gregale_apply_regression_action"} {
		if err := validateToolEnvelope(name, mustJSON(t, map[string]any{"plan_id": "reviewed", "verification": v})); err == nil {
			t.Errorf("%s accepted a receipt without a result", name)
		}
	}
	for _, name := range []string{"gregale_list_apps", "gregale_list_deployments", "gregale_list_plans", "gregale_list_domains"} {
		if err := validateToolEnvelope(name, json.RawMessage(`{"items":[]}`)); err == nil {
			t.Errorf("%s accepted missing pagination", name)
		}
	}
	raw, err := addToolPagination("gregale_list_apps", json.RawMessage(`{"items":[],"total":9007199254740993,"next_offset":20}`))
	if err != nil || !strings.Contains(string(raw), "9007199254740993") {
		t.Fatal("pagination rounded native evidence")
	}
	validateWorkflowSchema(t, toolOutputSchema("gregale_list_apps"), raw)
	if err := validateToolEnvelope("gregale_list_apps", raw); err != nil {
		t.Fatal(err)
	}
	bad := json.RawMessage(`{"items":[],"pagination":{"mode":"offset","has_more":true}}`)
	if err := validateToolEnvelope("gregale_list_apps", bad); err == nil {
		t.Fatal("more results without a cursor were accepted")
	}
}

func TestVerificationContractsLargePlanInventoryIsPaged(t *testing.T) {
	m, _ := newOperationFixture(t)
	deadline := time.Now().Add(time.Minute)
	for i := range maxReleasePlans {
		id := fmt.Sprintf("%064x", i)
		m.plans[id] = &releasePlan{ID: id, App: "alpha", AppID: "app-one", Scope: "default", Action: "deploy", ExpiresAt: deadline,
			result: &api.DeploymentResponse{ID: "release-base", AppID: "app-one", Scope: "default", Status: "live"}}
	}
	seen := map[string]bool{}
	offset := 0
	for {
		result, err := m.listPlans(t.Context(), planListArgs{Offset: offset})
		if err != nil {
			t.Fatal(err)
		}
		page := result.(map[string]any)
		items := page["items"].([]planReceipt)
		if len(items) > defaultPageSize {
			t.Fatal("plan inventory exceeded its page bound")
		}
		raw, err := addToolPagination("gregale_list_plans", mustJSON(t, page))
		if err != nil || len(raw) > maxResultBytes {
			t.Fatal("plan page exceeded the protocol bound")
		}
		validateWorkflowSchema(t, toolOutputSchema("gregale_list_plans"), raw)
		if err := validateToolEnvelope("gregale_list_plans", raw); err != nil {
			t.Fatal(err)
		}
		for _, item := range items {
			if seen[item.PlanID] {
				t.Fatal("stable plan pagination duplicated an identity")
			}
			seen[item.PlanID] = true
		}
		next, more := page["next_offset"].(int)
		if !more {
			break
		}
		offset = next
	}
	if len(seen) != maxReleasePlans {
		t.Fatal("plan pagination omitted receipts")
	}
}

func TestVerificationContractsAbortDoesNotVerifyCandidateReadiness(t *testing.T) {
	m, _ := newOperationFixture(t)
	v, _ := m.planVerification(&releasePlan{ID: "reviewed", Action: "service_abort", App: "alpha", AppID: "app-one", Scope: "default",
		result: &api.DeploymentResponse{ID: "aborted-candidate", AppID: "app-one", Scope: "default", Status: "cancelled"}})
	if v.Status != "unknown" || v.Goal != "manual_review" || v.Next != nil || v.Checks[0].ResourceID != "aborted-candidate" {
		t.Fatal("service abort was mistaken for candidate activation")
	}
}
