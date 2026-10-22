package mcpmanagement

import (
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/api"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type namedStageFixture struct {
	mu                           sync.Mutex
	envID, hash, current, latest string
	mode                         string
	protected, ambiguous         bool
	writes                       int
	requests                     []string
	keys                         []string
	cache                        map[string]api.DeploymentResponse
}

func (f *namedStageFixture) review() api.AppReleaseReview {
	serving := []string{}
	if f.current != "" {
		serving = append(serving, f.current)
	}
	return api.AppReleaseReview{AppID: "app-one", Scope: "staging", ProjectID: "project-one", EnvironmentID: f.envID, WorkloadRevision: 1, WorkloadConfigHash: strings.Repeat("c", 64), ConfigurationHash: f.hash, LatestDeploymentID: f.latest, ServingDeploymentIDs: serving, Protected: f.protected}
}

func (f *namedStageFixture) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	encode := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	if r.Method == http.MethodPost && r.URL.Path == "/v1/apps/alpha/deployments" {
		body, _ := io.ReadAll(r.Body)
		key := r.Header.Get("Idempotency-Key")
		f.requests = append(f.requests, string(body))
		f.keys = append(f.keys, key)
		if cached, ok := f.cache[key]; ok {
			encode(cached)
			return
		}
		var req api.CreateDeploymentRequest
		if json.Unmarshal(body, &req) != nil || req.Scope != "" || req.Environment != "staging" || req.ExpectedReview == nil || req.ExpectedReview.EnvironmentID != f.envID || (req.NativeRollout == "" && (req.TrafficPercent == nil || *req.TrafficPercent != 0)) || (req.NativeRollout != "" && (req.TrafficPercent != nil || req.NativeRollout != f.mode)) {
			w.WriteHeader(400)
			return
		}
		f.writes++
		f.latest = "stage-new"
		review := f.review()
		d := api.DeploymentResponse{ID: "stage-new", AppID: "app-one", Scope: "staging", Kind: "image", ImageDigest: req.Image, Status: "live", TrafficPercent: 0, StagedReview: &review}
		if req.NativeRollout == api.ExecutionModeService {
			d.RolloutState = "rolling_out"
		}
		if req.NativeRollout == api.ExecutionModeWorker {
			d.TrafficPercent = 100
			f.current = d.ID
		}
		f.cache[key] = d
		if f.ambiguous {
			f.ambiguous = false
			w.WriteHeader(504)
			return
		}
		encode(d)
		return
	}
	if r.Method == http.MethodPatch {
		var req api.UpdateDeploymentTrafficRequest
		if json.NewDecoder(r.Body).Decode(&req) != nil || req.ExpectedReview == nil || req.ExpectedReview.ConfigurationHash != f.hash || req.ExpectedServingDeploymentID == nil || *req.ExpectedServingDeploymentID != f.current {
			api.WriteProblem(w, api.NewProblem(409, api.CodeReleaseReviewChanged, "Changed", "review changed"))
			return
		}
		f.writes++
		f.current = "stage-new"
		encode(api.DeploymentResponse{ID: "stage-new", AppID: "app-one", Scope: "staging", Status: "live", TrafficPercent: 100})
		return
	}
	switch r.URL.Path {
	case "/v1/account":
		encode(api.AccountResponse{ID: "account-one"})
	case "/v1/projects/shop":
		encode(api.ProjectResponse{ProjectSummaryResponse: api.ProjectSummaryResponse{ID: "project-one", Slug: "shop"}, Workloads: []api.ProjectWorkloadResponse{{Slug: "alpha"}}})
	case "/v1/projects/shop/environments/staging":
		encode(api.ProjectEnvironmentResponse{ID: f.envID, ProjectID: "project-one", Slug: "staging", Protected: f.protected})
	case "/v1/apps/alpha":
		encode(api.AppResponse{ID: "app-one", Slug: "alpha", Type: "app", RAMMB: 256, Manifest: api.AppManifest{ExecutionMode: f.mode}})
	case "/v1/apps/alpha/routes":
		if r.URL.Query().Get("environment") != "staging" {
			w.WriteHeader(400)
			return
		}
		encode(api.AppRoutesResponse{AppID: "app-one", Slug: "alpha", Scope: "staging", ProjectID: "project-one", EnvironmentID: f.envID, Coverage: "deployment_attributed_history", Source: "partial", Routes: []string{"GET /staging-only"}})
	case "/v1/apps/alpha/operational-summary":
		if r.URL.Query().Get("environment") != "staging" {
			w.WriteHeader(400)
			return
		}
		encode(api.AppOperationalSummary{Version: 1, AppID: "app-one", Scope: "staging", ProjectID: "project-one", EnvironmentID: f.envID, Recovery: api.AppOperationalRecovery{Coverage: "explicit_scope_only", Rollbacks: []api.AppOperationalRollback{}, Restarts: []api.RuntimeConfigRestartStatusResponse{}}})
	case "/v1/apps/alpha/release-review":
		encode(f.review())
	case "/v1/apps/alpha/health":
		if r.URL.Query().Get("environment") != "staging" {
			w.WriteHeader(400)
			return
		}
		review := f.review()
		encode(api.AppHealthResponse{AppID: "app-one", Scope: "staging", ProjectID: "project-one", EnvironmentID: f.envID, LatestDeploymentID: review.LatestDeploymentID, ServingDeploymentIDs: review.ServingDeploymentIDs, Requests: &api.AppHealthRequests{Known: true, RequestCount: 12, ServerErrors: 1, WindowSeconds: 300, Coverage: "serving_deployments", DeploymentIDs: review.ServingDeploymentIDs}})
	case "/v1/deployments/stage-base":
		encode(api.DeploymentResponse{ID: "stage-base", AppID: "app-one", Scope: "staging", Status: "live", TrafficPercent: 100})
	case "/v1/deployments/stage-new":
		encode(api.DeploymentResponse{ID: "stage-new", AppID: "app-one", Scope: "staging", Status: "live", TrafficPercent: 0})
	default:
		w.WriteHeader(404)
	}
}

func namedStageOptions(t *testing.T) (*namedStageFixture, *httptest.Server, Options) {
	f := &namedStageFixture{envID: "env-staging", hash: strings.Repeat("a", 64), current: "stage-base", latest: "stage-base", cache: map[string]api.DeploymentResponse{}}
	b := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(b.Close)
	opts := environmentTestOptions(b, true)
	delete(opts.Environments.Named, "shop/production")
	g := opts.Environments.Named["shop/staging"]
	g.Write = true
	opts.Environments.Named["shop/staging"] = g
	return f, b, opts
}

func namedStageArgs() map[string]any {
	return map[string]any{"app": "alpha", "project": "shop", "environment": "staging", "image": testImage, "reason": "verify staging before production"}
}

func TestNamedStagingPlanApplyInspectAndPromote(t *testing.T) {
	f, b, opts := namedStageOptions(t)
	cs, ctx := connectConfiguredTestServer(t, b, opts, "2026-07-28")
	plan := callTool(t, ctx, cs, "gregale_plan_deploy", namedStageArgs(), false)
	id := planID(t, plan)
	if string(plan["environment_id"]) != `"env-staging"` || string(plan["workload_revision"]) != "1" {
		t.Fatal("named plan lacks revision binding")
	}
	callTool(t, ctx, cs, "gregale_apply_deploy", map[string]any{"plan_id": id}, false)
	callTool(t, ctx, cs, "gregale_wait_deployment", map[string]any{"app": "alpha", "project": "shop", "environment": "staging", "deployment_id": "stage-new"}, false)
	health := callTool(t, ctx, cs, "gregale_get_health", map[string]any{"app": "alpha", "project": "shop", "environment": "staging"}, false)
	if string(health["scope"]) != `"staging"` {
		t.Fatal("health scope mismatch")
	}
	metrics := callTool(t, ctx, cs, "gregale_get_metrics", map[string]any{"app": "alpha", "project": "shop", "environment": "staging"}, false)
	if string(metrics["source"]) != `"prometheus"` || !strings.Contains(string(metrics["requests"]), `"request_count":12`) {
		t.Fatal("scoped request metrics missing")
	}
	callTool(t, ctx, cs, "gregale_promote_deployment", map[string]any{"plan_id": id}, false)
	callTool(t, ctx, cs, "gregale_get_health", map[string]any{"app": "alpha", "project": "shop", "environment": "production"}, true)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writes != 2 {
		t.Fatal("named workflow repeated mutations")
	}
}

func TestNamedStagingConsentAndChangedProtection(t *testing.T) {
	f, b, opts := namedStageOptions(t)
	g := opts.Environments.Named["shop/staging"]
	g.Write = false
	opts.Environments.Named["shop/staging"] = g
	cs, ctx := connectConfiguredTestServer(t, b, opts, "2026-07-28")
	plan := callTool(t, ctx, cs, "gregale_plan_deploy", namedStageArgs(), false)
	callTool(t, ctx, cs, "gregale_apply_deploy", map[string]any{"plan_id": planID(t, plan)}, true)
	g.Write = true
	opts.Environments.Named["shop/staging"] = g
	cs, ctx = connectConfiguredTestServer(t, b, opts, "2026-07-28")
	plan = callTool(t, ctx, cs, "gregale_plan_deploy", namedStageArgs(), false)
	id := planID(t, plan)
	callTool(t, ctx, cs, "gregale_apply_deploy", map[string]any{"plan_id": id}, false)
	f.mu.Lock()
	f.protected = true
	f.mu.Unlock()
	result := callTool(t, ctx, cs, "gregale_promote_deployment", map[string]any{"plan_id": id}, true)
	if string(result["code"]) != `"approval_required"` {
		t.Fatal("protection change was not enforced")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writes != 1 {
		t.Fatal("unconsented/protected mutation reached API")
	}
}

func TestNamedStagingAmbiguousRetrySurvivesRestart(t *testing.T) {
	f, b, opts := namedStageOptions(t)
	store, path, key := testStateStore(t)
	opts.planStore = store
	opts.planNamespace = "named-grant"
	cs, ctx := connectConfiguredTestServer(t, b, opts, "2026-07-28")
	plan := callTool(t, ctx, cs, "gregale_plan_deploy", namedStageArgs(), false)
	id := planID(t, plan)
	f.mu.Lock()
	f.ambiguous = true
	f.mu.Unlock()
	callTool(t, ctx, cs, "gregale_apply_deploy", map[string]any{"plan_id": id}, true)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := OpenStateStore(path, key)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	opts.planStore = next
	cs, ctx = connectConfiguredTestServer(t, b, opts, "2026-07-28")
	callTool(t, ctx, cs, "gregale_apply_deploy", map[string]any{"plan_id": id}, false)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writes != 1 || len(f.keys) != 2 || f.keys[0] != f.keys[1] || f.requests[0] != f.requests[1] {
		t.Fatal("restart changed named staging request identity")
	}
}
