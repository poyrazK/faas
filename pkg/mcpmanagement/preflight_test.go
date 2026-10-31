package mcpmanagement

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestPreflightReadOnlyAndUnknownAdmission(t *testing.T) {
	for _, writes := range []bool{false, true} {
		f, cs, ctx := newReleaseFixture(t, writes)
		out := callTool(t, ctx, cs, "gregale_preflight", map[string]any{"app": "alpha"}, false)
		var checks map[string]preflightCheck
		if err := json.Unmarshal(out["checks"], &checks); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"release_review", "scoped_health", "read_consent"} {
			if checks[key].Status != "passed" {
				t.Fatalf("%s: %+v", key, out)
			}
		}
		for _, key := range []string{"source_access", "api_write_authorization", "native_qualification", "browser_approval", "durable_state", "scoped_operations"} {
			if checks[key].Status != "unknown" {
				t.Fatalf("unverified %s reported available: %+v", key, out)
			}
		}
		want := "blocked"
		if writes {
			want = "passed"
		}
		if checks["write_consent"].Status != want || f.writes != 0 {
			t.Fatal("preflight confused write consent or mutated intent")
		}
	}
}

func TestPreflightIncompatibleReviewAndStoreFailure(t *testing.T) {
	f, _, ctx := newReleaseFixture(t, false)
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/apps/alpha/release-review" {
			_ = json.NewEncoder(w).Encode(api.AppReleaseReview{AppID: "foreign", ConfigurationHash: strings.Repeat("a", 64)})
			return
		}
		f.serveHTTP(w, r)
	}))
	t.Cleanup(b.Close)
	_, manager, err := newManagementServer(api.NewClient(b.URL, testToken), Options{APIBase: b.URL})
	if err != nil {
		t.Fatal(err)
	}
	for _, closed := range []bool{false, true} {
		manager.opts.planStore = &StateStore{closed: closed, persist: &fileState{path: "/private/secret-state"}}
		out, err := manager.preflight(ctx, appArgs{App: "alpha"})
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(out)
		if err != nil || strings.Contains(string(encoded), "secret-state") {
			t.Fatal("store diagnostics exposed private paths")
		}
		checks := out.(map[string]any)["checks"].(map[string]preflightCheck)
		want := "passed"
		if closed {
			want = "blocked"
		}
		if checks["durable_state"].Status != want || checks["release_review"].Status != "unknown" || checks["scoped_health"].Status != "passed" || f.writes != 0 {
			t.Fatalf("preflight trusted foreign review or hid independent checks: %+v", out)
		}
	}
}

func TestPreflightNamedConsentAndRecreatedEnvironment(t *testing.T) {
	f, b, opts := namedStageOptions(t)
	opts.AllowWrites = true
	g := opts.Environments.Named["shop/staging"]
	g.Write = false
	opts.Environments.Named["shop/staging"] = g
	cs, ctx := connectConfiguredTestServer(t, b, opts, "2026-07-28")
	args := map[string]any{"app": "alpha", "project": "shop", "environment": "staging"}
	out := callTool(t, ctx, cs, "gregale_preflight", args, false)
	var checks map[string]preflightCheck
	if err := json.Unmarshal(out["checks"], &checks); err != nil {
		t.Fatal(err)
	}
	if string(out["environment_id"]) != `"env-staging"` || checks["write_consent"].Reason != "environment_write_not_consented" {
		t.Fatalf("lost environment/write consent: %+v", out)
	}
	f.mu.Lock()
	f.envID = "recreated"
	f.mu.Unlock()
	result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "gregale_preflight", Arguments: args})
	if err == nil && !result.IsError {
		t.Fatal("preflight inherited recreated environment consent")
	}
	if f.writes != 0 {
		t.Fatal("preflight mutated intent")
	}
}
