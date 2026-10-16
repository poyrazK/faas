package mcpmanagement

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type environmentCacheFixture struct{ *budgetFixture }

func newEnvironmentCacheFixture(t *testing.T) (*environmentCacheFixture, *httptest.Server, Options) {
	base, _, opts := newBudgetFixture(t)
	f := &environmentCacheFixture{budgetFixture: base}
	backend := httptest.NewServer(http.HandlerFunc(f.serveCache))
	t.Cleanup(backend.Close)
	opts.APIBase = backend.URL
	return f, backend, opts
}
func (f *environmentCacheFixture) serveCache(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/v1/apps/alpha/cache-policy" {
		f.budgetFixture.serve(w, r)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	scope := r.URL.Query().Get("environment")
	if r.Method == http.MethodGet {
		out, err := f.store.GetEnvironmentCachePolicy(r.Context(), f.account.ID, f.app.ID, scope)
		if err != nil {
			w.WriteHeader(404)
			return
		}
		_ = json.NewEncoder(w).Encode(out)
		return
	}
	var req api.SetEnvironmentCachePolicyRequest
	if r.Method != http.MethodPut || json.NewDecoder(r.Body).Decode(&req) != nil || r.Header.Get("Idempotency-Key") != req.RequestID {
		w.WriteHeader(400)
		return
	}
	out, err := f.store.SetEnvironmentCachePolicy(r.Context(), f.account.ID, f.app.ID, scope, req)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(409, api.CodeConflict, "Changed", "policy revision or identity changed"))
		return
	}
	f.writes++
	if f.ambiguous {
		f.ambiguous = false
		w.WriteHeader(504)
		return
	}
	_ = json.NewEncoder(w).Encode(out)
}
func environmentCacheArgs() map[string]any {
	return map[string]any{"app": "alpha", "project": "shop", "environment": "staging", "reason": "Cache staging catalogue", "rules": []api.EnvironmentCacheRule{{Path: "/catalog/*", Priority: 1, Cache: api.EdgeRuleCacheAction{MaxAgeSeconds: 120}}}}
}
func TestEnvironmentCachePolicyMCPConsentAndStale(t *testing.T) {
	f, b, opts := newEnvironmentCacheFixture(t)
	cs, ctx := connectConfiguredTestServer(t, b, opts, "2026-07-28")
	before := callTool(t, ctx, cs, "gregale_get_environment_cache_policy", map[string]any{"app": "alpha", "project": "shop", "environment": "staging"}, false)
	if string(before["inherited"]) != "true" {
		t.Fatal("missing inherited baseline")
	}
	first := planID(t, callTool(t, ctx, cs, "gregale_plan_environment_cache_policy", environmentCacheArgs(), false))
	stale := planID(t, callTool(t, ctx, cs, "gregale_plan_environment_cache_policy", environmentCacheArgs(), false))
	if f.writes != 0 {
		t.Fatal("planning wrote policy")
	}
	out := callTool(t, ctx, cs, "gregale_apply_environment_cache_policy", map[string]any{"plan_id": first}, false)
	if string(out["revision"]) != "1" || string(out["environment_id"]) != `"`+f.env.ID+`"` {
		t.Fatal("saved policy identity differs")
	}
	callTool(t, ctx, cs, "gregale_apply_environment_cache_policy", map[string]any{"plan_id": first}, false)
	callTool(t, ctx, cs, "gregale_apply_environment_cache_policy", map[string]any{"plan_id": stale}, true)
	if f.writes != 1 {
		t.Fatal("cached or stale plan mutated again")
	}
	neighbor, err := f.store.GetEnvironmentCachePolicy(ctx, f.account.ID, f.app.ID, "production")
	if err != nil || !neighbor.Inherited {
		t.Fatal("staging changed production")
	}
	readonly := opts
	grant := readonly.Environments.Named["shop/staging"]
	grant.Write = false
	readonly.Environments = &EnvironmentConsent{Named: map[string]EnvironmentGrant{"shop/staging": grant}}
	readClient, readCtx := connectConfiguredTestServer(t, b, readonly, "2026-07-28")
	readPlan := planID(t, callTool(t, readCtx, readClient, "gregale_plan_environment_cache_policy", environmentCacheArgs(), false))
	callTool(t, readCtx, readClient, "gregale_apply_environment_cache_policy", map[string]any{"plan_id": readPlan}, true)
	args := environmentCacheArgs()
	args["environment"] = "production"
	callTool(t, ctx, cs, "gregale_plan_environment_cache_policy", args, true)
	f.mu.Lock()
	f.env.ID = uuid.NewString()
	f.mu.Unlock()
	callTool(t, ctx, cs, "gregale_apply_environment_cache_policy", map[string]any{"plan_id": first}, true)
}
func TestEnvironmentCachePolicyMCPRestartRecovery(t *testing.T) {
	f, b, opts := newEnvironmentCacheFixture(t)
	store, path, key := testStateStore(t)
	opts.planStore, opts.planNamespace = store, "environment-cache-grant"
	cs, ctx := connectConfiguredTestServer(t, b, opts, "2026-07-28")
	plan := planID(t, callTool(t, ctx, cs, "gregale_plan_environment_cache_policy", environmentCacheArgs(), false))
	f.mu.Lock()
	f.ambiguous = true
	f.mu.Unlock()
	callTool(t, ctx, cs, "gregale_apply_environment_cache_policy", map[string]any{"plan_id": plan}, true)
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
	out := callTool(t, ctx, cs, "gregale_apply_environment_cache_policy", map[string]any{"plan_id": plan}, false)
	if string(out["revision"]) != "1" {
		t.Fatal("retry did not recover original receipt")
	}
	current, err := f.store.GetEnvironmentCachePolicy(ctx, f.account.ID, f.app.ID, "staging")
	if err != nil || current.Revision != 1 {
		t.Fatal("retry created another revision")
	}
}

func environmentCacheInheritanceArgsMap() map[string]any {
	return map[string]any{"app": "alpha", "project": "shop", "environment": "staging", "reason": "Restore live app cache inheritance"}
}
func TestEnvironmentCacheInheritanceMCP(t *testing.T) {
	f, b, opts := newEnvironmentCacheFixture(t)
	cs, ctx := connectConfiguredTestServer(t, b, opts, "2026-07-28")
	override := planID(t, callTool(t, ctx, cs, "gregale_plan_environment_cache_policy", environmentCacheArgs(), false))
	callTool(t, ctx, cs, "gregale_apply_environment_cache_policy", map[string]any{"plan_id": override}, false)
	restore := planID(t, callTool(t, ctx, cs, "gregale_plan_environment_cache_inheritance", environmentCacheInheritanceArgsMap(), false))
	stale := planID(t, callTool(t, ctx, cs, "gregale_plan_environment_cache_policy", environmentCacheArgs(), false))
	if f.writes != 1 {
		t.Fatal("restoration planning mutated intent")
	}
	callTool(t, ctx, cs, "gregale_apply_environment_cache_policy", map[string]any{"plan_id": restore}, true)
	out := callTool(t, ctx, cs, "gregale_apply_environment_cache_inheritance", map[string]any{"plan_id": restore}, false)
	if string(out["inherited"]) != "true" || string(out["revision"]) != "2" || string(out["rules"]) != "[]" {
		t.Fatalf("restored=%s", out)
	}
	callTool(t, ctx, cs, "gregale_apply_environment_cache_inheritance", map[string]any{"plan_id": restore}, false)
	callTool(t, ctx, cs, "gregale_apply_environment_cache_policy", map[string]any{"plan_id": stale}, true)
	if f.writes != 2 {
		t.Fatal("retry or stale plan changed intent")
	}
	neighbor, err := f.store.GetEnvironmentCachePolicy(ctx, f.account.ID, f.app.ID, "production")
	if err != nil || !neighbor.Inherited || neighbor.Revision != 0 {
		t.Fatal("restoration changed neighboring environment")
	}
	// A fresh override can replace the inherited revision, without resetting its counter.
	next := planID(t, callTool(t, ctx, cs, "gregale_plan_environment_cache_policy", environmentCacheArgs(), false))
	out = callTool(t, ctx, cs, "gregale_apply_environment_cache_policy", map[string]any{"plan_id": next}, false)
	if string(out["inherited"]) != "false" || string(out["revision"]) != "3" {
		t.Fatal("override after restoration failed")
	}
	readonly := opts
	grant := opts.Environments.Named["shop/staging"]
	grant.Write = false
	readonly.Environments = &EnvironmentConsent{Named: map[string]EnvironmentGrant{"shop/staging": grant}}
	rc, rctx := connectConfiguredTestServer(t, b, readonly, "2026-07-28")
	rp := planID(t, callTool(t, rctx, rc, "gregale_plan_environment_cache_inheritance", environmentCacheInheritanceArgsMap(), false))
	callTool(t, rctx, rc, "gregale_apply_environment_cache_inheritance", map[string]any{"plan_id": rp}, true)
	args := environmentCacheInheritanceArgsMap()
	args["environment"] = "production"
	callTool(t, ctx, cs, "gregale_plan_environment_cache_inheritance", args, true)
	f.mu.Lock()
	f.env.ID = uuid.NewString()
	f.mu.Unlock()
	callTool(t, ctx, cs, "gregale_apply_environment_cache_inheritance", map[string]any{"plan_id": restore}, true)
}
func TestEnvironmentCacheInheritanceMCPRestartRecovery(t *testing.T) {
	f, b, opts := newEnvironmentCacheFixture(t)
	store, path, key := testStateStore(t)
	opts.planStore, opts.planNamespace = store, "cache-inheritance-grant"
	cs, ctx := connectConfiguredTestServer(t, b, opts, "2026-07-28")
	override := planID(t, callTool(t, ctx, cs, "gregale_plan_environment_cache_policy", environmentCacheArgs(), false))
	callTool(t, ctx, cs, "gregale_apply_environment_cache_policy", map[string]any{"plan_id": override}, false)
	restore := planID(t, callTool(t, ctx, cs, "gregale_plan_environment_cache_inheritance", environmentCacheInheritanceArgsMap(), false))
	f.mu.Lock()
	f.ambiguous = true
	f.mu.Unlock()
	callTool(t, ctx, cs, "gregale_apply_environment_cache_inheritance", map[string]any{"plan_id": restore}, true)
	// A later write must not change the original restoration receipt after a lost reply.
	current, err := f.store.GetEnvironmentCachePolicy(ctx, f.account.ID, f.app.ID, "staging")
	if err != nil || !current.Inherited || current.Revision != 2 {
		t.Fatalf("committed restore=%+v %v", current, err)
	}
	_, err = f.store.SetEnvironmentCachePolicy(ctx, f.account.ID, f.app.ID, "staging", api.SetEnvironmentCachePolicyRequest{AppID: f.app.ID, ProjectID: f.project.ID, EnvironmentID: f.env.ID, RequestID: uuid.NewString(), ExpectedRevision: &current.Revision, Rules: []api.EnvironmentCacheRule{}})
	if err != nil {
		t.Fatal(err)
	}
	_ = cs.Close()
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := OpenStateStore(path, key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = next.Close() })
	opts.planStore = next
	cs, ctx = connectConfiguredTestServer(t, b, opts, "2026-07-28")
	out := callTool(t, ctx, cs, "gregale_apply_environment_cache_inheritance", map[string]any{"plan_id": restore}, false)
	if string(out["inherited"]) != "true" || string(out["revision"]) != "2" {
		t.Fatalf("recovered=%s", out)
	}
	current, err = f.store.GetEnvironmentCachePolicy(ctx, f.account.ID, f.app.ID, "staging")
	if err != nil || current.Inherited || current.Revision != 3 {
		t.Fatal("retry overwrote later intent")
	}
	// Completed receipts must also survive reopening the encrypted journal.
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
	cs, ctx = connectConfiguredTestServer(t, b, opts, "2026-07-28")
	out = callTool(t, ctx, cs, "gregale_apply_environment_cache_inheritance", map[string]any{"plan_id": restore}, false)
	if string(out["revision"]) != "2" {
		t.Fatal("stored result lost restoration revision")
	}
}
