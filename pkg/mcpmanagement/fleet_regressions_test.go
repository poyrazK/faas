// adr: 20261017224029
package mcpmanagement

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

type fleetTestApp struct {
	id              string
	errorCode       string
	native          api.DebugRegressionsResponse
	status          int
	blocked         bool
	metadataBlocked bool
}

type fleetRegressionFixture struct {
	mu               sync.Mutex
	apps             map[string]*fleetTestApp
	deployments      map[string]api.DeploymentResponse
	requests         []string
	reads            map[string]int
	writes           int
	expectedSince    string
	onRegressionRead func(string)
	entered          chan string
	cancelled        chan string
}

func newFleetRegressionFixture(t *testing.T) (*fleetRegressionFixture, *httptest.Server) {
	t.Helper()
	f := &fleetRegressionFixture{apps: map[string]*fleetTestApp{}, deployments: map[string]api.DeploymentResponse{}, reads: map[string]int{}, expectedSince: "1h", entered: make(chan string, 8), cancelled: make(chan string, 8)}
	for i, slug := range []string{"alpha", "beta", "gamma", "delta", "echo"} {
		id := fmt.Sprintf("%08d-1111-4111-8111-111111111111", i+1)
		native := regressionsFixture()
		for j := range native.Regressions {
			native.Regressions[j].DeploymentID = id
		}
		f.apps[slug] = &fleetTestApp{id: "app-" + slug, native: native, errorCode: "native_fleet_error"}
		f.deployments[id] = api.DeploymentResponse{ID: id, AppID: "app-" + slug, Scope: "staging"}
	}
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, status, blocked := f.response(t, r)
		if blocked != "" {
			f.entered <- blocked
			<-r.Context().Done()
			f.cancelled <- blocked
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(data)
	}))
	t.Cleanup(b.Close)
	return f, b
}

func (f *fleetRegressionFixture) response(t *testing.T, r *http.Request) ([]byte, int, string) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r.URL.Path)
	if r.Method != http.MethodGet {
		f.writes++
		return nil, http.StatusMethodNotAllowed, ""
	}
	var value any
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) == 3 && parts[0] == "v1" && parts[1] == "deployments" {
		value = f.deployments[parts[2]]
	} else if len(parts) >= 3 && parts[0] == "v1" && parts[1] == "apps" && f.apps[parts[2]] != nil {
		slug := parts[2]
		app := f.apps[slug]
		if len(parts) == 3 {
			if app.metadataBlocked {
				return nil, 0, slug
			}
			value = api.AppResponse{ID: app.id, Slug: slug}
		} else if len(parts) == 5 && parts[3] == "debug" && parts[4] == "regressions" {
			f.reads[slug]++
			if r.URL.Query().Get("since") != f.expectedSince || len(r.URL.Query()) != boolCount(f.expectedSince != "") {
				t.Error("fleet lookback changed or per-app projection limit was forwarded")
			}
			if app.blocked {
				return nil, 0, slug
			}
			if app.status != 0 {
				problem := api.NewProblem(app.status, app.errorCode, "PRIVATE_PROBLEM_TITLE", "PRIVATE_PROBLEM_DETAIL "+testToken)
				retry := int64(7)
				problem.RetryAfterSeconds = &retry
				data, _ := json.Marshal(problem)
				return data, app.status, ""
			}
			value = app.native
			if f.onRegressionRead != nil {
				f.onRegressionRead(slug)
			}
		}
	}
	if value == nil {
		t.Errorf("unexpected fleet read %s", r.URL.Path)
		return nil, http.StatusNotFound, ""
	}
	data, _ := json.Marshal(value)
	return data, http.StatusOK, ""
}

func boolCount(value bool) int {
	if value {
		return 1
	}
	return 0
}

func fleetTestConsent() *EnvironmentConsent {
	return &EnvironmentConsent{RequestReadApps: map[string]string{"alpha": "app-alpha", "beta": "app-beta", "gamma": "app-gamma", "delta": "app-delta", "echo": "app-echo"}}
}

func callFleetRegressions(t *testing.T, ctx context.Context, cs *mcp.ClientSession, args fleetRegressionsArgs) fleetRegressionsView {
	t.Helper()
	out := callTool(t, ctx, cs, "gregale_list_fleet_regressions", args, false)
	data, _ := json.Marshal(out)
	var result fleetRegressionsView
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	for _, excluded := range []string{"PRIVATE_", "excluded-", "fp_test_", `"query"`} {
		if strings.Contains(string(data), excluded) {
			t.Fatal("fleet result released excluded diagnostic/error data")
		}
	}
	if len(data) > maxResultBytes {
		t.Fatal("fleet result exceeded the shared response bound")
	}
	return result
}

func TestFleetRegressionsPreservePerAppEvidence(t *testing.T) {
	for _, version := range []string{"2026-07-28", "2025-11-25"} {
		t.Run(version, func(t *testing.T) {
			f, b := newFleetRegressionFixture(t)
			f.apps["alpha"].native.Since = "30m"
			f.apps["beta"].native.Regressions = nil
			cs, ctx := connectConfiguredTestServer(t, b, Options{APIBase: b.URL, Environments: fleetTestConsent()}, version)
			catalog, err := cs.ListTools(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, tool := range catalog.Tools {
				if tool.Name == "gregale_list_fleet_regressions" {
					found = true
					if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint || tool.Annotations.DestructiveHint == nil || *tool.Annotations.DestructiveHint {
						t.Fatal("fleet tool did not advertise read-only behavior")
					}
				}
			}
			if !found {
				t.Fatal("fleet tool was absent from discovery")
			}
			result := callFleetRegressions(t, ctx, cs, fleetRegressionsArgs{Apps: []string{"beta", "alpha"}, Since: "1h", LimitPerApp: 2})
			if result.Scope != "selected_apps" || result.Status != "ok" || result.Partial || result.SuccessfulAppCount != 2 || result.FailedAppCount != 0 || result.SourceCompleteness != "unknown" || result.RequestedSince != "1h" || len(result.Apps) != 2 {
				t.Fatalf("fleet envelope=%+v", result)
			}
			beta, alpha := result.Apps[0], result.Apps[1]
			if beta.App != "beta" || beta.Result.Status != "no_visible_regressions" || beta.Result.Regressions == nil || beta.Error != nil || alpha.App != "alpha" || alpha.Result.AppID != "app-alpha" || alpha.Result.Since != "30m" || !alpha.Result.RetentionClamped {
				t.Fatal("fleet lost selection order or per-app native lookback/visibility")
			}
			rows := alpha.Result.Regressions
			if len(rows) != 2 || !alpha.Result.RegressionsTruncated || alpha.Result.NativeCount != 3 || alpha.Result.ReturnedCount != 2 || rows[0].Kind != "errors" || rows[1].State != "acknowledged" || rows[0].DeploymentScope != "staging" || !rows[0].RouteModified || rows[0].Factor != "1.00" || rows[0].SuspectedDependency.Basis != "hypothesis" {
				t.Fatal("fleet changed native ordering, guards, truncation or projection semantics")
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.reads["alpha"] != 1 || f.reads["beta"] != 1 || f.writes != 0 {
				t.Fatal("fleet fetched extra diagnostics or wrote native state")
			}
		})
	}
}

func TestFleetRegressionsSelectionValidationBeforeReads(t *testing.T) {
	f, b := newFleetRegressionFixture(t)
	cs, ctx := connectConfiguredTestServer(t, b, Options{APIBase: b.URL}, "2026-07-28")
	for _, args := range []fleetRegressionsArgs{
		{Apps: []string{}}, {Apps: []string{"alpha", "alpha"}},
		{Apps: []string{"alpha", "beta", "gamma", "delta", "echo", "future"}},
		{Apps: []string{"alpha", "../beta"}}, {Apps: []string{"alpha", ""}},
		{Apps: []string{"alpha"}, Since: "invalid"}, {Apps: []string{"alpha"}, Since: "-1h"},
		{Apps: []string{"alpha"}, LimitPerApp: -1}, {Apps: []string{"alpha"}, LimitPerApp: api.MCPFleetRegressionsMaxPerApp + 1},
	} {
		callTool(t, ctx, cs, "gregale_list_fleet_regressions", args, true)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.requests) != 0 {
		t.Fatal("invalid batch performed native reads")
	}
}

func TestFleetRegressionsPartialAndUnavailable(t *testing.T) {
	for _, mode := range []string{"denied", "all_denied", "app_denied", "replaced", "native_error", "native_error_without_code", "foreign", "widened", "timeout", "default"} {
		t.Run(mode, func(t *testing.T) {
			f, b := newFleetRegressionFixture(t)
			opts := Options{APIBase: b.URL, Environments: fleetTestConsent()}
			want := "request_access_denied"
			switch mode {
			case "denied", "all_denied":
				delete(opts.Environments.RequestReadApps, "alpha")
				if mode == "all_denied" {
					delete(opts.Environments.RequestReadApps, "beta")
				}
			case "app_denied":
				opts.AllowedApps = map[string]string{"alpha": "old-app", "beta": "app-beta"}
				opts.Environments.RequestReadApps = map[string]string{"alpha": "old-app", "beta": "app-beta"}
				want = "app_access_denied"
			case "replaced":
				opts.Environments.RequestReadApps["alpha"] = "old-app"
			case "native_error":
				f.apps["alpha"].status = http.StatusServiceUnavailable
				want = "native_fleet_error"
			case "native_error_without_code":
				f.apps["alpha"].status = http.StatusServiceUnavailable
				f.apps["alpha"].errorCode = ""
				want = "http_error"
			case "foreign":
				dep := f.deployments[f.apps["alpha"].native.Regressions[0].DeploymentID]
				dep.AppID = "app-beta"
				f.deployments[dep.ID] = dep
				want = "invalid_response"
			case "widened":
				f.apps["alpha"].native.Since = "2h"
				want = "invalid_response"
			case "timeout":
				f.apps["alpha"].blocked = true
				opts.Timeout = 600 * time.Millisecond
				want = "timeout"
			case "default":
				f.expectedSince = ""
			}
			cs, ctx := connectConfiguredTestServer(t, b, opts, "2026-07-28")
			since := "1h"
			if mode == "default" {
				since = ""
			}
			result := callFleetRegressions(t, ctx, cs, fleetRegressionsArgs{Apps: []string{"alpha", "beta"}, Since: since})
			if result.LimitPerApp != api.MCPFleetRegressionsDefaultPerApp || result.RequestedSince != "1h" {
				t.Fatal("fleet defaults changed")
			}
			if mode == "default" {
				if result.Status != "ok" || result.SuccessfulAppCount != 2 {
					t.Fatal("default lookback failed")
				}
				return
			}
			if !result.Partial || result.Apps[0].Result != nil || result.Apps[0].Error == nil || result.Apps[0].Error.Code != want {
				t.Fatalf("missing isolated app failure: status=%s partial=%v error=%+v", result.Status, result.Partial, result.Apps[0].Error)
			}
			if mode == "all_denied" {
				if result.Status != "unavailable" || result.SuccessfulAppCount != 0 || result.FailedAppCount != 2 || result.Apps[1].Result != nil {
					t.Fatal("all-app failure became successful empty evidence")
				}
			} else if result.Status != "partial" || result.SuccessfulAppCount != 1 || result.FailedAppCount != 1 || result.Apps[1].Result == nil {
				t.Fatal("one failed app discarded the other app's valid evidence")
			}
			if mode == "native_error" && (result.Apps[0].Error.Status != 503 || result.Apps[0].Error.RetryAfterSeconds == nil || *result.Apps[0].Error.RetryAfterSeconds != 7) {
				t.Fatal("native status/retry metadata was lost")
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if (mode == "denied" || mode == "all_denied" || mode == "app_denied" || mode == "replaced") && f.reads["alpha"] != 0 || mode == "all_denied" && f.reads["beta"] != 0 || f.writes != 0 {
				t.Fatal("batch bypassed diagnostic consent or made native writes")
			}
		})
	}
}

func TestFleetRegressionsFinalGuardAfterLaterAppRead(t *testing.T) {
	for _, mode := range []string{"app", "deployment", "scope", "local_app", "final_timeout"} {
		t.Run(mode, func(t *testing.T) {
			f, b := newFleetRegressionFixture(t)
			f.onRegressionRead = func(slug string) {
				if slug != "beta" {
					return
				}
				dep := f.deployments[f.apps["alpha"].native.Regressions[0].DeploymentID]
				switch mode {
				case "app", "local_app":
					f.apps["alpha"].id = "replacement"
				case "deployment":
					dep.AppID = "other-app"
				case "scope":
					dep.Scope = "production"
				case "final_timeout":
					f.apps["alpha"].metadataBlocked = true
				}
				f.deployments[dep.ID] = dep
			}
			opts := Options{APIBase: b.URL, Environments: fleetTestConsent()}
			if mode == "local_app" {
				opts.Environments = nil
			}
			if mode == "final_timeout" {
				opts.Timeout = 600 * time.Millisecond
			}
			cs, ctx := connectConfiguredTestServer(t, b, opts, "2026-07-28")
			result := callFleetRegressions(t, ctx, cs, fleetRegressionsArgs{Apps: []string{"alpha", "beta"}, Since: "1h"})
			if result.Status != "partial" || result.Apps[0].Result != nil || result.Apps[0].Error == nil || result.Apps[1].Result == nil || result.SuccessfulAppCount != 1 || result.FailedAppCount != 1 {
				t.Fatal("final batch guard released stale/cross-app evidence or discarded valid results")
			}
			if mode == "final_timeout" && result.Apps[0].Error.Code != "timeout" {
				t.Fatal("timed-out final check released collected evidence")
			}
		})
	}
}

func TestFleetRegressionsProjectionAndByteBounds(t *testing.T) {
	f, b := newFleetRegressionFixture(t)
	apps := []string{"alpha", "beta", "gamma", "delta", "echo"}
	for _, slug := range apps {
		row := f.apps[slug].native.Regressions[0]
		row.Route = "/" + strings.Repeat("<", api.MCPRequestFieldMaxBytes)
		row.Kind, row.State = strings.Repeat("<", api.MCPRequestFieldMaxBytes), strings.Repeat("<", api.MCPRequestFieldMaxBytes)
		row.SuspectedDependency.Name = strings.Repeat("<", api.MCPRequestFieldMaxBytes)
		f.apps[slug].native.Regressions = make([]api.DebugRegressionItem, api.MCPFleetRegressionsMaxPerApp+1)
		for i := range f.apps[slug].native.Regressions {
			f.apps[slug].native.Regressions[i] = row
		}
		f.apps[slug].native.Regressions[api.MCPFleetRegressionsMaxPerApp].DeploymentID = "excluded-unprojected"
	}
	cs, ctx := connectConfiguredTestServer(t, b, Options{APIBase: b.URL}, "2026-07-28")
	result := callFleetRegressions(t, ctx, cs, fleetRegressionsArgs{Apps: apps, Since: "1h", LimitPerApp: api.MCPFleetRegressionsMaxPerApp})
	if result.Status != "ok" || result.Partial || result.SuccessfulAppCount != len(apps) {
		t.Fatal("bounded native prefixes failed")
	}
	for _, app := range result.Apps {
		data, _ := json.Marshal(app.Result)
		if !app.ResponseSizeTruncated || !app.Result.RegressionsTruncated || app.Result.NativeCount != api.MCPFleetRegressionsMaxPerApp+1 || app.Result.ReturnedCount != len(app.Result.Regressions) || len(data) > api.MCPFleetRegressionAppResultMaxBytes || app.Result.ReturnedCount >= api.MCPFleetRegressionsMaxPerApp {
			t.Fatal("fleet did not preserve and disclose its bounded native prefix")
		}
	}
}

func TestFleetRegressionsGlobalCancellation(t *testing.T) {
	f, b := newFleetRegressionFixture(t)
	f.apps["alpha"].blocked = true
	_, manager, err := newManagementServer(api.NewClient(b.URL, testToken), Options{APIBase: b.URL})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		_, err := manager.listFleetRegressions(ctx, fleetRegressionsArgs{Apps: []string{"alpha", "beta"}, Since: "1h"})
		finished <- err
	}()
	select {
	case <-f.entered:
	case <-time.After(time.Second):
		t.Fatal("fleet did not start selected app read")
	}
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled fleet call did not finish")
	}
	select {
	case <-f.cancelled:
	case <-time.After(time.Second):
		t.Fatal("cancelled fleet call left a native read in flight")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.reads["beta"] != 0 || f.writes != 0 {
		t.Fatal("cancelled call continued into other apps or made a write")
	}
}
