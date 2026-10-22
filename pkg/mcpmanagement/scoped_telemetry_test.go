package mcpmanagement

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestScopedTelemetryRespectsEnvironmentConsent(t *testing.T) {
	f, b, opts := namedStageOptions(t)
	cs, ctx := connectConfiguredTestServer(t, b, opts, "2026-07-28")
	for _, tool := range []string{"gregale_get_routes", "gregale_get_operational_summary"} {
		out := callTool(t, ctx, cs, tool, map[string]any{"app": "alpha", "project": "shop", "environment": "staging"}, false)
		if string(out["scope"]) != `"staging"` || string(out["environment_id"]) != `"env-staging"` {
			t.Fatal("scoped identity missing")
		}
		callTool(t, ctx, cs, tool, map[string]any{"app": "alpha"}, true)
		callTool(t, ctx, cs, tool, map[string]any{"app": "alpha", "project": "shop", "environment": "production"}, true)
	}
	if f.writes != 0 {
		t.Fatal("telemetry made a write")
	}
}
func TestScopedTelemetryRejectsLegacyAggregateResponses(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/apps/alpha" {
			_, _ = w.Write([]byte(`{"id":"app-one","slug":"alpha"}`))
			return
		}
		if r.URL.Query().Get("scope") != "default" {
			t.Error("default selector missing")
		}
		if strings.HasSuffix(r.URL.Path, "/routes") {
			_, _ = w.Write([]byte(`{"app_id":"app-one","routes":["PRIVATE OTHER ENV"],"source":"live"}`))
			return
		}
		_, _ = w.Write([]byte(`{"version":1,"app_id":"app-one","recovery":{"rollbacks":[{"scope":"production"}]}}`))
	}))
	defer backend.Close()
	cs, ctx := connectTestServer(t, backend, 0, "")
	for _, tool := range []string{"gregale_get_routes", "gregale_get_operational_summary"} {
		out := callTool(t, ctx, cs, tool, map[string]any{"app": "alpha"}, true)
		if string(out["code"]) != `"invalid_response"` {
			t.Fatal("legacy aggregate was exposed")
		}
	}
}
func TestWorkerRolloutIncludesNativeResidencyObservation(t *testing.T) {
	f, cs, ctx := newReleaseFixture(t, true)
	f.app.Manifest.ExecutionMode = "worker"
	id := planID(t, callTool(t, ctx, cs, "gregale_plan_workload_rollout", workloadPlanArgs(), false))
	callTool(t, ctx, cs, "gregale_apply_workload_rollout", map[string]any{"plan_id": id}, false)
	out := callTool(t, ctx, cs, "gregale_get_rollout", map[string]any{"app": "alpha", "deployment_id": "release-new"}, false)
	if string(out["completion_basis"]) != `"resident_generation_snapshot"` || !strings.Contains(string(out["worker_observation"]), `"queue_processing_confirmed":false`) {
		t.Fatal("worker progress collapsed into image lifecycle")
	}
}
