package mcpmanagement

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func newEnvironmentPurgeFixture(t *testing.T) (*environmentCacheFixture, *httptest.Server, Options) {
	t.Helper()
	f, base, opts := newEnvironmentCacheFixture(t)
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		prefix := "/v1/apps/alpha/cache/purges"
		if r.URL.Path != prefix && !strings.HasPrefix(r.URL.Path, prefix+"/") {
			base.Config.Handler.ServeHTTP(w, r)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		scope := r.URL.Query().Get("environment")
		if r.Method == http.MethodGet {
			out, err := f.store.GetEnvironmentCachePurge(r.Context(), f.account.ID, f.app.ID, scope, strings.TrimPrefix(r.URL.Path, prefix+"/"))
			if err != nil {
				w.WriteHeader(404)
				return
			}
			_ = json.NewEncoder(w).Encode(out)
			return
		}
		var req api.CreateEnvironmentCachePurgeRequest
		if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&req) != nil || r.Header.Get("Idempotency-Key") != req.RequestID {
			w.WriteHeader(400)
			return
		}
		out, err := f.store.CreateEnvironmentCachePurge(r.Context(), f.account.ID, f.app.ID, scope, req)
		if err != nil {
			api.WriteProblem(w, api.NewProblem(409, api.CodeConflict, "Changed", "identity or purge request changed"))
			return
		}
		f.writes++
		if f.ambiguous {
			f.ambiguous = false
			w.WriteHeader(504)
			return
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(b.Close)
	opts.APIBase = b.URL
	return f, b, opts
}
func environmentPurgeArgs(path string) map[string]any {
	return map[string]any{"app": "alpha", "project": "shop", "environment": "staging", "path": path, "reason": "Invalidate cached catalogue data"}
}
func TestEnvironmentCachePurgeMCPReviewApplyAndStatus(t *testing.T) {
	for _, path := range []string{"", "/catalog/item"} {
		t.Run(path, func(t *testing.T) {
			f, b, opts := newEnvironmentPurgeFixture(t)
			cs, ctx := connectConfiguredTestServer(t, b, opts, "2026-07-28")
			review := callTool(t, ctx, cs, "gregale_plan_environment_cache_purge", environmentPurgeArgs(path), false)
			var plan environmentPurgePlan
			if json.Unmarshal(review["environment_cache_purge"], &plan) != nil || plan.Proposed.Path != path || plan.Proposed.EnvironmentID != f.env.ID {
				t.Fatal("purge review target missing")
			}
			if f.writes != 0 {
				t.Fatal("purge planning mutated state")
			}
			id := planID(t, review)
			callTool(t, ctx, cs, "gregale_apply_environment_cache_policy", map[string]any{"plan_id": id}, true)
			receipt := callTool(t, ctx, cs, "gregale_apply_environment_cache_purge", map[string]any{"plan_id": id}, false)
			if string(receipt["generation"]) != "1" {
				t.Fatal("missing purge generation")
			}
			callTool(t, ctx, cs, "gregale_apply_environment_cache_purge", map[string]any{"plan_id": id}, false)
			if f.writes != 1 {
				t.Fatal("purge retry created another command")
			}
			args := map[string]any{"app": "alpha", "project": "shop", "environment": "staging", "request_id": plan.Proposed.RequestID}
			out := callTool(t, ctx, cs, "gregale_get_environment_cache_purge", args, false)
			if string(out["status"]) != `"degraded"` {
				t.Fatal("zero fleet counted as completed")
			}
			role, target := "compute-node", "tcp://127.0.0.1:8080"
			node, err := f.store.CreateComputeNode(ctx, state.ComputeNode{Name: "purge-observer", TargetURL: "tcp://127.0.0.1:50051", GatewayTargetURL: &target, Active: true, Role: &role, VPCPUs: 1, MemMB: 1024, MaxConcurrency: 10, AdmissionCeilingMB: 512, VCPUBudget: 1})
			if err != nil {
				t.Fatal(err)
			}
			session := uuid.NewString()
			if err = f.store.BeginEnvironmentCacheGatewaySession(ctx, node.Name, session, time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			out = callTool(t, ctx, cs, "gregale_get_environment_cache_purge", args, false)
			if string(out["status"]) != `"pending"` {
				t.Fatal("missing ack counted completed")
			}
			if err = f.store.RecordEnvironmentCachePurgeWatermark(ctx, node.Name, session, 1); err != nil {
				t.Fatal(err)
			}
			out = callTool(t, ctx, cs, "gregale_get_environment_cache_purge", args, false)
			if string(out["status"]) != `"completed"` {
				t.Fatal("current-session purge not completed")
			}
			args["environment"] = "production"
			callTool(t, ctx, cs, "gregale_get_environment_cache_purge", args, true)
			policy, err := f.store.GetEnvironmentCachePolicy(ctx, f.account.ID, f.app.ID, "staging")
			if err != nil || policy.Revision != 0 || !policy.Inherited {
				t.Fatal("purge altered policy")
			}
		})
	}
}
func TestEnvironmentCachePurgeMCPConsentAndInvalidPaths(t *testing.T) {
	f, b, opts := newEnvironmentPurgeFixture(t)
	grant := opts.Environments.Named["shop/staging"]
	grant.Write = false
	opts.Environments.Named["shop/staging"] = grant
	cs, ctx := connectConfiguredTestServer(t, b, opts, "2026-07-28")
	id := planID(t, callTool(t, ctx, cs, "gregale_plan_environment_cache_purge", environmentPurgeArgs("/item"), false))
	callTool(t, ctx, cs, "gregale_apply_environment_cache_purge", map[string]any{"plan_id": id}, true)
	for _, path := range []string{"*", "/item?x=1", "/a/../b", "/a%2fb", "/a*", "/a\n"} {
		callTool(t, ctx, cs, "gregale_plan_environment_cache_purge", environmentPurgeArgs(path), true)
	}
	if f.writes != 0 {
		t.Fatal("unauthorized purge mutated state")
	}
}
func TestEnvironmentCachePurgeMCPRestartRecovery(t *testing.T) {
	f, b, opts := newEnvironmentPurgeFixture(t)
	store, path, key := testStateStore(t)
	opts.planStore, opts.planNamespace = store, "environment-purge-grant"
	cs, ctx := connectConfiguredTestServer(t, b, opts, "2026-07-28")
	id := planID(t, callTool(t, ctx, cs, "gregale_plan_environment_cache_purge", environmentPurgeArgs("/item"), false))
	f.mu.Lock()
	f.ambiguous = true
	f.mu.Unlock()
	callTool(t, ctx, cs, "gregale_apply_environment_cache_purge", map[string]any{"plan_id": id}, true)
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
	out := callTool(t, ctx, cs, "gregale_apply_environment_cache_purge", map[string]any{"plan_id": id}, false)
	if string(out["generation"]) != "1" {
		t.Fatal("restart did not recover original purge")
	}
	rows, err := f.store.ListEnvironmentCachePurgesAfter(ctx, 0)
	if err != nil || len(rows) != 1 {
		t.Fatal("restart duplicated purge")
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
	grant := opts.Environments.Named["shop/staging"]
	grant.Write = false
	opts.Environments.Named["shop/staging"] = grant
	cs, ctx = connectConfiguredTestServer(t, b, opts, "2026-07-28")
	callTool(t, ctx, cs, "gregale_apply_environment_cache_purge", map[string]any{"plan_id": id}, true)
}
