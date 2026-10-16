package mcpmanagement

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func newCacheHistoryFixture(t *testing.T) (*environmentCacheFixture, *httptest.Server, Options) {
	t.Helper()
	f, base, opts := newCacheCopyFixture(t)
	grant := opts.Environments.Named["shop/staging"]
	grant.Write = true
	opts.Environments.Named["shop/staging"] = grant
	changeCopyFixturePolicy(t, f, "staging", false)
	changeCopyFixturePolicy(t, f, "staging", true)
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		prefix := "/v1/apps/alpha/cache-policy/history"
		if r.URL.Path == prefix {
			before, _ := strconv.ParseInt(r.URL.Query().Get("before_revision"), 10, 64)
			limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
			out, err := f.store.ListEnvironmentCachePolicyHistory(r.Context(), f.account.ID, f.app.ID, r.URL.Query().Get("environment"), before, limit)
			if err != nil {
				w.WriteHeader(404)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(out)
			return
		}
		if strings.HasPrefix(r.URL.Path, prefix+"/") {
			revision, _ := strconv.ParseInt(strings.TrimPrefix(r.URL.Path, prefix+"/"), 10, 64)
			out, err := f.store.GetEnvironmentCachePolicyRevision(r.Context(), f.account.ID, f.app.ID, r.URL.Query().Get("environment"), revision)
			if err != nil {
				w.WriteHeader(404)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(out)
			return
		}
		base.Config.Handler.ServeHTTP(w, r)
	}))
	t.Cleanup(b.Close)
	opts.APIBase = b.URL
	return f, b, opts
}
func cacheRollbackArgs(revision int64) map[string]any {
	return map[string]any{"app": "alpha", "project": "shop", "environment": "staging", "revision": revision, "reason": "Restore the reviewed earlier cache policy"}
}
func TestEnvironmentCacheRollbackModesAndHistoryMCP(t *testing.T) {
	for _, revision := range []int64{1, 2, 3, 0} {
		t.Run(strconv.FormatInt(revision, 10), func(t *testing.T) {
			f, b, opts := newCacheHistoryFixture(t)
			cs, ctx := connectConfiguredTestServer(t, b, opts, "2026-07-28")
			args := map[string]any{"app": "alpha", "project": "shop", "environment": "staging", "limit": 2}
			first := callTool(t, ctx, cs, "gregale_list_environment_cache_history", args, false)
			if string(first["current_revision"]) != "3" || string(first["next_before_revision"]) != "2" {
				t.Fatalf("history=%s", first)
			}
			args["before_revision"] = 2
			tail := callTool(t, ctx, cs, "gregale_list_environment_cache_history", args, false)
			var entries []api.EnvironmentCachePolicy
			if json.Unmarshal(tail["entries"], &entries) != nil || len(entries) != 2 || entries[0].Revision != 1 || entries[1].Revision != 0 {
				t.Fatal("history pagination skipped revisions")
			}
			historical := callTool(t, ctx, cs, "gregale_get_environment_cache_revision", map[string]any{"app": "alpha", "project": "shop", "environment": "staging", "revision": revision}, false)
			review := callTool(t, ctx, cs, "gregale_plan_environment_cache_rollback", cacheRollbackArgs(revision), false)
			var details environmentCachePlan
			if json.Unmarshal(review["environment_cache_policy"], &details) != nil || details.Restore == nil || details.Restore.Revision != revision || details.Before.Revision != 3 || details.Proposed.RestoreRevision == nil {
				t.Fatal("rollback review lacks exact historical binding")
			}
			if f.writes != 0 {
				t.Fatal("history inspection or planning wrote policy")
			}
			plan := planID(t, review)
			callTool(t, ctx, cs, "gregale_apply_environment_cache_policy", map[string]any{"plan_id": plan}, true)
			out := callTool(t, ctx, cs, "gregale_apply_environment_cache_rollback", map[string]any{"plan_id": plan}, false)
			if string(out["revision"]) != "4" || string(out["inherited"]) != string(historical["inherited"]) || string(out["rules"]) != string(historical["rules"]) {
				t.Fatalf("rollback=%s", out)
			}
			callTool(t, ctx, cs, "gregale_apply_environment_cache_rollback", map[string]any{"plan_id": plan}, false)
			if f.writes != 1 {
				t.Fatal("rollback duplicated write")
			}
			role, target := "compute-node", "tcp://127.0.0.1:8080"
			node, err := f.store.CreateComputeNode(ctx, state.ComputeNode{Name: "history-observer", TargetURL: "tcp://127.0.0.1:50051", GatewayTargetURL: &target, Active: true, Role: &role, VPCPUs: 1, MemMB: 1024, MaxConcurrency: 10, AdmissionCeilingMB: 512, VCPUBudget: 1})
			if err != nil {
				t.Fatal(err)
			}
			session := uuid.NewString()
			if err = f.store.BeginEnvironmentCacheGatewaySession(ctx, node.Name, session, time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			if err = f.store.RecordEnvironmentCacheGatewayObserved(ctx, node.Name, session, f.app.ID, f.env.ID, 4); err != nil {
				t.Fatal(err)
			}
			watch := callTool(t, ctx, cs, "gregale_watch_environment_cache_policy", map[string]any{"app": "alpha", "project": "shop", "environment": "staging", "revision": 4, "wait_seconds": 1}, false)
			if string(watch["status"]) != `"converged"` {
				t.Fatal("rollback revision cannot converge")
			}
		})
	}
}
func TestEnvironmentCacheRollbackGuardsMCP(t *testing.T) {
	for _, mode := range []string{"stale", "read_only", "replaced", "missing_revision"} {
		t.Run(mode, func(t *testing.T) {
			f, b, opts := newCacheHistoryFixture(t)
			if mode == "read_only" {
				grant := opts.Environments.Named["shop/staging"]
				grant.Write = false
				opts.Environments.Named["shop/staging"] = grant
			}
			cs, ctx := connectConfiguredTestServer(t, b, opts, "2026-07-28")
			args := cacheRollbackArgs(1)
			if mode == "missing_revision" {
				args["revision"] = 99
				callTool(t, ctx, cs, "gregale_plan_environment_cache_rollback", args, true)
				return
			}
			plan := planID(t, callTool(t, ctx, cs, "gregale_plan_environment_cache_rollback", args, false))
			if mode == "stale" {
				changeCopyFixturePolicy(t, f, "staging", false)
			}
			if mode == "replaced" {
				f.mu.Lock()
				f.env.ID = uuid.NewString()
				f.mu.Unlock()
			}
			callTool(t, ctx, cs, "gregale_apply_environment_cache_rollback", map[string]any{"plan_id": plan}, true)
			if f.writes != 0 {
				t.Fatal("rejected rollback wrote")
			}
		})
	}
}
func TestEnvironmentCacheRollbackRestartRecoveryMCP(t *testing.T) {
	f, b, opts := newCacheHistoryFixture(t)
	store, path, key := testStateStore(t)
	opts.planStore, opts.planNamespace = store, "cache-rollback-grant"
	cs, ctx := connectConfiguredTestServer(t, b, opts, "2026-07-28")
	plan := planID(t, callTool(t, ctx, cs, "gregale_plan_environment_cache_rollback", cacheRollbackArgs(1), false))
	f.mu.Lock()
	f.ambiguous = true
	f.mu.Unlock()
	callTool(t, ctx, cs, "gregale_apply_environment_cache_rollback", map[string]any{"plan_id": plan}, true)
	changeCopyFixturePolicy(t, f, "staging", true)
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
	out := callTool(t, ctx, cs, "gregale_apply_environment_cache_rollback", map[string]any{"plan_id": plan}, false)
	if string(out["revision"]) != "4" || string(out["inherited"]) != "false" {
		t.Fatalf("recovered=%s", out)
	}
	current, err := f.store.GetEnvironmentCachePolicy(ctx, f.account.ID, f.app.ID, "staging")
	if err != nil || current.Revision != 5 || !current.Inherited {
		t.Fatal("rollback retry overwrote later intent")
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
	callTool(t, ctx, cs, "gregale_apply_environment_cache_rollback", map[string]any{"plan_id": plan}, true)
}
