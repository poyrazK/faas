package mcpmanagement

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func sourcePlanArgs() map[string]any {
	return map[string]any{"app": "alpha", "repo": "team/repository", "ref": "main", "reason": "build reviewed source"}
}

func TestSourcePlanPinsCommitAndObservesBuild(t *testing.T) {
	f, cs, ctx := newReleaseFixture(t, true)
	plan := callTool(t, ctx, cs, "gregale_plan_source_deploy", sourcePlanArgs(), false)
	var source api.SourceReviewResponse
	if err := json.Unmarshal(plan["source"], &source); err != nil {
		t.Fatal(err)
	}
	if source.CommitSHA != strings.Repeat("a", 40) || source.RepositoryID != 987 {
		t.Fatal("missing immutable source review")
	}
	id := planID(t, plan)
	f.mu.Lock()
	f.sourceHead = strings.Repeat("b", 40)
	f.mu.Unlock()
	staged := callTool(t, ctx, cs, "gregale_apply_deploy", map[string]any{"plan_id": id}, false)
	if !strings.Contains(string(staged["deployment"]), strings.Repeat("a", 40)) {
		t.Fatal("moving branch replaced reviewed source")
	}
	status := callTool(t, ctx, cs, "gregale_wait_deployment", map[string]any{"app": "alpha", "deployment_id": "release-new", "wait_seconds": 1}, false)
	if !strings.Contains(string(status["build"]), "queued") || string(status["pending"]) != "true" {
		t.Fatal("missing build progress")
	}
	callTool(t, ctx, cs, "gregale_promote_deployment", map[string]any{"plan_id": id}, true)
	f.mu.Lock()
	d := f.deployments["release-new"]
	d.Status = "live"
	f.deployments[d.ID] = d
	f.mu.Unlock()
	callTool(t, ctx, cs, "gregale_promote_deployment", map[string]any{"plan_id": id}, false)
}

func TestSourcePlanDurableAmbiguousRetryDoesNotBuildTwice(t *testing.T) {
	f, _, _ := newReleaseFixture(t, true)
	backend := httptest.NewServer(http.HandlerFunc(f.serveHTTP))
	defer backend.Close()
	store, path, key := testStateStore(t)
	opts := Options{APIBase: backend.URL, Token: testToken, AllowWrites: true, planStore: store, planNamespace: "source-grant"}
	cs, ctx := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
	id := planID(t, callTool(t, ctx, cs, "gregale_plan_source_deploy", sourcePlanArgs(), false))
	f.mu.Lock()
	f.ambiguous = true
	f.mu.Unlock()
	callTool(t, ctx, cs, "gregale_apply_deploy", map[string]any{"plan_id": id}, true)
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
	callTool(t, ctx, cs, "gregale_apply_deploy", map[string]any{"plan_id": id}, false)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writes != 1 {
		t.Fatal("retry created a second build")
	}
}

func TestSourcePlanRejectsUnsafeInputsAndChangedConfiguration(t *testing.T) {
	f, cs, ctx := newReleaseFixture(t, true)
	for _, input := range []map[string]any{
		{"app": "alpha", "repo": "https://github.com/team/repo", "ref": "main", "reason": "test"},
		{"app": "alpha", "repo": "team/repo", "ref": "refs/tags/latest", "reason": "test"},
		{"app": "alpha", "repo": "team/repo", "ref": "main?token=credential", "reason": "test"},
	} {
		callTool(t, ctx, cs, "gregale_plan_source_deploy", input, true)
	}
	id := planID(t, callTool(t, ctx, cs, "gregale_plan_source_deploy", sourcePlanArgs(), false))
	f.mu.Lock()
	f.app.RAMMB = 512
	f.mu.Unlock()
	result := callTool(t, ctx, cs, "gregale_apply_deploy", map[string]any{"plan_id": id}, true)
	if string(result["code"]) != `"plan_stale"` {
		t.Fatal("changed configuration admitted source build")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writes != 0 {
		t.Fatal("stale source plan changed deployment state")
	}
}

func TestNativeSourceRolloutDurableRetry(t *testing.T) {
	for _, mode := range []string{api.ExecutionModeService, api.ExecutionModeWorker} {
		t.Run(mode, func(t *testing.T) {
			f, _, _ := newReleaseFixture(t, true)
			f.app.Manifest.ExecutionMode = mode
			backend := httptest.NewServer(http.HandlerFunc(f.serveHTTP))
			defer backend.Close()
			store, path, key := testStateStore(t)
			opts := Options{APIBase: backend.URL, Token: testToken, AllowWrites: true, planStore: store, planNamespace: "native-source"}
			cs, ctx := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
			args := sourcePlanArgs()
			args["rollout_mode"] = mode
			plan := callTool(t, ctx, cs, "gregale_plan_source_deploy", args, false)
			id := planID(t, plan)
			callTool(t, ctx, cs, "gregale_apply_deploy", map[string]any{"plan_id": id}, true)
			f.mu.Lock()
			f.ambiguous = true
			f.sourceHead = strings.Repeat("b", 40)
			f.mu.Unlock()
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
			result := callTool(t, ctx, cs, "gregale_apply_workload_rollout", map[string]any{"plan_id": id}, false)
			var d deploymentView
			if json.Unmarshal(result["deployment"], &d) != nil || d.CommitSHA != strings.Repeat("a", 40) || d.BuildID != "build-one" {
				t.Fatal("restart changed source provenance")
			}
			callTool(t, ctx, cs, "gregale_apply_workload_rollout", map[string]any{"plan_id": id}, false)
			callTool(t, ctx, cs, "gregale_promote_deployment", map[string]any{"plan_id": id}, true)
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.writes != 1 {
				t.Fatal("source retry created another generation/build")
			}
		})
	}
}
