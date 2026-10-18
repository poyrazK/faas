// adr: 20261017210000
package mcpmanagement

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type triageFixture struct {
	oldActionAPI  bool
	environmentID string
	mu            sync.Mutex
	app, scope    string
	row           api.DebugRegressionReview
	receipts      map[string]api.DebugRegressionActionResponse
	bodies        map[string]string
	writes, reads int
	lost, oldAPI  bool
}

func newTriageFixture(t *testing.T) (*triageFixture, *httptest.Server, Options) {
	t.Helper()
	f := &triageFixture{environmentID: "env-one", app: uuid.NewString(), scope: api.DefaultEnvScope, receipts: map[string]api.DebugRegressionActionResponse{}, bodies: map[string]string{}}
	f.row = api.DebugRegressionReview{AppID: f.app, DeploymentID: discoveryDeploymentID, Route: "GET /orders", Version: uuid.NewString(), State: "active", FirstDetectedAt: "2026-10-17T10:00:00Z", LastDetectedAt: "2026-10-17T11:00:00Z"}
	b := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(b.Close)
	opts := Options{APIBase: b.URL, Token: testToken, AllowWrites: true, AllowedApps: map[string]string{"alpha": f.app}, Environments: &EnvironmentConsent{DefaultWrite: true, RequestReadApps: map[string]string{"alpha": f.app}, RegressionWriteApps: map[string]string{"alpha": f.app}}}
	return f, b, opts
}
func (f *triageFixture) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	encode := func(v any) { w.Header().Set("Content-Type", "application/json"); _ = json.NewEncoder(w).Encode(v) }
	switch r.URL.Path {
	case "/v1/account":
		encode(api.AccountResponse{ID: "account-one"})
	case "/v1/apps/alpha":
		encode(api.AppResponse{ID: f.app, Slug: "alpha"})
	case "/v1/projects/shop":
		encode(api.ProjectResponse{ProjectSummaryResponse: api.ProjectSummaryResponse{ID: "project-one", Slug: "shop"}, Workloads: []api.ProjectWorkloadResponse{{Slug: "alpha"}}})
	case "/v1/projects/shop/environments/staging":
		encode(api.ProjectEnvironmentResponse{ID: f.environmentID, ProjectID: "project-one", Slug: "staging"})
	case "/v1/deployments/" + discoveryDeploymentID:
		encode(api.DeploymentResponse{ID: discoveryDeploymentID, AppID: f.app, Scope: f.scope})
	case "/v1/apps/alpha/debug/regressions/review":
		f.reads++
		if f.oldAPI {
			w.WriteHeader(404)
			return
		}
		if r.Method != http.MethodGet || r.URL.Query().Get("deployment_id") != f.row.DeploymentID || r.URL.Query().Get("route") != f.row.Route || len(r.URL.Query()) != 2 {
			w.WriteHeader(400)
			return
		}
		encode(f.row)
	case "/v1/apps/alpha/debug/regressions":
		// This models N-1 accepting a legacy action while ignoring unknown guards.
		f.writes++
		w.WriteHeader(200)
	case "/v1/apps/alpha/debug/regressions/actions":
		if f.oldActionAPI {
			w.WriteHeader(404)
			return
		}
		var req api.GuardedDebugRegressionActionRequest
		if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&req) != nil {
			w.WriteHeader(400)
			return
		}
		body, _ := json.Marshal(req)
		key := r.Header.Get("Idempotency-Key")
		if !strings.HasPrefix(key, "mcp-regression-") {
			w.WriteHeader(400)
			return
		}
		if receipt, ok := f.receipts[key]; ok {
			if f.bodies[key] != string(body) {
				w.WriteHeader(409)
				return
			}
			encode(receipt)
			return
		}
		if req.ExpectedAppID != f.app || req.ExpectedVersion != f.row.Version || req.DeploymentID != f.row.DeploymentID || req.Route != f.row.Route {
			api.WriteProblem(w, api.NewProblem(409, api.CodeRegressionChanged, "Changed", "observation changed"))
			return
		}
		f.writes++
		f.row.State = map[string]string{"acknowledge": "acknowledged", "dismiss": "dismissed", "resolve": "resolved", "reopen": "active"}[req.Action]
		f.row.DismissedUntil = req.DismissedUntil
		f.row.Version = uuid.NewString()
		copy := f.row
		receipt := api.DebugRegressionActionResponse{Review: &copy, Regression: api.DebugRegressionItem{SuspectedDependency: &api.DebugSuspectedDependency{Query: "PRIVATE_SQL"}}}
		f.receipts[key], f.bodies[key] = receipt, string(body)
		if f.lost {
			f.lost = false
			w.WriteHeader(504)
			return
		}
		encode(receipt)
	default:
		w.WriteHeader(404)
	}
}
func triageArgs(action string) map[string]any {
	return map[string]any{"app": "alpha", "deployment_id": discoveryDeploymentID, "route": "GET /orders", "action": action, "reason": "Investigating retained latency observation"}
}

func TestRegressionTriagePlanActionsAndConcurrency(t *testing.T) {
	for _, version := range []string{"2026-07-28", "2025-11-25"} {
		t.Run(version, func(t *testing.T) {
			f, b, opts := newTriageFixture(t)
			cs, ctx := connectConfiguredTestServer(t, b, opts, version)
			for _, action := range []string{"acknowledge", "dismiss", "resolve", "reopen"} {
				p := callTool(t, ctx, cs, "gregale_plan_regression_action", triageArgs(action), false)
				id := planID(t, p)
				before := f.writes
				if action == "dismiss" && !strings.Contains(string(p["regression_triage"]), "dismissed_until") {
					t.Fatal("dismissal expiry was not frozen")
				}
				out := callTool(t, ctx, cs, "gregale_apply_regression_action", map[string]any{"plan_id": id}, false)
				raw, _ := json.Marshal(out)
				if strings.Contains(string(raw), "PRIVATE_SQL") {
					t.Fatal("native diagnostic payload leaked")
				}
				callTool(t, ctx, cs, "gregale_apply_regression_action", map[string]any{"plan_id": id}, false)
				if f.writes != before+1 {
					t.Fatal("cached receipt wrote again")
				}
			}
			stale := planID(t, callTool(t, ctx, cs, "gregale_plan_regression_action", triageArgs("resolve"), false))
			fresh := planID(t, callTool(t, ctx, cs, "gregale_plan_regression_action", triageArgs("acknowledge"), false))
			callTool(t, ctx, cs, "gregale_apply_regression_action", map[string]any{"plan_id": fresh}, false)
			out := callTool(t, ctx, cs, "gregale_apply_regression_action", map[string]any{"plan_id": stale}, true)
			if string(out["code"]) != `"regression_changed"` {
				t.Fatal(out)
			}
		})
	}
}

func TestRegressionTriageConsentIdentityAndNativeCompatibility(t *testing.T) {
	for _, mode := range []string{"no diagnostics", "no triage", "no environment writes", "wrong environment", "app replaced", "old API", "old action API", "bad version", "unsafe route"} {
		t.Run(mode, func(t *testing.T) {
			f, b, opts := newTriageFixture(t)
			args := triageArgs("resolve")
			planFails := false
			switch mode {
			case "no diagnostics":
				opts.Environments.RequestReadApps = nil
				opts.Environments.RegressionWriteApps = nil
				planFails = true
			case "no triage":
				opts.Environments.RegressionWriteApps = nil
			case "no environment writes":
				opts.Environments.DefaultWrite = false
			case "wrong environment":
				f.scope = "staging"
				planFails = true
			case "app replaced":
				f.app = uuid.NewString()
				planFails = true
			case "old action API":
				f.oldActionAPI = true
			case "old API":
				f.oldAPI = true
				planFails = true
			case "bad version":
				f.row.Version = ""
				planFails = true
			case "unsafe route":
				args["route"] = "GET /orders?token=private"
				planFails = true
			}
			cs, ctx := connectConfiguredTestServer(t, b, opts, "2026-07-28")
			p := callTool(t, ctx, cs, "gregale_plan_regression_action", args, planFails)
			if !planFails {
				callTool(t, ctx, cs, "gregale_apply_regression_action", map[string]any{"plan_id": planID(t, p)}, true)
			}
			if f.writes != 0 {
				t.Fatal("unauthorized write")
			}
		})
	}
}

func TestRegressionTriageLostReplyEncryptedRestartAndNarrowedConsent(t *testing.T) {
	f, b, opts := newTriageFixture(t)
	store, path, key := testStateStore(t)
	opts.planStore, opts.planNamespace = store, "triage-grant"
	cs, ctx := connectConfiguredTestServer(t, b, opts, "2026-07-28")
	id := planID(t, callTool(t, ctx, cs, "gregale_plan_regression_action", triageArgs("dismiss"), false))
	f.mu.Lock()
	f.lost = true
	f.mu.Unlock()
	callTool(t, ctx, cs, "gregale_apply_regression_action", map[string]any{"plan_id": id}, true)
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
	out := callTool(t, ctx, cs, "gregale_apply_regression_action", map[string]any{"plan_id": id}, false)
	if f.writes != 1 || string(out["state"]) != `"dismissed"` {
		t.Fatal("original committed result was not recovered")
	}
	_ = cs.Close()
	opts.Environments.RegressionWriteApps = nil
	cs, ctx = connectConfiguredTestServer(t, b, opts, "2026-07-28")
	callTool(t, ctx, cs, "gregale_apply_regression_action", map[string]any{"plan_id": id}, true)
	if f.writes != 1 {
		t.Fatal("narrowed consent wrote")
	}
}

func TestRegressionTriageExpiryAndReadOnly(t *testing.T) {
	f, b, opts := newTriageFixture(t)
	_, m, err := newManagementServer(api.NewClient(b.URL, testToken), opts)
	if err != nil {
		t.Fatal(err)
	}
	args := regressionActionArgs{App: "alpha", DeploymentID: discoveryDeploymentID, Route: "GET /orders", Action: "resolve", Reason: "Investigate incident"}
	value, err := m.planRegressionAction(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	p := value.(releasePlan)
	m.plans[p.ID].ExpiresAt = time.Now().Add(-time.Second)
	if _, err = m.applyRegressionAction(context.Background(), applyArgs{PlanID: p.ID}); err == nil {
		t.Fatal("expired plan applied")
	}
	opts.AllowWrites = false
	cs, ctx := connectConfiguredTestServer(t, b, opts, "2026-07-28")
	catalog, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range catalog.Tools {
		if tool.Name == "gregale_apply_regression_action" {
			t.Fatal("read-only catalog has triage mutation")
		}
	}
	if f.writes != 0 {
		t.Fatal("expiry/read-only checks wrote")
	}
}

func TestRegressionTriageBrowserSelections(t *testing.T) {
	for _, tc := range []struct {
		name   string
		writes bool
		reads  map[string]string
		form   url.Values
		ok     bool
	}{
		{"explicit", true, map[string]string{"alpha": "app-one"}, url.Values{"regression_write_apps": {"alpha"}}, true},
		{"legacy", true, map[string]string{"alpha": "app-one"}, url.Values{}, true},
		{"read only", false, map[string]string{"alpha": "app-one"}, url.Values{"regression_write_apps": {"alpha"}}, false},
		{"no diagnostics", true, nil, url.Values{"regression_write_apps": {"alpha"}}, false},
		{"unknown app", true, map[string]string{"alpha": "app-one"}, url.Values{"regression_write_apps": {"beta"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &http.Request{PostForm: tc.form}
			got, err := selectedRegressionWriteApps(r, tc.reads, tc.writes)
			if (err == nil) != tc.ok {
				t.Fatal(got, err)
			}
			if tc.name == "legacy" && len(got) != 0 {
				t.Fatal("legacy consent acquired triage authority")
			}
		})
	}
}

func TestRegressionTriageNamedWriteConsentAndRegistryReplacement(t *testing.T) {
	for _, namedWrite := range []bool{false, true} {
		t.Run(map[bool]string{false: "default write only", true: "named write only"}[namedWrite], func(t *testing.T) {
			f, b, opts := newTriageFixture(t)
			f.scope = "staging"
			opts.Environments.DefaultWrite = !namedWrite
			opts.Environments.Named = map[string]EnvironmentGrant{"shop/staging": {Project: "shop", ProjectID: "project-one", Environment: "staging", EnvironmentID: f.environmentID, Apps: map[string]string{"alpha": f.app}, Write: namedWrite}}
			cs, ctx := connectConfiguredTestServer(t, b, opts, "2026-07-28")
			args := triageArgs("resolve")
			args["project"], args["environment"] = "shop", "staging"
			id := planID(t, callTool(t, ctx, cs, "gregale_plan_regression_action", args, false))
			callTool(t, ctx, cs, "gregale_apply_regression_action", map[string]any{"plan_id": id}, !namedWrite)
			if !namedWrite {
				if f.writes != 0 {
					t.Fatal("default consent authorized named write")
				}
				return
			}
			if f.writes != 1 {
				t.Fatal("explicit named consent did not authorize triage")
			}
			f.mu.Lock()
			f.environmentID = "replacement-env"
			f.mu.Unlock()
			callTool(t, ctx, cs, "gregale_apply_regression_action", map[string]any{"plan_id": id}, true)
			if f.writes != 1 {
				t.Fatal("registry replacement inherited a cached receipt")
			}
		})
	}
}
