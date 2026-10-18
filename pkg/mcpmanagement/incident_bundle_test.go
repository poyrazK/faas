// adr: 20261017232000
package mcpmanagement

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type incidentFixture struct {
	mu          sync.Mutex
	appID       string
	deployments map[string]api.DeploymentResponse
	evidence    api.DebugRequestEvidenceResponse
	coverage    api.DebugCoverageResponse
	regressions api.DebugRegressionsResponse
	comparison  api.DebugCompareResponse
	blocked     string
	entered     chan struct{}
	fail        map[string]bool
	reads       []string
	onRead      func(string)
}

func newIncidentFixture(t *testing.T) (*incidentFixture, *httptest.Server) {
	t.Helper()
	f := &incidentFixture{appID: "app-1", deployments: map[string]api.DeploymentResponse{}, evidence: withRequestPath(withRequestRegression(withRequestGuest(requestFixtureEvidence()))), coverage: performanceFixtureCoverage("app-1"), regressions: regressionsFixture(), comparison: comparisonFixture("app-1"), fail: map[string]bool{}}
	for _, id := range []string{"release-base", discoveryDeploymentID, comparisonTargetID} {
		f.deployments[id] = api.DeploymentResponse{ID: id, AppID: "app-1", Scope: "staging"}
	}
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		path := r.URL.Path
		f.reads = append(f.reads, path)
		if f.blocked == path {
			if f.entered != nil {
				select {
				case f.entered <- struct{}{}:
				default:
				}
			}
			f.mu.Unlock()
			<-r.Context().Done()
			f.mu.Lock()
			return
		}
		var value any
		switch {
		case path == "/v1/apps/alpha":
			value = api.AppResponse{ID: f.appID, Slug: "alpha"}
		case strings.HasPrefix(path, "/v1/deployments/"):
			value = f.deployments[strings.TrimPrefix(path, "/v1/deployments/")]
		case path == "/v1/apps/alpha/debug/requests/row-1/evidence":
			value = f.evidence
		case path == "/v1/apps/alpha/debug/coverage":
			value = f.coverage
		case path == "/v1/apps/alpha/debug/regressions":
			value = f.regressions
		case path == "/v1/apps/alpha/debug/compare":
			var selection api.DebugCompareRequest
			if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&selection) != nil || selection.Source != discoveryDeploymentID || selection.Mirror != comparisonTargetID || selection.Since != "1h" {
				t.Error("comparison changed the exact explicit selection")
			}
			value = f.comparison
		default:
			t.Errorf("unexpected incident request %s %s", r.Method, path)
			w.WriteHeader(404)
			return
		}
		if path != "/v1/apps/alpha/debug/compare" && r.Method != http.MethodGet {
			t.Error("incident bundle performed a native mutation")
		}
		if path == "/v1/apps/alpha/debug/coverage" || path == "/v1/apps/alpha/debug/regressions" {
			if r.URL.Query().Get("since") != "1h" || len(r.URL.Query()) != 1 {
				t.Error("bundle did not use explicit aggregate lookback or forwarded projection limits")
			}
		}
		if f.onRead != nil {
			f.onRead(path)
		}
		w.Header().Set("Content-Type", "application/json")
		if f.fail[path] {
			w.WriteHeader(503)
			problem := api.NewProblem(503, "incident_unavailable", "PRIVATE_TITLE", "PRIVATE_DETAIL "+testToken)
			retry := int64(7)
			problem.RetryAfterSeconds = &retry
			value = problem
		}
		_ = json.NewEncoder(w).Encode(value)
	}))
	t.Cleanup(b.Close)
	return f, b
}

func incidentArgs(compare bool) incidentBundleArgs {
	args := incidentBundleArgs{App: "alpha", RequestID: "row-1"}
	if compare {
		args.SourceDeploymentID, args.TargetDeploymentID = discoveryDeploymentID, comparisonTargetID
	}
	return args
}

func decodeIncidentBundle(t *testing.T, out map[string]json.RawMessage) incidentBundleView {
	t.Helper()
	data, _ := json.Marshal(out)
	for _, excluded := range []string{"PRIVATE_", "excluded-", "private-query", "fp_test_", `"spans"`, `"query"`} {
		if strings.Contains(string(data), excluded) {
			t.Fatalf("incident leaked %q", excluded)
		}
	}
	if len(data) > maxResultBytes {
		t.Fatal("incident exceeded shared output bound")
	}
	var view incidentBundleView
	if err := json.Unmarshal(data, &view); err != nil {
		t.Fatal(err)
	}
	return view
}

func TestIncidentBundleConsentAndProjection(t *testing.T) {
	for _, version := range []string{"2026-07-28", "2025-11-25"} {
		for _, mode := range []string{"local", "approved", "denied", "replaced", "app_denied"} {
			t.Run(version+"/"+mode, func(t *testing.T) {
				f, b := newIncidentFixture(t)
				opts := Options{APIBase: b.URL}
				if mode != "local" {
					opts.Environments = &EnvironmentConsent{RequestReadApps: map[string]string{"alpha": "app-1"}}
				}
				if mode == "denied" {
					opts.Environments.RequestReadApps = nil
				}
				if mode == "replaced" {
					opts.Environments.RequestReadApps["alpha"] = "old-app"
				}
				if mode == "app_denied" {
					opts.AllowedApps = map[string]string{"alpha": "old-app"}
					opts.Environments.RequestReadApps["alpha"] = "old-app"
				}
				cs, ctx := connectConfiguredTestServer(t, b, opts, version)
				denied := mode == "denied" || mode == "replaced" || mode == "app_denied"
				out := callTool(t, ctx, cs, "gregale_get_incident_bundle", incidentArgs(true), denied)
				if denied {
					f.mu.Lock()
					defer f.mu.Unlock()
					for _, path := range f.reads {
						if strings.Contains(path, "/debug/") || strings.HasPrefix(path, "/v1/deployments/") {
							t.Fatal("denied bundle read diagnostics")
						}
					}
					return
				}
				v := decodeIncidentBundle(t, out)
				if v.Status != "ok" || v.Partial || v.Scope != "app_wide" || v.AppID != "app-1" || v.RequestedSince != "1h" || v.SchemaVersion != "gregale.mcp.incident/v1" || v.SourceCompleteness != "unknown" {
					t.Fatal("bundle lost selection or uncertainty")
				}
				for _, s := range []incidentBundleSection{v.Evidence, v.Coverage, v.Regressions, v.Comparison} {
					start, e1 := time.Parse(time.RFC3339Nano, s.ReadStartedAt)
					end, e2 := time.Parse(time.RFC3339Nano, s.ReadFinishedAt)
					if s.Status != "ok" || s.SourceTool == "" || len(s.Result) == 0 || s.Error != nil || e1 != nil || e2 != nil || end.Before(start) {
						t.Fatal("bundle lost per-section provenance")
					}
				}
				var evidence requestEvidenceView
				_ = json.Unmarshal(v.Evidence.Result, &evidence)
				var coverage coverageView
				_ = json.Unmarshal(v.Coverage.Result, &coverage)
				var comparison deploymentComparisonView
				_ = json.Unmarshal(v.Comparison.Result, &comparison)
				if evidence.Request.ID != "row-1" || len(evidence.Explanation.Findings) == 0 || evidence.Explanation.Findings[0].EvidenceRefs[0] != "/request" || coverage.WindowEnd == comparison.WindowEnd || comparison.SourceDeploymentID != discoveryDeploymentID || !strings.Contains(v.Limitations, "relative") {
					t.Fatal("native windows, nested references or exact comparison were changed")
				}
			})
		}
	}
}

func TestIncidentBundleValidationBeforeReads(t *testing.T) {
	for _, mutate := range []func(*incidentBundleArgs){
		func(a *incidentBundleArgs) { a.RequestID = "" }, func(a *incidentBundleArgs) { a.App = "../alpha" }, func(a *incidentBundleArgs) { a.Since = "-1h" },
		func(a *incidentBundleArgs) { a.SourceDeploymentID = discoveryDeploymentID }, func(a *incidentBundleArgs) { a.Route = "/orders" },
		func(a *incidentBundleArgs) {
			a.SourceDeploymentID = discoveryDeploymentID
			a.TargetDeploymentID = discoveryDeploymentID
		},
		func(a *incidentBundleArgs) {
			a.SourceDeploymentID = discoveryDeploymentID
			a.TargetDeploymentID = comparisonTargetID
			a.Route = "/orders?secret=yes"
		},
	} {
		f, b := newIncidentFixture(t)
		cs, ctx := connectConfiguredTestServer(t, b, Options{APIBase: b.URL}, "2026-07-28")
		args := incidentArgs(false)
		mutate(&args)
		callTool(t, ctx, cs, "gregale_get_incident_bundle", args, true)
		f.mu.Lock()
		reads := len(f.reads)
		f.mu.Unlock()
		if reads != 0 {
			t.Fatal("invalid selection made native reads")
		}
	}
}

func TestIncidentBundlePartialAndRequiredEvidence(t *testing.T) {
	for _, mode := range []string{"no_comparison", "coverage_failed", "all_optional_failed", "evidence_failed", "foreign_coverage", "widened_coverage", "foreign_regression", "changed_comparison"} {
		t.Run(mode, func(t *testing.T) {
			f, b := newIncidentFixture(t)
			if mode == "coverage_failed" || mode == "all_optional_failed" {
				f.fail["/v1/apps/alpha/debug/coverage"] = true
			}
			if mode == "all_optional_failed" {
				f.fail["/v1/apps/alpha/debug/regressions"] = true
				f.fail["/v1/apps/alpha/debug/compare"] = true
			}
			if mode == "evidence_failed" {
				f.fail["/v1/apps/alpha/debug/requests/row-1/evidence"] = true
			}
			if mode == "foreign_coverage" {
				f.coverage.AppID = "other-app"
			}
			if mode == "widened_coverage" {
				f.coverage.Since = "2h"
				f.coverage.WindowStart = "2026-10-17T09:00:00Z"
			}
			if mode == "foreign_regression" {
				d := f.deployments[discoveryDeploymentID]
				d.AppID = "other-app"
				f.deployments[discoveryDeploymentID] = d
			}
			if mode == "changed_comparison" {
				f.comparison.Mirror = discoveryDeploymentID
			}
			cs, ctx := connectConfiguredTestServer(t, b, Options{APIBase: b.URL}, "2026-07-28")
			out := callTool(t, ctx, cs, "gregale_get_incident_bundle", incidentArgs(mode != "no_comparison"), mode == "evidence_failed")
			if mode == "evidence_failed" {
				f.mu.Lock()
				defer f.mu.Unlock()
				for _, path := range f.reads {
					if strings.HasSuffix(path, "/coverage") || strings.HasSuffix(path, "/regressions") || strings.HasSuffix(path, "/compare") {
						t.Fatal("required evidence failure collected unrelated aggregates")
					}
				}
				return
			}
			v := decodeIncidentBundle(t, out)
			if mode == "no_comparison" {
				if v.Status != "ok" || v.Partial || v.Comparison.Status != "not_requested" || len(v.Comparison.Result) != 0 {
					t.Fatal("unrequested comparison collected or invented evidence")
				}
				return
			}
			if v.Status != "partial" || !v.Partial || v.Evidence.Status != "ok" {
				t.Fatal("optional failure lost required evidence")
			}
			for _, s := range []incidentBundleSection{v.Coverage, v.Regressions, v.Comparison} {
				if s.Status == "error" && (s.Error == nil || len(s.Result) != 0) {
					t.Fatal("failed section retained evidence")
				}
			}
			if mode == "coverage_failed" && (v.Coverage.Error.Code != "incident_unavailable" || v.Coverage.Error.Status != 503 || *v.Coverage.Error.RetryAfterSeconds != 7) {
				t.Fatal("lost retry metadata")
			}
		})
	}
}

func TestIncidentBundleLateIdentityChanges(t *testing.T) {
	for _, mode := range []string{"app", "evidence_scope", "regression_scope", "regression_owner"} {
		t.Run(mode, func(t *testing.T) {
			f, b := newIncidentFixture(t)
			f.onRead = func(path string) {
				if path != "/v1/apps/alpha/debug/compare" {
					return
				}
				if mode == "app" {
					f.appID = "replacement"
					return
				}
				id := discoveryDeploymentID
				if mode == "evidence_scope" {
					id = "release-base"
				}
				d := f.deployments[id]
				d.Scope = "changed"
				if mode == "regression_owner" {
					d.AppID = "other-app"
				}
				f.deployments[id] = d
			}
			cs, ctx := connectConfiguredTestServer(t, b, Options{APIBase: b.URL}, "2026-07-28")
			fatal := mode == "app" || mode == "evidence_scope"
			out := callTool(t, ctx, cs, "gregale_get_incident_bundle", incidentArgs(true), fatal)
			if fatal {
				if out["evidence"] != nil {
					t.Fatal("identity failure exposed collected evidence")
				}
				return
			}
			v := decodeIncidentBundle(t, out)
			if !v.Partial || v.Evidence.Status != "ok" || v.Regressions.Status != "error" || len(v.Regressions.Result) != 0 || v.Comparison.Status != "error" {
				t.Fatal("late deployment change released stale section")
			}
		})
	}
}

func TestIncidentSectionBoundsAndCancellation(t *testing.T) {
	_, b := newIncidentFixture(t)
	_, manager, err := newManagementServer(api.NewClient(b.URL, testToken), Options{APIBase: b.URL})
	if err != nil {
		t.Fatal(err)
	}
	for _, maxBytes := range []int{api.MCPIncidentEvidenceMaxBytes, api.MCPIncidentSectionMaxBytes} {
		s, guard, err := manager.readIncidentSection(context.Background(), "alpha", "app-1", "fixture", maxBytes, time.Second, func(context.Context) (any, error) {
			return &guardedRead{value: map[string]string{"data": strings.Repeat("&", maxBytes)}, check: func(context.Context) error { return nil }}, nil
		})
		if err == nil || s.Error == nil || s.Error.Code != "response_too_large" || guard != nil || len(s.Result) != 0 {
			t.Fatal("oversized section was truncated or exposed")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = manager.getIncidentBundle(ctx, incidentArgs(true))
	if !errors.Is(err, context.Canceled) {
		t.Fatal("bundle ignored cancellation")
	}
}

func TestIncidentBundleLimitsAndWindows(t *testing.T) {
	f, b := newIncidentFixture(t)
	f.coverage.Since, f.coverage.WindowStart = "30m", "2026-10-17T10:30:00Z"
	f.coverage.OldestTelemetryAt = ""
	f.regressions.Since = "30m"
	f.regressions.Regressions = append(f.regressions.Regressions, f.regressions.Regressions...)
	for len(f.comparison.Routes) <= api.MCPIncidentComparisonLimit {
		row := f.comparison.Routes[0]
		row.Route = "/" + strings.Repeat("x", len(f.comparison.Routes))
		f.comparison.Routes = append(f.comparison.Routes, row)
	}
	cs, ctx := connectConfiguredTestServer(t, b, Options{APIBase: b.URL}, "2026-07-28")
	v := decodeIncidentBundle(t, callTool(t, ctx, cs, "gregale_get_incident_bundle", incidentArgs(true), false))
	var regressions regressionsView
	var comparison deploymentComparisonView
	var coverage coverageView
	_ = json.Unmarshal(v.Regressions.Result, &regressions)
	_ = json.Unmarshal(v.Comparison.Result, &comparison)
	_ = json.Unmarshal(v.Coverage.Result, &coverage)
	if v.Partial || !coverage.RetentionClamped || !regressions.RetentionClamped || regressions.Since != "30m" || regressions.ReturnedCount != api.MCPIncidentRegressionLimit || !regressions.RegressionsTruncated || comparison.ReturnedRouteCount != api.MCPIncidentComparisonLimit || !comparison.RoutesTruncated || comparison.RetentionClamped {
		t.Fatal("bundle changed per-component bounds, windows or truncation")
	}
}

func TestIncidentBundleSectionTimeouts(t *testing.T) {
	for _, phase := range []string{"coverage_read", "final_guard"} {
		t.Run(phase, func(t *testing.T) {
			f, b := newIncidentFixture(t)
			if phase == "coverage_read" {
				f.blocked = "/v1/apps/alpha/debug/coverage"
			} else {
				f.onRead = func(path string) {
					if path == "/v1/apps/alpha/debug/compare" {
						f.blocked = "/v1/deployments/" + discoveryDeploymentID
					}
				}
			}
			cs, ctx := connectConfiguredTestServer(t, b, Options{APIBase: b.URL, Timeout: time.Second}, "2026-07-28")
			v := decodeIncidentBundle(t, callTool(t, ctx, cs, "gregale_get_incident_bundle", incidentArgs(true), false))
			section := v.Coverage
			if phase == "final_guard" {
				section = v.Regressions
			}
			if !v.Partial || section.Status != "error" || section.Error.Code != "timeout" || len(section.Result) != 0 || v.Evidence.Status != "ok" {
				t.Fatal("section timeout leaked evidence or discarded valid request")
			}
		})
	}
}

func TestIncidentBundleInFlightCancellation(t *testing.T) {
	f, b := newIncidentFixture(t)
	f.blocked = "/v1/apps/alpha/debug/requests/row-1/evidence"
	f.entered = make(chan struct{}, 1)
	_, manager, err := newManagementServer(api.NewClient(b.URL, testToken), Options{APIBase: b.URL})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := manager.getIncidentBundle(ctx, incidentArgs(true)); done <- err }()
	select {
	case <-f.entered:
	case <-time.After(time.Second):
		t.Fatal("required read did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("in-flight cancellation lost")
		}
	case <-time.After(time.Second):
		t.Fatal("bundle did not cancel native read")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, path := range f.reads {
		if strings.HasSuffix(path, "/coverage") || strings.HasSuffix(path, "/regressions") || strings.HasSuffix(path, "/compare") {
			t.Fatal("cancelled bundle collected later sections")
		}
	}
}

func TestIncidentBundleFinalAppCheckBudget(t *testing.T) {
	f, b := newIncidentFixture(t)
	f.blocked = "/v1/apps/alpha"
	_, manager, err := newManagementServer(api.NewClient(b.URL, testToken), Options{APIBase: b.URL})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	err = manager.checkIncidentBundle(ctx, incidentArgs(false), &incidentBundleView{AppID: "app-1"}, nil, 100*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
		t.Fatal("final app check exhausted the whole tool deadline instead of its reserved budget")
	}
}
