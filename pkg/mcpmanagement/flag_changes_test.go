// adr: 20261117215858
package mcpmanagement

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/flags"
)

// flagAPI is a fake flag API: one project environment with a versioned
// configuration history.
type flagAPI struct {
	mu        sync.Mutex
	versions  []flagSnapshot
	puts      int
	kills     int
	loseReply bool
	approval  bool
}

func newFlagAPI(t *testing.T) (*flagAPI, *httptest.Server) {
	t.Helper()
	f := &flagAPI{versions: []flagSnapshot{{Version: 1, Config: flags.Config{Flags: []flags.Flag{
		{Key: "checkout", Enabled: true, Default: false, Seed: "s1", Rules: []flags.Rule{}},
	}, Groups: map[string][]string{}}}}}
	b := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(b.Close)
	return f, b
}

func (f *flagAPI) latest() flagSnapshot { return f.versions[len(f.versions)-1] }

func (f *flagAPI) publish(cfg flags.Config) flagSnapshot {
	next := flagSnapshot{Version: f.latest().Version + 1, Config: cfg}
	f.versions = append(f.versions, next)
	return next
}

func (f *flagAPI) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	encode := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	const base = "/v1/projects/shop/environments/production"
	switch {
	case r.URL.Path == "/v1/account":
		encode(api.AccountResponse{ID: "acct-1", Email: "agent@test.example"})
	case r.URL.Path == "/v1/projects/shop":
		encode(map[string]any{"id": "proj-1", "slug": "shop", "workloads": []any{}})
	case r.URL.Path == base:
		encode(api.ProjectEnvironmentResponse{ID: "env-1", ProjectID: "proj-1", Slug: "production"})
	case r.URL.Path == base+"/flags" && r.Method == http.MethodGet:
		encode(f.latest())
	case r.URL.Path == base+"/flags/lifecycle":
		encode(map[string]any{"config_version": f.latest().Version, "flags": []any{}})
	case r.URL.Path == base+"/flags/versions":
		out := []flagSnapshot{}
		for i := len(f.versions) - 1; i >= 0; i-- {
			out = append(out, f.versions[i])
		}
		encode(out)
	case r.URL.Path == base+"/flags" && r.Method == http.MethodPut:
		if f.approval {
			api.WriteProblem(w, api.NewProblem(http.StatusPreconditionRequired, "approval_required", "Approval required", "ask a person"))
			return
		}
		var req struct {
			ExpectedVersion int64        `json:"expected_version"`
			Config          flags.Config `json:"config"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.puts++
		if req.ExpectedVersion != f.latest().Version {
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, flagsConflictCode, "Configuration changed", "refresh"))
			return
		}
		out := f.publish(req.Config)
		if f.loseReply {
			f.loseReply = false
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		encode(out)
	case r.URL.Path == base+"/flags/rollback":
		var req struct {
			ExpectedVersion int64 `json:"expected_version"`
			Version         int64 `json:"version"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.ExpectedVersion != f.latest().Version {
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, flagsConflictCode, "Configuration changed", "refresh"))
			return
		}
		encode(f.publish(f.versions[req.Version-1].Config))
	case strings.HasPrefix(r.URL.Path, base+"/flags/") && strings.HasSuffix(r.URL.Path, "/kill"):
		f.kills++
		key := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, base+"/flags/"), "/kill")
		cfg, _ := cloneFlagConfig(f.latest().Config)
		if _, err := flags.EditKill(key, true)(&cfg); err != nil {
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", err.Error()))
			return
		}
		encode(api.FeatureFlagKillResponse{Key: key, Killed: true, Changed: true, Version: f.publish(cfg).Version})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func connectFlagServer(t *testing.T) (*flagAPI, func(string, any, bool) map[string]json.RawMessage) {
	t.Helper()
	f, b := newFlagAPI(t)
	cs, ctx := connectConfiguredTestServer(t, b, Options{APIBase: b.URL, Token: testToken, AllowWrites: true}, "2025-06-18")
	return f, func(name string, args any, wantError bool) map[string]json.RawMessage {
		t.Helper()
		return callTool(t, ctx, cs, name, args, wantError)
	}
}

func TestFlagPlanAppliesTheReviewedConfiguration(t *testing.T) {
	f, call := connectFlagServer(t)
	plan := call("gregale_plan_flag_change", map[string]any{"project": "shop", "environment": "production", "reason": "roll out new checkout",
		"changes": []map[string]any{
			{"op": "set_percent", "key": "checkout", "percent": "25"},
			{"op": "create", "key": "dark-mode", "variants": []string{"off", "on"}, "owner": "web"},
		}}, false)
	var changed []flagDiff
	_ = json.Unmarshal(plan["changed"], &changed)
	if len(changed) != 2 || changed[0].Key != "checkout" || changed[1].Key != "dark-mode" || changed[1].Before != nil {
		t.Fatalf("changed = %+v", changed)
	}
	if f.puts != 0 {
		t.Fatal("planning published a configuration")
	}
	var id string
	_ = json.Unmarshal(plan["plan_id"], &id)
	receipt := call("gregale_apply_flag_change", map[string]any{"plan_id": id}, false)
	if !strings.Contains(string(receipt["result"]), `"version":2`) {
		t.Fatalf("receipt = %s", receipt["result"])
	}
	got := f.latest().Config
	c, _ := flags.FindFlag(&got, "checkout")
	if len(c.Rules) != 1 || c.Rules[0].ID != flags.EveryonePercentRule || *c.Rules[0].Rollout != 2500 {
		t.Fatalf("checkout rules = %+v", c.Rules)
	}
	if d, err := flags.FindFlag(&got, "dark-mode"); err != nil || d.Owner != "web" || len(d.Variants) != 2 {
		t.Fatalf("dark-mode = %+v %v", d, err)
	}
	// A repeated apply returns the same receipt without publishing again.
	call("gregale_apply_flag_change", map[string]any{"plan_id": id}, false)
	if f.puts != 1 {
		t.Fatalf("puts = %d, want 1", f.puts)
	}
}

func TestFlagPlanRefusesAConfigurationThatChanged(t *testing.T) {
	f, call := connectFlagServer(t)
	plan := call("gregale_plan_flag_change", map[string]any{"project": "shop", "environment": "production", "reason": "turn off", "changes": []map[string]any{{"op": "disable", "key": "checkout"}}}, false)
	f.mu.Lock()
	f.publish(f.latest().Config)
	f.mu.Unlock()
	var id string
	_ = json.Unmarshal(plan["plan_id"], &id)
	out := call("gregale_apply_flag_change", map[string]any{"plan_id": id}, true)
	if !strings.Contains(string(out["code"]), "plan_stale") {
		t.Fatalf("error = %v", out)
	}
}

func TestFlagApplyRecoversItsOwnLostPublish(t *testing.T) {
	f, call := connectFlagServer(t)
	f.loseReply = true
	plan := call("gregale_plan_flag_change", map[string]any{"project": "shop", "environment": "production", "reason": "enable", "changes": []map[string]any{{"op": "set", "key": "checkout", "description": "new checkout"}}}, false)
	var id string
	_ = json.Unmarshal(plan["plan_id"], &id)
	call("gregale_apply_flag_change", map[string]any{"plan_id": id}, true)
	receipt := call("gregale_apply_flag_change", map[string]any{"plan_id": id}, false)
	if !strings.Contains(string(receipt["result"]), `"version":2`) || len(f.versions) != 2 {
		t.Fatalf("receipt = %s, versions = %d", receipt["result"], len(f.versions))
	}
}

func TestFlagPlanRejectsInvalidEditsBeforeReading(t *testing.T) {
	_, call := connectFlagServer(t)
	for _, change := range []map[string]any{
		{"op": "explode", "key": "checkout"},
		{"op": "set_percent", "key": "checkout", "percent": "101"},
		{"op": "set", "key": "checkout"},
		{"op": "enable", "key": "Bad Key"},
		{"op": "disable", "key": "missing"},
	} {
		call("gregale_plan_flag_change", map[string]any{"project": "shop", "environment": "production", "reason": "x", "changes": []map[string]any{change}}, true)
	}
}

func TestFlagRollbackPlanRestoresAnEarlierVersion(t *testing.T) {
	f, call := connectFlagServer(t)
	f.publish(flags.Config{Flags: []flags.Flag{{Key: "checkout", Enabled: false, Default: false, Seed: "s1", Rules: []flags.Rule{}}}, Groups: map[string][]string{}})
	plan := call("gregale_plan_flag_rollback", map[string]any{"project": "shop", "environment": "production", "version": 1, "reason": "undo"}, false)
	var id string
	_ = json.Unmarshal(plan["plan_id"], &id)
	call("gregale_apply_flag_change", map[string]any{"plan_id": id}, false)
	got := f.latest()
	if got.Version != 3 || !got.Flags[0].Enabled {
		t.Fatalf("latest = %+v", got)
	}
}

func TestKillFlagNeedsNoPlan(t *testing.T) {
	f, call := connectFlagServer(t)
	out := call("gregale_kill_flag", map[string]any{"project": "shop", "environment": "production", "key": "checkout", "reason": "5xx spike"}, false)
	if string(out["killed"]) != "true" || f.kills != 1 || !f.latest().Flags[0].Killed {
		t.Fatalf("kill = %v", out)
	}
}

func TestFlagApplySurfacesApprovalAndRetriesTheSamePlan(t *testing.T) {
	f, call := connectFlagServer(t)
	f.approval = true
	plan := call("gregale_plan_flag_change", map[string]any{"project": "shop", "environment": "production", "reason": "on", "changes": []map[string]any{{"op": "set_percent", "key": "checkout", "percent": "5"}}}, false)
	var id string
	_ = json.Unmarshal(plan["plan_id"], &id)
	call("gregale_apply_flag_change", map[string]any{"plan_id": id}, true)
	f.mu.Lock()
	f.approval = false
	f.mu.Unlock()
	call("gregale_apply_flag_change", map[string]any{"plan_id": id}, false)
	if f.latest().Version != 2 {
		t.Fatalf("version = %d", f.latest().Version)
	}
}
