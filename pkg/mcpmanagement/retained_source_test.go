package mcpmanagement

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func retainedSource(t *testing.T, f *releaseFixture) api.DeploymentResponse {
	t.Helper()
	d := f.deployments["release-old"]
	d.Kind, d.CommitSHA = "github", strings.Repeat("a", 40)
	d.SourceURL, d.ImageDigest = "github://team/repository@"+d.CommitSHA, ""
	f.deployments[d.ID] = d
	return d
}

func TestRetainedSourceRollbackRebuildsAndSurvivesRestart(t *testing.T) {
	for _, mode := range []string{api.ExecutionModeService, api.ExecutionModeWorker} {
		t.Run(mode, func(t *testing.T) {
			f, _, _ := newReleaseFixture(t, true)
			f.app.Manifest.ExecutionMode = mode
			old := retainedSource(t, f)
			f.sourceHead = strings.Repeat("b", 40)
			backend := httptest.NewServer(http.HandlerFunc(f.serveHTTP))
			defer backend.Close()
			store, path, key := testStateStore(t)
			opts := Options{APIBase: backend.URL, Token: testToken, AllowWrites: true, planStore: store, planNamespace: "retained-source"}
			cs, ctx := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
			args := map[string]any{"app": "alpha", "retained_deployment_id": old.ID, "reason": "rebuild retained commit"}
			plan := callTool(t, ctx, cs, "gregale_plan_workload_rollout", args, false)
			if string(plan["target_deployment_id"]) != `"release-old"` || !strings.Contains(string(plan["traffic_effect"]), "mutable dependencies can differ") {
				t.Fatal("retained rebuild provenance or artifact semantics missing")
			}
			id := planID(t, plan)
			f.mu.Lock()
			f.ambiguous = true
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
			if json.Unmarshal(result["deployment"], &d) != nil || d.ID == old.ID || d.CommitSHA != old.CommitSHA || d.BuildID != "build-one" {
				t.Fatal("retained source did not rebuild exact commit as a new generation")
			}
			callTool(t, ctx, cs, "gregale_apply_workload_rollout", map[string]any{"plan_id": id}, false)
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.writes != 1 {
				t.Fatal("restart duplicated retained rebuild")
			}
		})
	}
}

func TestRetainedSourceRollbackRejectsAmbiguousProvenance(t *testing.T) {
	for _, failure := range []string{"url", "branch", "mismatch", "root", "scope", "app", "id", "upload", "failed"} {
		t.Run(failure, func(t *testing.T) {
			f, cs, ctx := newReleaseFixture(t, true)
			f.app.Manifest.ExecutionMode = api.ExecutionModeWorker
			d := retainedSource(t, f)
			switch failure {
			case "url":
				d.SourceURL = "https://user:secret@github.com/team/repository"
			case "branch":
				d.SourceURL = "github://team/repository@main"
			case "mismatch":
				d.CommitSHA = strings.Repeat("b", 40)
			case "root":
				d.SourceRoot = "other-root"
			case "scope":
				d.Scope = "neighbor"
			case "app":
				d.AppID = "foreign"
			case "id":
				d.ID = "different"
			case "upload":
				d.Kind = "tarball"
			case "failed":
				d.Status = "failed"
			}
			f.deployments["release-old"] = d
			callTool(t, ctx, cs, "gregale_plan_workload_rollout", map[string]any{"app": "alpha", "retained_deployment_id": "release-old", "reason": "reject unsafe rollback"}, true)
			if f.writes != 0 {
				t.Fatal("rejected retained source created a build")
			}
		})
	}
}

func TestNamedRetainedSourceRollbackPreservesScopeAndProtection(t *testing.T) {
	for _, mode := range []string{api.ExecutionModeService, api.ExecutionModeWorker} {
		t.Run(mode, func(t *testing.T) {
			f, _, opts := namedStageOptions(t)
			f.mode = mode
			commit := strings.Repeat("a", 40)
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/deployments/retained-source" {
					_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "retained-source", AppID: "app-one", Scope: "staging", Kind: "github", Status: "superseded", CommitSHA: commit, SourceURL: "github://team/repository@" + commit})
					return
				}
				if r.URL.Path == "/v1/apps/alpha/source-review" {
					var input api.SourceReviewRequest
					if json.NewDecoder(r.Body).Decode(&input) != nil || input.Environment != "staging" || input.Ref != commit {
						w.WriteHeader(400)
						return
					}
					f.mu.Lock()
					defer f.mu.Unlock()
					_ = json.NewEncoder(w).Encode(api.SourceReviewResponse{AppID: "app-one", Repo: input.Repo, CommitSHA: commit, InstallationID: 55, RepositoryID: 987, ReleaseReview: f.review()})
					return
				}
				if r.URL.Path == "/v1/apps/alpha/deployments/source-ref" {
					var input api.SourceRefDeployRequest
					if json.NewDecoder(r.Body).Decode(&input) != nil || input.Environment != "staging" || input.Ref != commit || input.NativeRollout != mode || input.TrafficPercent != nil || input.ExpectedSource == nil || !input.NoTriggers {
						w.WriteHeader(400)
						return
					}
					f.mu.Lock()
					defer f.mu.Unlock()
					key := r.Header.Get("Idempotency-Key")
					if key == "" {
						w.WriteHeader(400)
						return
					}
					if d, ok := f.cache[key]; ok {
						_ = json.NewEncoder(w).Encode(d)
						return
					}
					f.latest = "stage-new"
					review := f.review()
					d := api.DeploymentResponse{ID: "stage-new", AppID: "app-one", Scope: "staging", Kind: "github", Status: "building", CommitSHA: commit, BuildID: "stage-build", StagedReview: &review}
					if mode == api.ExecutionModeWorker {
						d.TrafficPercent = 100
					} else {
						d.RolloutState = "rolling_out"
					}
					f.writes++
					f.cache[key] = d
					_ = json.NewEncoder(w).Encode(d)
					return
				}
				f.serve(w, r)
			}))
			defer backend.Close()
			opts.APIBase = backend.URL
			cs, ctx := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
			args := map[string]any{"app": "alpha", "project": "shop", "environment": "staging", "retained_deployment_id": "retained-source", "reason": "rebuild staged source"}
			plan := callTool(t, ctx, cs, "gregale_plan_workload_rollout", args, false)
			if string(plan["environment_id"]) != `"env-staging"` {
				t.Fatal("retained plan lost immutable environment")
			}
			callTool(t, ctx, cs, "gregale_apply_workload_rollout", map[string]any{"plan_id": planID(t, plan)}, false)
			callTool(t, ctx, cs, "gregale_apply_workload_rollout", map[string]any{"plan_id": planID(t, plan)}, false)
			f.mu.Lock()
			f.protected = true
			f.mu.Unlock()
			callTool(t, ctx, cs, "gregale_apply_workload_rollout", map[string]any{"plan_id": planID(t, plan)}, true)
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.writes != 1 {
				t.Fatal("replay or protection change created another build")
			}
		})
	}
}
