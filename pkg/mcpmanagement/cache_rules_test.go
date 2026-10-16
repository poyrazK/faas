package mcpmanagement

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

type cacheFixture struct {
	mu      sync.Mutex
	version int
	rule    api.EdgeRuleResponse
	receipt *api.EdgeRuleResponse
	key     string
	writes  int
	lost    bool
	account string
	app     string
}

func newCacheFixture(t *testing.T) (*cacheFixture, *httptest.Server, Options) {
	t.Helper()
	f := &cacheFixture{version: 1, account: "account", app: "app", rule: api.EdgeRuleResponse{ID: "cache-rule", AppID: "app", AccountID: "account", MatchHost: api.EdgeRuleAppHosts, MatchPath: "/catalog/*", MatchMethods: []string{"GET"}, Priority: 100, Kind: "cache", Enabled: true, Mode: api.EdgeRuleModeEnforce, Action: json.RawMessage(`{"kind":"cache","cache":{"max_age_seconds":30,"stale_if_error_seconds":0}}`)}}
	b := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(b.Close)
	return f, b, Options{APIBase: b.URL, Token: testToken, AllowWrites: true}
}

func (f *cacheFixture) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	encode := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	switch {
	case r.URL.Path == "/v1/account":
		encode(api.AccountResponse{ID: f.account})
	case r.URL.Path == "/v1/apps/alpha":
		encode(api.AppResponse{ID: f.app, Slug: "alpha", Type: "app"})
	case r.URL.Path == "/v1/apps/alpha/edge-rules/versions":
		encode([]api.EdgeRuleSetVersionResponse{{Version: f.version, Current: true, RulesSHA256: "snapshot"}})
	case strings.HasPrefix(r.URL.Path, "/v1/apps/alpha/edge-rules/versions/"):
		// An unrelated historical rule contains values the MCP review must omit.
		secret := api.EdgeRuleResponse{ID: "private", Description: "private-rule-value", MatchHeaders: map[string]string{"x-private": "secret-condition"}, Action: json.RawMessage(`{"secret":"secret-action"}`)}
		encode(api.EdgeRuleSetVersionResponse{Version: f.version, Current: true, RulesSHA256: "snapshot", RuleCount: 2, Rules: []api.EdgeRuleResponse{f.rule, secret}})
	case r.Method == http.MethodPatch && r.URL.Path == "/v1/edge-rules/cache-rule":
		key := r.Header.Get("Idempotency-Key")
		if key == "" {
			w.WriteHeader(400)
			return
		}
		if f.receipt != nil && key == f.key {
			encode(*f.receipt)
			return
		}
		if r.Header.Get("If-Match") != api.EdgeRuleSetETag(f.version) {
			api.WriteProblem(w, api.ErrEdgeRulesVersionMismatch(r.Header.Get("If-Match"), f.version))
			return
		}
		var req api.UpdateEdgeRuleRequest
		if json.NewDecoder(r.Body).Decode(&req) != nil || req.Action == nil {
			w.WriteHeader(400)
			return
		}
		var cache api.EdgeRuleCacheAction
		if json.Unmarshal(*req.Action, &cache) != nil || cache.Validate() != nil {
			w.WriteHeader(422)
			return
		}
		f.rule.Action, _ = json.Marshal(map[string]any{"kind": "cache", "cache": cache})
		f.version++
		f.writes++
		f.key = key
		copy := f.rule
		f.receipt = &copy
		if f.lost {
			f.lost = false
			w.WriteHeader(504)
			return
		}
		encode(f.rule)
	default:
		w.WriteHeader(404)
	}
}

func cacheArgs() map[string]any {
	return map[string]any{"app": "alpha", "rule_id": "cache-rule", "reason": "Reduce catalogue wakes", "cache": map[string]any{"max_age_seconds": 120, "stale_if_error_seconds": 0}}
}

func TestCacheRuleReviewApplyAndStale(t *testing.T) {
	f, b, opts := newCacheFixture(t)
	cs, ctx := connectConfiguredTestServer(t, b, opts, "2026-07-28")
	review := callTool(t, ctx, cs, "gregale_plan_cache_rule", cacheArgs(), false)
	data, _ := json.Marshal(review)
	for _, secret := range []string{"private-rule-value", "secret-condition", "secret-action"} {
		if strings.Contains(string(data), secret) {
			t.Fatal("review disclosed unrelated rule")
		}
	}
	if string(review["scope"]) != `"app_shared"` {
		t.Fatal("review mislabels shared policy as an environment change")
	}
	if f.writes != 0 || !strings.Contains(string(data), "app_shared") || !strings.Contains(string(data), "named environments") {
		t.Fatal("review lacks shared scope or mutated policy")
	}
	stale := planID(t, review)
	current := planID(t, callTool(t, ctx, cs, "gregale_plan_cache_rule", cacheArgs(), false))
	out := callTool(t, ctx, cs, "gregale_apply_cache_rule", map[string]any{"plan_id": current}, false)
	if string(out["policy_scope"]) != `"app_shared"` {
		t.Fatal("receipt scope missing")
	}
	callTool(t, ctx, cs, "gregale_apply_cache_rule", map[string]any{"plan_id": current}, false)
	callTool(t, ctx, cs, "gregale_apply_cache_rule", map[string]any{"plan_id": stale}, true)
	if f.writes != 1 {
		t.Fatal("replay or stale plan wrote again")
	}
	f.mu.Lock()
	f.app = "replacement"
	f.mu.Unlock()
	callTool(t, ctx, cs, "gregale_apply_cache_rule", map[string]any{"plan_id": current}, true)
}

func TestCacheRuleLostReplyRestart(t *testing.T) {
	f, b, opts := newCacheFixture(t)
	store, path, key := testStateStore(t)
	cs, ctx := connectLocalStateTestServer(t, b, opts, store)
	plan := planID(t, callTool(t, ctx, cs, "gregale_plan_cache_rule", cacheArgs(), false))
	f.mu.Lock()
	f.lost = true
	f.mu.Unlock()
	callTool(t, ctx, cs, "gregale_apply_cache_rule", map[string]any{"plan_id": plan}, true)
	_ = cs.Close()
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := OpenStateStore(path, key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = next.Close() })
	cs, ctx = connectLocalStateTestServer(t, b, opts, next)
	callTool(t, ctx, cs, "gregale_apply_cache_rule", map[string]any{"plan_id": plan}, false)
	if f.writes != 1 {
		t.Fatal("lost reply recovery changed policy twice")
	}
	f.mu.Lock()
	f.account = "other"
	f.mu.Unlock()
	callTool(t, ctx, cs, "gregale_apply_cache_rule", map[string]any{"plan_id": plan}, true)
}

func TestCacheRuleConsentAndUnsupportedRules(t *testing.T) {
	for _, consent := range []*EnvironmentConsent{{DefaultWrite: true}, {Named: map[string]EnvironmentGrant{}}} {
		_, b, opts := newCacheFixture(t)
		opts.Environments = consent
		cs, ctx := connectConfiguredTestServer(t, b, opts, "2026-07-28")
		callTool(t, ctx, cs, "gregale_plan_cache_rule", cacheArgs(), true)
	}
	f, b, opts := newCacheFixture(t)
	cs, ctx := connectConfiguredTestServer(t, b, opts, "2026-07-28")
	args := cacheArgs()
	args["environment"] = "staging"
	args["project"] = "shop"
	callTool(t, ctx, cs, "gregale_plan_cache_rule", args, true)
	original := f.rule
	cases := []func(*api.EdgeRuleResponse){
		func(r *api.EdgeRuleResponse) { r.MatchHeaders = map[string]string{"authorization": "private"} },
		func(r *api.EdgeRuleResponse) { r.Mode = "log" },
		func(r *api.EdgeRuleResponse) { r.Enabled = false },
		func(r *api.EdgeRuleResponse) { r.AppID = "other" },
		func(r *api.EdgeRuleResponse) { r.MatchHost = "*.example.com" },
		func(r *api.EdgeRuleResponse) { at := time.Now().Add(time.Hour); r.ExpiresAt = &at },
	}
	for _, change := range cases {
		f.mu.Lock()
		f.rule = original
		change(&f.rule)
		f.mu.Unlock()
		callTool(t, ctx, cs, "gregale_plan_cache_rule", cacheArgs(), true)
	}
	if f.writes != 0 {
		t.Fatal("unsupported scope/rule mutated policy")
	}
}

func TestCacheRuleRechecksConsentForSavedReceipt(t *testing.T) {
	f, b, opts := newCacheFixture(t)
	_, m, err := newManagementServer(api.NewClient(b.URL, testToken), opts)
	if err != nil {
		t.Fatal(err)
	}
	value, err := m.planCacheRule(t.Context(), cacheRulePlanArgs{App: "alpha", RuleID: "cache-rule", Reason: "Review shared policy", Cache: api.EdgeRuleCacheAction{MaxAgeSeconds: 120, VaryOn: []string{}}})
	if err != nil {
		t.Fatal(err)
	}
	p := value.(releasePlan)
	if _, err = m.applyCacheRule(t.Context(), applyArgs{PlanID: p.ID}); err != nil {
		t.Fatal(err)
	}
	m.opts.Environments = &EnvironmentConsent{DefaultWrite: true}
	if _, err = m.applyCacheRule(t.Context(), applyArgs{PlanID: p.ID}); err == nil {
		t.Fatal("saved receipt bypassed narrower environment consent")
	}
	m.opts.Environments = nil
	m.opts.AllowWrites = false
	if _, err = m.applyCacheRule(t.Context(), applyArgs{PlanID: p.ID}); err == nil {
		t.Fatal("saved receipt bypassed disabled writes")
	}
	m.opts.AllowWrites = true
	m.plans[p.ID].ExpiresAt = time.Now().Add(-time.Second)
	if _, err = m.applyCacheRule(t.Context(), applyArgs{PlanID: p.ID}); err == nil {
		t.Fatal("expired plan was accepted")
	}
	if f.writes != 1 {
		t.Fatal("guard failure repeated the write")
	}
}

func connectLocalStateTestServer(t *testing.T, backend *httptest.Server, opts Options, store *StateStore) (*mcp.ClientSession, context.Context) {
	t.Helper()
	ctx := t.Context()
	s, err := NewLocalServerWithState(ctx, api.NewClient(backend.URL, testToken), opts, store)
	if err != nil {
		t.Fatal(err)
	}
	ct, st := mcp.NewInMemoryTransports()
	ss, err := s.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "local-state-test", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs, ctx
}

func TestLocalReviewStateDoesNotCrossProfilesOrAccounts(t *testing.T) {
	f, b, opts := newCacheFixture(t)
	store, _, _ := testStateStore(t)
	t.Cleanup(func() { _ = store.Close() })
	cs, ctx := connectLocalStateTestServer(t, b, opts, store)
	plan := planID(t, callTool(t, ctx, cs, "gregale_plan_cache_rule", cacheArgs(), false))
	_ = cs.Close()
	other := opts
	other.Profile = "other"
	cs, ctx = connectLocalStateTestServer(t, b, other, store)
	callTool(t, ctx, cs, "gregale_apply_cache_rule", map[string]any{"plan_id": plan}, true)
	_ = cs.Close()
	otherBackend := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(otherBackend.Close)
	other = opts
	other.APIBase = otherBackend.URL
	cs, ctx = connectLocalStateTestServer(t, otherBackend, other, store)
	callTool(t, ctx, cs, "gregale_apply_cache_rule", map[string]any{"plan_id": plan}, true)
	_ = cs.Close()
	f.mu.Lock()
	f.account = "other"
	f.mu.Unlock()
	cs, ctx = connectLocalStateTestServer(t, b, opts, store)
	callTool(t, ctx, cs, "gregale_apply_cache_rule", map[string]any{"plan_id": plan}, true)
	if f.writes != 0 {
		t.Fatal("local journal crossed account/profile boundary")
	}
}
