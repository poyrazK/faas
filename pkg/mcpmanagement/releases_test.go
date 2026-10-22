package mcpmanagement

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

var testImage = "registry.example/team/app@sha256:" + strings.Repeat("a", 64)

type releaseFixture struct {
	mu          sync.Mutex
	account     string
	app         api.AppResponse
	current     string
	latest      string
	deployments map[string]api.DeploymentResponse
	keys        map[string]api.DeploymentResponse
	recoveries  map[string]api.RolloutTransitionResponse
	writes      int
	ambiguous   bool
	waitReads   int
	sourceHead  string
}

func newReleaseFixture(t *testing.T, writes bool) (*releaseFixture, *mcp.ClientSession, context.Context) {
	t.Helper()
	f := &releaseFixture{account: "account-one", app: api.AppResponse{ID: "app-one", Slug: "alpha", Type: "app", RAMMB: 256, Status: "active"}, current: "release-base", latest: "release-base", deployments: map[string]api.DeploymentResponse{}, keys: map[string]api.DeploymentResponse{}}
	f.deployments["release-base"] = api.DeploymentResponse{ID: "release-base", AppID: "app-one", Status: "live", Scope: "default", TrafficPercent: 100}
	f.deployments["release-old"] = api.DeploymentResponse{ID: "release-old", AppID: "app-one", Status: "superseded", Scope: "default", ImageDigest: testImage}
	backend := httptest.NewServer(http.HandlerFunc(f.serveHTTP))
	t.Cleanup(backend.Close)
	cs, ctx := connectConfiguredTestServer(t, backend, Options{APIBase: backend.URL, Token: testToken, AllowWrites: writes}, "2026-07-28")
	return f, cs, ctx
}

func (f *releaseFixture) serveHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if r.Header.Get("Authorization") != "Bearer "+testToken {
		w.WriteHeader(401)
		return
	}
	if r.Method != http.MethodGet {
		if r.URL.Path == "/v1/apps/alpha/source-review" {
			var input api.SourceReviewRequest
			if json.NewDecoder(r.Body).Decode(&input) != nil {
				w.WriteHeader(400)
				return
			}
			commit := f.sourceHead
			if commit == "" {
				commit = strings.Repeat("a", 40)
			}
			if sourceCommit.MatchString(input.Ref) {
				commit = input.Ref
			}
			serving := []string{}
			if f.current != "" {
				serving = append(serving, f.current)
			}
			_ = json.NewEncoder(w).Encode(api.SourceReviewResponse{AppID: f.app.ID, Repo: input.Repo, CommitSHA: commit, InstallationID: 55, RepositoryID: 987, ReleaseReview: api.AppReleaseReview{AppID: f.app.ID, ConfigurationHash: strings.Repeat("a", 64), LatestDeploymentID: f.latest, ServingDeploymentIDs: serving}})
			return
		}
		f.mutate(w, r)
		return
	}
	switch r.URL.Path {
	case "/v1/account":
		_ = json.NewEncoder(w).Encode(api.AccountResponse{ID: f.account})
	case "/v1/apps/alpha":
		_ = json.NewEncoder(w).Encode(f.app)
	case "/v1/apps/alpha/release-review":
		serving := []string{}
		if f.current != "" {
			serving = append(serving, f.current)
		}
		_ = json.NewEncoder(w).Encode(api.AppReleaseReview{AppID: f.app.ID, ConfigurationHash: strings.Repeat("a", 64), LatestDeploymentID: f.latest, ServingDeploymentIDs: serving})
	case "/v1/apps/alpha/health":
		ids := []string{}
		if f.current != "" {
			ids = append(ids, f.current)
		}
		_ = json.NewEncoder(w).Encode(api.AppHealthResponse{AppID: f.app.ID, Scope: "default", ServingDeploymentIDs: ids, LatestDeploymentID: f.latest})
	case "/v1/apps/alpha/rollouts/release-new/observation":
		_ = json.NewEncoder(w).Encode(api.WorkerRolloutObservation{AppID: f.app.ID, DeploymentID: "release-new", Scope: "default", Basis: "resident_generation_snapshot", SelectedDeploymentID: "release-new", Running: 1, GenerationConverged: true})
	case "/v1/builds/build-one":
		_ = json.NewEncoder(w).Encode(api.BuildResponse{ID: "build-one", DeploymentID: "release-new", Status: api.BuildStatusQueued})
	default:
		id := strings.TrimPrefix(r.URL.Path, "/v1/deployments/")
		d, ok := f.deployments[id]
		if !ok {
			w.WriteHeader(404)
			return
		}
		if id == "release-wait" {
			f.waitReads++
			if f.waitReads >= 2 {
				d.Status = "failed"
			}
		}
		_ = json.NewEncoder(w).Encode(d)
	}
}

func (f *releaseFixture) mutate(w http.ResponseWriter, r *http.Request) {
	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		http.Error(w, "missing idempotency key", 400)
		return
	}
	if old, ok := f.recoveries[key]; ok {
		_ = json.NewEncoder(w).Encode(old)
		return
	}
	if old, ok := f.keys[key]; ok {
		_ = json.NewEncoder(w).Encode(old)
		return
	}
	var d api.DeploymentResponse
	switch r.Method + " " + r.URL.Path {
	case "POST /v1/apps/alpha/deployments/source-ref":
		var body api.SourceRefDeployRequest
		if json.NewDecoder(r.Body).Decode(&body) != nil || body.ExpectedSource == nil || body.Ref != body.ExpectedSource.CommitSHA || !sourceCommit.MatchString(body.Ref) || !body.NoTriggers || (body.NativeRollout == "" && (body.TrafficPercent == nil || *body.TrafficPercent != 0)) || (body.NativeRollout != "" && (body.TrafficPercent != nil || body.NativeRollout != f.app.Manifest.EffectiveExecutionMode())) || body.SourceBranch != "" {
			http.Error(w, "unsafe source deploy", 400)
			return
		}
		d = api.DeploymentResponse{ID: "release-new", AppID: f.app.ID, Kind: "github", Status: "building", Scope: "default", CommitSHA: body.Ref, BuildID: "build-one"}
		if body.NativeRollout == api.ExecutionModeService {
			d.RolloutState = "rolling_out"
		}
		if body.NativeRollout == api.ExecutionModeWorker {
			d.TrafficPercent = 100
		}
		f.latest = d.ID
	case "POST /v1/apps/alpha/deployments":
		var body api.CreateDeploymentRequest
		if json.NewDecoder(r.Body).Decode(&body) != nil || body.Image != testImage || body.Scope != "default" || body.Reason == nil || body.ExpectedReview == nil || (body.NativeRollout == "" && (body.TrafficPercent == nil || *body.TrafficPercent != 0)) || (body.NativeRollout != "" && (body.TrafficPercent != nil || body.NativeRollout != f.app.Manifest.EffectiveExecutionMode())) {
			http.Error(w, "unsafe deploy body", 400)
			return
		}
		d = api.DeploymentResponse{ID: "release-new", AppID: "app-one", Kind: "image", Status: "live", Scope: "default", ImageDigest: body.Image, TrafficPercent: 0}
		if body.NativeRollout == api.ExecutionModeService {
			d.RolloutState = "rolling_out"
		}
		if body.NativeRollout == api.ExecutionModeWorker {
			d.TrafficPercent = 100
			f.current = d.ID
		}
		f.latest = d.ID
		f.app.DeploymentAvailability = "available"
	case "PATCH /v1/deployments/release-new/traffic":
		var body api.UpdateDeploymentTrafficRequest
		if json.NewDecoder(r.Body).Decode(&body) != nil || body.ExpectedServingDeploymentID == nil || body.TrafficPercent != 100 {
			http.Error(w, "missing traffic guard", 400)
			return
		}
		if *body.ExpectedServingDeploymentID != f.current {
			api.WriteProblem(w, api.NewProblem(409, "traffic_serving_changed", "Serving release changed", "Create a new release plan"))
			return
		}
		d = f.deployments["release-new"]
		d.TrafficPercent, f.current = 100, d.ID
	case "POST /v1/apps/alpha/rollouts/recover":
		var body api.RecoverRolloutRequest
		if json.NewDecoder(r.Body).Decode(&body) != nil || body.ExpectedReview == nil || body.DeploymentID != "release-new" || body.ExpectedPredecessorDeploymentID != "release-base" || body.Action != "abort" {
			http.Error(w, "unsafe recovery", 400)
			return
		}
		d = f.deployments[body.DeploymentID]
		d.ServiceRolloutHandoff = &api.ServiceRolloutHandoffResponse{Action: "abort", Phase: "pending", Generation: 3, PredecessorDeploymentID: body.ExpectedPredecessorDeploymentID}
		f.deployments[d.ID] = d
		f.writes++
		result := api.RolloutTransitionResponse{Deployment: d, ServiceRecovery: &api.ServiceRolloutRecoveryReceipt{DeploymentID: d.ID, PredecessorDeploymentID: body.ExpectedPredecessorDeploymentID, RequestID: "recovery-one", Status: "accepted"}}
		if f.recoveries == nil {
			f.recoveries = make(map[string]api.RolloutTransitionResponse)
		}
		f.recoveries[key] = result
		if f.ambiguous {
			f.ambiguous = false
			api.WriteProblem(w, api.NewProblem(503, "capacity", "Temporary failure", "Retry the same plan"))
			return
		}
		_ = json.NewEncoder(w).Encode(result)
		return
	case "POST /v1/apps/alpha/rollback":
		var body api.RollbackRequest
		if json.NewDecoder(r.Body).Decode(&body) != nil || body.ExpectedCurrentDeploymentID == nil || body.TargetDeploymentID == nil || body.Reason == "" {
			http.Error(w, "missing rollback guard", 400)
			return
		}
		if *body.ExpectedCurrentDeploymentID != f.current {
			api.WriteProblem(w, api.NewProblem(409, "rollback_current_changed", "Serving release changed", "Create a new rollback plan"))
			return
		}
		d = f.deployments[*body.TargetDeploymentID]
		d.RollbackOperation = &api.RollbackOperation{ID: "rollback-one", Status: "pending"}
	default:
		http.Error(w, "unexpected mutation", 400)
		return
	}
	f.writes++
	f.keys[key], f.deployments[d.ID] = d, d
	if f.ambiguous {
		f.ambiguous = false
		api.WriteProblem(w, api.NewProblem(503, "capacity", "Temporary failure", "Retry the same plan"))
		return
	}
	_ = json.NewEncoder(w).Encode(d)
}

func planID(t *testing.T, out map[string]json.RawMessage) string {
	t.Helper()
	var id string
	if err := json.Unmarshal(out["plan_id"], &id); err != nil || id == "" {
		t.Fatalf("plan=%s error=%v", out, err)
	}
	return id
}

func deployPlan(t *testing.T, ctx context.Context, cs *mcp.ClientSession) string {
	t.Helper()
	out := callTool(t, ctx, cs, "gregale_plan_deploy", map[string]any{"app": "alpha", "image": testImage, "reason": "Ship verified image"}, false)
	if string(out["account_id"]) != `"account-one"` || string(out["expected_current_deployment_id"]) != `"release-base"` || string(out["image"]) != fmt.Sprintf("%q", testImage) {
		t.Fatalf("plan identity=%s", out)
	}
	return planID(t, out)
}

func TestManagementReleaseJourney(t *testing.T) {
	f, cs, ctx := newReleaseFixture(t, true)
	id := deployPlan(t, ctx, cs)
	args := map[string]any{"plan_id": id}
	out := callTool(t, ctx, cs, "gregale_apply_deploy", args, false)
	if !strings.Contains(string(out["deployment"]), `"traffic_percent":0`) {
		t.Fatalf("staged=%s", out)
	}
	callTool(t, ctx, cs, "gregale_apply_deploy", args, false)
	callTool(t, ctx, cs, "gregale_wait_deployment", map[string]any{"app": "alpha", "deployment_id": "release-new"}, false)
	out = callTool(t, ctx, cs, "gregale_promote_deployment", args, false)
	if !strings.Contains(string(out["deployment"]), `"traffic_percent":100`) {
		t.Fatalf("promoted=%s", out)
	}
	callTool(t, ctx, cs, "gregale_promote_deployment", args, false)
	rollback := callTool(t, ctx, cs, "gregale_plan_rollback", map[string]any{"app": "alpha", "target_deployment_id": "release-old", "reason": "Restore last known release"}, false)
	rollbackArgs := map[string]any{"plan_id": planID(t, rollback)}
	out = callTool(t, ctx, cs, "gregale_apply_rollback", rollbackArgs, false)
	if !strings.Contains(string(out["rollback_operation"]), "rollback-one") {
		t.Fatalf("rollback=%s", out)
	}
	callTool(t, ctx, cs, "gregale_apply_rollback", rollbackArgs, false)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writes != 3 {
		t.Fatalf("writes=%d", f.writes)
	}
}

func TestManagementReleaseStalePlans(t *testing.T) {
	for _, change := range []string{"account", "app", "configuration", "serving", "latest"} {
		t.Run(change, func(t *testing.T) {
			f, cs, ctx := newReleaseFixture(t, true)
			id := deployPlan(t, ctx, cs)
			f.mu.Lock()
			switch change {
			case "account":
				f.account = "account-two"
			case "app":
				f.app.ID = "app-two"
			case "configuration":
				f.app.RAMMB = 512
			case "serving":
				f.current = "release-external"
			case "latest":
				f.latest = "release-external"
			}
			f.mu.Unlock()
			out := callTool(t, ctx, cs, "gregale_apply_deploy", map[string]any{"plan_id": id}, true)
			if string(out["code"]) != `"plan_stale"` && string(out["code"]) != `"account_changed"` {
				t.Fatalf("error=%s", out)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.writes != 0 {
				t.Fatal("stale plan mutated account")
			}
		})
	}
}

func TestManagementAmbiguousReleaseRetryAndServingRace(t *testing.T) {
	f, cs, ctx := newReleaseFixture(t, true)
	id := deployPlan(t, ctx, cs)
	f.mu.Lock()
	f.ambiguous = true
	f.mu.Unlock()
	args := map[string]any{"plan_id": id}
	callTool(t, ctx, cs, "gregale_apply_deploy", args, true)
	callTool(t, ctx, cs, "gregale_apply_deploy", args, false)
	f.mu.Lock()
	f.current = "release-external"
	f.mu.Unlock()
	out := callTool(t, ctx, cs, "gregale_promote_deployment", args, true)
	if string(out["code"]) != `"traffic_serving_changed"` {
		t.Fatalf("race=%s", out)
	}
	callTool(t, ctx, cs, "gregale_apply_deploy", args, false)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writes != 1 || f.current != "release-external" {
		t.Fatalf("writes=%d current=%s", f.writes, f.current)
	}
}

func TestManagementWritesDisabledAndInvalidReleaseTargets(t *testing.T) {
	f, cs, ctx := newReleaseFixture(t, false)
	id := deployPlan(t, ctx, cs)
	if _, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "gregale_apply_deploy", Arguments: map[string]any{"plan_id": id}}); err == nil {
		t.Fatal("writes exposed by default")
	}
	for _, image := range []string{"image:latest", "/tmp/source", "https://user:password@example/image", "image@sha256:abc"} {
		callTool(t, ctx, cs, "gregale_plan_deploy", map[string]any{"app": "alpha", "image": image, "reason": "Release"}, true)
	}
	f.mu.Lock()
	f.deployments["foreign"] = api.DeploymentResponse{ID: "foreign", AppID: "other-app", Status: "superseded"}
	f.mu.Unlock()
	callTool(t, ctx, cs, "gregale_plan_rollback", map[string]any{"app": "alpha", "target_deployment_id": "foreign", "reason": "Restore"}, true)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writes != 0 {
		t.Fatal("read-only planning made a write")
	}
}

func TestManagementWaitReportsTerminalFailureAndPending(t *testing.T) {
	f, cs, ctx := newReleaseFixture(t, false)
	f.mu.Lock()
	f.deployments["release-wait"] = api.DeploymentResponse{ID: "release-wait", AppID: "app-one", Status: "building"}
	f.mu.Unlock()
	out := callTool(t, ctx, cs, "gregale_wait_deployment", map[string]any{"app": "alpha", "deployment_id": "release-wait", "wait_seconds": 2}, false)
	if string(out["terminal"]) != "true" || !strings.Contains(string(out["deployment"]), "failed") {
		t.Fatalf("wait=%s", out)
	}
	f.mu.Lock()
	f.deployments["release-pending"] = api.DeploymentResponse{ID: "release-pending", AppID: "app-one", Status: "building"}
	f.mu.Unlock()
	out = callTool(t, ctx, cs, "gregale_wait_deployment", map[string]any{"app": "alpha", "deployment_id": "release-pending", "wait_seconds": 1}, false)
	if string(out["pending"]) != "true" {
		t.Fatalf("pending=%s", out)
	}
	short, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	_, err := cs.CallTool(short, &mcp.CallToolParams{Name: "gregale_wait_deployment", Arguments: map[string]any{"app": "alpha", "deployment_id": "release-pending", "wait_seconds": 2}})
	if err == nil {
		t.Fatal("wait ignored cancellation")
	}
}

func TestManagementPlanExpirationAndCapacity(t *testing.T) {
	m := &management{opts: Options{AllowWrites: true}, plans: map[string]*releasePlan{"expired": {ID: "expired", Action: "deploy", ExpiresAt: time.Now().Add(-time.Second)}}}
	if _, err := m.checkedPlan(context.Background(), "expired", "deploy"); err == nil {
		t.Fatal("expired plan accepted")
	}
	if len(m.plans) != 0 {
		t.Fatal("expired plan was retained")
	}
	for range maxReleasePlans {
		if _, err := m.savePlan(&releasePlan{Action: "deploy", AccountID: "account-one", APIBase: "https://api.example", App: "alpha", AppID: "app-one", Scope: api.DefaultEnvScope, ExpiresAt: time.Now().Add(time.Minute)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.savePlan(&releasePlan{ExpiresAt: time.Now().Add(time.Minute)}); err == nil {
		t.Fatal("plan capacity was unbounded")
	}
	m.opts.AllowWrites = false
	if _, err := m.applyDeploy(context.Background(), applyArgs{PlanID: "unknown"}); err == nil {
		t.Fatal("handler itself did not enforce read-only mode")
	}
}

func TestManagementConcurrentApplyIsIdempotent(t *testing.T) {
	f, cs, ctx := newReleaseFixture(t, true)
	id := deployPlan(t, ctx, cs)
	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() {
			result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "gregale_apply_deploy", Arguments: map[string]any{"plan_id": id}})
			if err != nil || result.IsError {
				t.Errorf("concurrent apply=%+v error=%v", result, err)
			}
		})
	}
	wg.Wait()
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writes != 1 {
		t.Fatalf("concurrent writes=%d", f.writes)
	}
}

func TestManagementRejectsBackgroundReleasePlanning(t *testing.T) {
	for _, mode := range []string{api.ExecutionModeService, api.ExecutionModeWorker} {
		t.Run(mode, func(t *testing.T) {
			f, cs, ctx := newReleaseFixture(t, true)
			f.mu.Lock()
			f.app.Manifest.ExecutionMode = mode
			f.mu.Unlock()
			out := callTool(t, ctx, cs, "gregale_plan_deploy", map[string]any{"app": "alpha", "image": testImage, "reason": "Release"}, true)
			if string(out["code"]) != `"unsupported_release"` {
				t.Fatalf("background workflow=%s", out)
			}
		})
	}
}
