package mcpmanagement

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func workloadPlanArgs() map[string]any {
	return map[string]any{"app": "alpha", "image": testImage, "reason": "review native rollout"}
}

func TestWorkloadRolloutUsesNativeActivation(t *testing.T) {
	for _, mode := range []string{api.ExecutionModeService, api.ExecutionModeWorker} {
		t.Run(mode, func(t *testing.T) {
			f, cs, ctx := newReleaseFixture(t, true)
			f.app.Manifest.ExecutionMode = mode
			f.app.Manifest.ServiceReplicas = &api.ServiceReplicas{Min: 1, Max: 3, Desired: 2}
			f.app.Manifest.StopSignal = "SIGTERM"
			plan := callTool(t, ctx, cs, "gregale_plan_workload_rollout", workloadPlanArgs(), false)
			if string(plan["rollout_mode"]) != `"`+mode+`"` || !strings.Contains(string(plan["rollout_policy"]), "SIGTERM") {
				t.Fatal("missing native policy review")
			}
			id := planID(t, plan)
			callTool(t, ctx, cs, "gregale_apply_deploy", map[string]any{"plan_id": id}, true)
			callTool(t, ctx, cs, "gregale_apply_workload_rollout", map[string]any{"plan_id": id}, false)
			callTool(t, ctx, cs, "gregale_apply_workload_rollout", map[string]any{"plan_id": id}, false)
			callTool(t, ctx, cs, "gregale_promote_deployment", map[string]any{"plan_id": id}, true)
			observed := callTool(t, ctx, cs, "gregale_get_rollout", map[string]any{"app": "alpha", "deployment_id": "release-new"}, false)
			if mode == api.ExecutionModeService && string(observed["pending"]) != "true" {
				t.Fatal("live service reported complete before handoff")
			}
			if f.writes != 1 {
				t.Fatal("repeated apply wrote again")
			}
		})
	}
}

func TestWorkloadRolloutDurableAmbiguousRetry(t *testing.T) {
	for _, mode := range []string{api.ExecutionModeService, api.ExecutionModeWorker} {
		t.Run(mode, func(t *testing.T) {
			f, _, _ := newReleaseFixture(t, true)
			f.app.Manifest.ExecutionMode = mode
			backend := httptest.NewServer(http.HandlerFunc(f.serveHTTP))
			defer backend.Close()
			store, path, key := testStateStore(t)
			opts := Options{APIBase: backend.URL, Token: testToken, AllowWrites: true, planStore: store, planNamespace: "workload-grant"}
			cs, ctx := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
			id := planID(t, callTool(t, ctx, cs, "gregale_plan_workload_rollout", workloadPlanArgs(), false))
			f.ambiguous = true
			callTool(t, ctx, cs, "gregale_apply_workload_rollout", map[string]any{"plan_id": id}, true)
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			restored, err := OpenStateStore(path, key)
			if err != nil {
				t.Fatal(err)
			}
			defer restored.Close()
			opts.planStore = restored
			cs, ctx = connectConfiguredTestServer(t, backend, opts, "2026-07-28")
			callTool(t, ctx, cs, "gregale_apply_workload_rollout", map[string]any{"plan_id": id}, false)
			if f.writes != 1 {
				t.Fatal("restart created two generations")
			}
		})
	}
}

func TestWorkerRollbackRedeploysRetainedImage(t *testing.T) {
	f, cs, ctx := newReleaseFixture(t, true)
	f.app.Manifest.ExecutionMode = api.ExecutionModeWorker
	old := f.deployments["release-old"]
	old.Kind = "image"
	f.deployments[old.ID] = old
	plan := callTool(t, ctx, cs, "gregale_plan_workload_rollout", map[string]any{"app": "alpha", "retained_deployment_id": "release-old", "reason": "recover worker"}, false)
	if !strings.Contains(string(plan["traffic_effect"]), "CURRENT reviewed configuration") {
		t.Fatal("rollback silently restored old settings")
	}
	id := planID(t, plan)
	result := callTool(t, ctx, cs, "gregale_apply_workload_rollout", map[string]any{"plan_id": id}, false)
	var d deploymentView
	if err := json.Unmarshal(result["deployment"], &d); err != nil || d.ID == old.ID || d.ImageDigest != old.ImageDigest {
		t.Fatal("rollback did not create a generation from retained digest")
	}
}

func TestWorkloadRolloutRejectsChangedPolicyAndRequestMode(t *testing.T) {
	f, cs, ctx := newReleaseFixture(t, true)
	callTool(t, ctx, cs, "gregale_plan_workload_rollout", workloadPlanArgs(), true)
	f.app.Manifest.ExecutionMode = api.ExecutionModeWorker
	id := planID(t, callTool(t, ctx, cs, "gregale_plan_workload_rollout", workloadPlanArgs(), false))
	f.app.Manifest.StopSignal = "SIGKILL"
	callTool(t, ctx, cs, "gregale_apply_workload_rollout", map[string]any{"plan_id": id}, true)
	if f.writes != 0 {
		t.Fatal("stale stop policy created a generation")
	}
	callTool(t, ctx, cs, "gregale_plan_deploy", workloadPlanArgs(), true)
}

func TestServiceAbortPinsPairAndReportsAccepted(t *testing.T) {
	f, cs, ctx := newReleaseFixture(t, true)
	f.app.Manifest.ExecutionMode = api.ExecutionModeService
	id := planID(t, callTool(t, ctx, cs, "gregale_plan_workload_rollout", workloadPlanArgs(), false))
	callTool(t, ctx, cs, "gregale_apply_workload_rollout", map[string]any{"plan_id": id}, false)
	abort := callTool(t, ctx, cs, "gregale_plan_service_abort", map[string]any{"app": "alpha", "deployment_id": "release-new", "predecessor_deployment_id": "release-base", "reason": "recover service"}, false)
	result := callTool(t, ctx, cs, "gregale_apply_service_abort", map[string]any{"plan_id": planID(t, abort)}, false)
	if !strings.Contains(string(result["service_recovery"]), "accepted") {
		t.Fatal("missing asynchronous recovery receipt")
	}
	observed := callTool(t, ctx, cs, "gregale_get_rollout", map[string]any{"app": "alpha", "deployment_id": "release-new"}, false)
	if string(observed["pending"]) != "true" || !strings.Contains(string(observed["deployment"]), "abort") {
		t.Fatal("accepted abort reported completed")
	}
	callTool(t, ctx, cs, "gregale_apply_service_abort", map[string]any{"plan_id": planID(t, abort)}, false)
	if f.writes != 2 {
		t.Fatal("retry requested reverse handoff again")
	}
}

func TestNamedWorkloadRolloutConsentAndProtection(t *testing.T) {
	for _, mode := range []string{api.ExecutionModeService, api.ExecutionModeWorker} {
		t.Run(mode, func(t *testing.T) {
			f, b, opts := namedStageOptions(t)
			f.mode = mode
			grant := opts.Environments.Named["shop/staging"]
			grant.Write = false
			opts.Environments.Named["shop/staging"] = grant
			cs, ctx := connectConfiguredTestServer(t, b, opts, "2026-07-28")
			plan := callTool(t, ctx, cs, "gregale_plan_workload_rollout", namedStageArgs(), false)
			callTool(t, ctx, cs, "gregale_apply_workload_rollout", map[string]any{"plan_id": planID(t, plan)}, true)
			if f.writes != 0 {
				t.Fatal("read-only environment performed activation")
			}
			grant.Write = true
			opts.Environments.Named["shop/staging"] = grant
			cs, ctx = connectConfiguredTestServer(t, b, opts, "2026-07-28")
			plan = callTool(t, ctx, cs, "gregale_plan_workload_rollout", namedStageArgs(), false)
			f.protected = true
			callTool(t, ctx, cs, "gregale_apply_workload_rollout", map[string]any{"plan_id": planID(t, plan)}, true)
			callTool(t, ctx, cs, "gregale_plan_workload_rollout", namedStageArgs(), true)
			if f.writes != 0 {
				t.Fatal("protected target bypassed approval")
			}
			f.protected = false
			plan = callTool(t, ctx, cs, "gregale_plan_workload_rollout", namedStageArgs(), false)
			result := callTool(t, ctx, cs, "gregale_apply_workload_rollout", map[string]any{"plan_id": planID(t, plan)}, false)
			if !strings.Contains(string(result["staged_review"]), "env-staging") || f.writes != 1 {
				t.Fatal("named rollout lost committed review")
			}
			callTool(t, ctx, cs, "gregale_get_rollout", map[string]any{"app": "alpha", "project": "shop", "environment": "production", "deployment_id": "stage-new"}, true)
		})
	}
}
