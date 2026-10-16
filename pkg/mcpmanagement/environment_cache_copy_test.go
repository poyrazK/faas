package mcpmanagement

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func cacheCopyArgs() map[string]any {
	return map[string]any{"app": "alpha", "project": "shop", "source_environment": "staging", "environment": "production", "reason": "Promote the reviewed staging cache rules"}
}
func newCacheCopyFixture(t *testing.T) (*environmentCacheFixture, *httptest.Server, Options) {
	t.Helper()
	f, _, opts := newEnvironmentCacheFixture(t)
	source, err := f.store.GetEnvironmentCachePolicy(t.Context(), f.account.ID, f.app.ID, "staging")
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.store.SetEnvironmentCachePolicy(t.Context(), f.account.ID, f.app.ID, "staging", api.SetEnvironmentCachePolicyRequest{AppID: f.app.ID, ProjectID: f.project.ID, EnvironmentID: f.env.ID, RequestID: uuid.NewString(), ExpectedRevision: &source.Revision, Rules: []api.EnvironmentCacheRule{{Path: "/catalog/*", Priority: 1, Cache: api.EdgeRuleCacheAction{MaxAgeSeconds: 120}}}})
	if err != nil {
		t.Fatal(err)
	}
	dest, err := f.store.GetEnvironmentCachePolicy(t.Context(), f.account.ID, f.app.ID, "production")
	if err != nil {
		t.Fatal(err)
	}
	grant := opts.Environments.Named["shop/staging"]
	grant.Write = false
	opts.Environments = &EnvironmentConsent{Named: map[string]EnvironmentGrant{"shop/staging": grant, "shop/production": {Project: "shop", ProjectID: f.project.ID, Environment: "production", EnvironmentID: dest.EnvironmentID, Apps: map[string]string{"alpha": f.app.ID}, Write: true}}}
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/projects/shop/environments/production" {
			out, err := f.store.GetEnvironmentCachePolicy(r.Context(), f.account.ID, f.app.ID, "production")
			if err != nil {
				w.WriteHeader(404)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(api.ProjectEnvironmentResponse{ID: out.EnvironmentID, ProjectID: out.ProjectID, Slug: "production"})
			return
		}
		if r.URL.Path == "/v1/apps/alpha/cache-policy/convergence" {
			out, err := f.store.GetEnvironmentCacheConvergence(r.Context(), f.account.ID, f.app.ID, r.URL.Query().Get("environment"))
			if err != nil {
				w.WriteHeader(404)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(out)
			return
		}
		f.serveCache(w, r)
	}))
	t.Cleanup(b.Close)
	opts.APIBase = b.URL
	return f, b, opts
}
func changeCopyFixturePolicy(t *testing.T, f *environmentCacheFixture, scope string, inherited bool) api.EnvironmentCachePolicy {
	t.Helper()
	p, err := f.store.GetEnvironmentCachePolicy(t.Context(), f.account.ID, f.app.ID, scope)
	if err != nil {
		t.Fatal(err)
	}
	out, err := f.store.SetEnvironmentCachePolicy(t.Context(), f.account.ID, f.app.ID, scope, api.SetEnvironmentCachePolicyRequest{AppID: f.app.ID, ProjectID: f.project.ID, EnvironmentID: p.EnvironmentID, RequestID: uuid.NewString(), ExpectedRevision: &p.Revision, Inherited: inherited, Rules: []api.EnvironmentCacheRule{}})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func TestEnvironmentCacheCopyMCPReviewApplyAndWatch(t *testing.T) {
	for _, empty := range []bool{false, true} {
		t.Run(map[bool]string{false: "rules", true: "disabled"}[empty], func(t *testing.T) {
			f, b, opts := newCacheCopyFixture(t)
			if empty {
				changeCopyFixturePolicy(t, f, "staging", false)
			}
			cs, ctx := connectConfiguredTestServer(t, b, opts, "2026-07-28")
			review := callTool(t, ctx, cs, "gregale_plan_environment_cache_copy", cacheCopyArgs(), false)
			var detail environmentCachePlan
			if json.Unmarshal(review["environment_cache_policy"], &detail) != nil || detail.Source == nil || detail.Source.Scope != "staging" || detail.Before.Scope != "production" || detail.Proposed.CopyFrom == nil || detail.Proposed.CopyFrom.EnvironmentID != f.env.ID {
				t.Fatalf("copy review=%s", review)
			}
			if f.writes != 0 {
				t.Fatal("copy planning wrote intent")
			}
			plan := planID(t, review)
			callTool(t, ctx, cs, "gregale_apply_environment_cache_policy", map[string]any{"plan_id": plan}, true)
			out := callTool(t, ctx, cs, "gregale_apply_environment_cache_copy", map[string]any{"plan_id": plan}, false)
			if string(out["scope"]) != `"production"` || string(out["revision"]) != "1" || string(out["inherited"]) != "false" {
				t.Fatalf("copy result=%s", out)
			}
			callTool(t, ctx, cs, "gregale_apply_environment_cache_copy", map[string]any{"plan_id": plan}, false)
			if f.writes != 1 {
				t.Fatal("completed copy applied twice")
			}
			source, err := f.store.GetEnvironmentCachePolicy(ctx, f.account.ID, f.app.ID, "staging")
			if err != nil || source.Revision != detail.Source.Revision {
				t.Fatal("copy changed source")
			}
			role, target := "compute-node", "tcp://127.0.0.1:8080"
			node, err := f.store.CreateComputeNode(ctx, state.ComputeNode{Name: "copy-observer", TargetURL: "tcp://127.0.0.1:50051", GatewayTargetURL: &target, Active: true, Role: &role, VPCPUs: 1, MemMB: 1024, MaxConcurrency: 10, AdmissionCeilingMB: 512, VCPUBudget: 1})
			if err != nil {
				t.Fatal(err)
			}
			session := uuid.NewString()
			if err = f.store.BeginEnvironmentCacheGatewaySession(ctx, node.Name, session, time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			if err = f.store.RecordEnvironmentCacheGatewayObserved(ctx, node.Name, session, f.app.ID, detail.Before.EnvironmentID, 1); err != nil {
				t.Fatal(err)
			}
			watch := callTool(t, ctx, cs, "gregale_watch_environment_cache_policy", map[string]any{"app": "alpha", "project": "shop", "environment": "production", "revision": 1, "wait_seconds": 1}, false)
			if string(watch["status"]) != `"converged"` {
				t.Fatalf("copy convergence=%s", watch)
			}
		})
	}
}
func TestEnvironmentCacheCopyMCPGuards(t *testing.T) {
	for _, mode := range []string{"source_stale", "destination_stale", "inherited", "no_source_consent", "no_destination_write", "source_replaced", "same_environment"} {
		t.Run(mode, func(t *testing.T) {
			f, b, opts := newCacheCopyFixture(t)
			if mode == "no_source_consent" {
				delete(opts.Environments.Named, "shop/staging")
			}
			if mode == "no_destination_write" {
				g := opts.Environments.Named["shop/production"]
				g.Write = false
				opts.Environments.Named["shop/production"] = g
			}
			if mode == "inherited" {
				changeCopyFixturePolicy(t, f, "staging", true)
			}
			cs, ctx := connectConfiguredTestServer(t, b, opts, "2026-07-28")
			args := cacheCopyArgs()
			if mode == "same_environment" {
				args["environment"] = "staging"
			}
			planError := mode == "inherited" || mode == "no_source_consent" || mode == "same_environment"
			result := callTool(t, ctx, cs, "gregale_plan_environment_cache_copy", args, planError)
			if planError {
				return
			}
			switch mode {
			case "source_stale":
				changeCopyFixturePolicy(t, f, "staging", false)
			case "destination_stale":
				changeCopyFixturePolicy(t, f, "production", false)
			case "source_replaced":
				f.mu.Lock()
				f.env.ID = uuid.NewString()
				f.mu.Unlock()
			}
			callTool(t, ctx, cs, "gregale_apply_environment_cache_copy", map[string]any{"plan_id": planID(t, result)}, true)
			if f.writes != 0 {
				t.Fatal("rejected copy reached native write")
			}
		})
	}
}
func TestEnvironmentCacheCopyMCPRestartRecoveryAndSourceConsent(t *testing.T) {
	f, b, opts := newCacheCopyFixture(t)
	store, path, key := testStateStore(t)
	opts.planStore, opts.planNamespace = store, "cache-copy-grant"
	cs, ctx := connectConfiguredTestServer(t, b, opts, "2026-07-28")
	plan := planID(t, callTool(t, ctx, cs, "gregale_plan_environment_cache_copy", cacheCopyArgs(), false))
	f.mu.Lock()
	f.ambiguous = true
	f.mu.Unlock()
	callTool(t, ctx, cs, "gregale_apply_environment_cache_copy", map[string]any{"plan_id": plan}, true)
	changeCopyFixturePolicy(t, f, "staging", true)
	changeCopyFixturePolicy(t, f, "production", false)
	_ = cs.Close()
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := OpenStateStore(path, key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = next.Close() })
	opts.planStore = next
	cs, ctx = connectConfiguredTestServer(t, b, opts, "2026-07-28")
	out := callTool(t, ctx, cs, "gregale_apply_environment_cache_copy", map[string]any{"plan_id": plan}, false)
	if string(out["revision"]) != "1" || string(out["inherited"]) != "false" {
		t.Fatalf("recovered copy=%s", out)
	}
	current, err := f.store.GetEnvironmentCachePolicy(ctx, f.account.ID, f.app.ID, "production")
	if err != nil || current.Revision != 2 || len(current.Rules) != 0 {
		t.Fatal("copy retry overwrote later edit")
	}
	_ = cs.Close()
	if err = next.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := OpenStateStore(path, key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = again.Close() })
	opts.planStore = again
	delete(opts.Environments.Named, "shop/staging")
	cs, ctx = connectConfiguredTestServer(t, b, opts, "2026-07-28")
	callTool(t, ctx, cs, "gregale_apply_environment_cache_copy", map[string]any{"plan_id": plan}, true)
	if f.writes != 2 {
		t.Fatal("withdrawn source consent reached native receipt")
	}
}
