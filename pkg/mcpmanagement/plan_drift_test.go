// adr: 20261024123000
package mcpmanagement

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestPlanDriftMCPReleaseDetails(t *testing.T) {
	for _, protocol := range []string{"2026-07-28", "2025-11-25"} {
		for _, change := range []string{"settings", "configuration", "serving", "latest", "combined"} {
			t.Run(protocol+"/"+change, func(t *testing.T) {
				f, _, _ := newReleaseFixture(t, false)
				var configChanged atomic.Bool
				b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if configChanged.Load() && r.URL.Path == "/v1/apps/alpha/release-review" {
						f.mu.Lock()
						defer f.mu.Unlock()
						_ = json.NewEncoder(w).Encode(api.AppReleaseReview{AppID: f.app.ID, ConfigurationHash: strings.Repeat("PRIVATE_CONFIG__", 4), LatestDeploymentID: f.latest, ServingDeploymentIDs: []string{f.current}})
						return
					}
					f.serveHTTP(w, r)
				}))
				t.Cleanup(b.Close)
				cs, ctx := connectConfiguredTestServer(t, b, Options{APIBase: b.URL, Token: testToken, AllowWrites: true}, protocol)
				id := planID(t, callTool(t, ctx, cs, "gregale_plan_deploy", map[string]any{"app": "alpha", "image": testImage, "reason": "explain drift"}, false))
				f.mu.Lock()
				if change == "settings" || change == "combined" {
					f.app.RAMMB++
				}
				if change == "serving" || change == "combined" {
					f.current = "release-current"
				}
				if change == "latest" || change == "combined" {
					f.latest = "release-latest"
				}
				f.mu.Unlock()
				configChanged.Store(change == "configuration" || change == "combined")
				out := callTool(t, ctx, cs, "gregale_check_plan", getActionArgs{App: "alpha", PlanID: id}, false)
				var rows []planDrift
				if json.Unmarshal(out["drift"], &rows) != nil || len(rows) == 0 || string(out["baseline_check"]) != `"changed"` || string(out["apply_eligibility"]) != `"blocked"` {
					t.Fatalf("drift missing: %s", out)
				}
				fields := map[string]planDrift{}
				for _, r := range rows {
					fields[r.Field] = r
				}
				want := map[string][]string{"settings": {"app_settings"}, "configuration": {"configuration_baseline"}, "serving": {"serving_deployment_id"}, "latest": {"latest_deployment_id"}, "combined": {"app_settings", "configuration_baseline", "serving_deployment_id", "latest_deployment_id"}}[change]
				if len(fields) != len(want) {
					t.Fatalf("unexpected drift: %+v", rows)
				}
				for _, name := range want {
					row, ok := fields[name]
					if !ok {
						t.Fatalf("missing %s", name)
					}
					if name == "app_settings" || name == "configuration_baseline" {
						if !row.ValuesOmitted || row.Expected != nil || row.Observed != nil {
							t.Fatal("configuration baseline exposed values")
						}
					} else {
						expected := "release-base"
						observed := "release-current"
						if name == "latest_deployment_id" {
							observed = "release-latest"
						}
						if row.Expected != expected || row.Observed != observed {
							t.Fatalf("bad ID comparison: %+v", row)
						}
					}
				}
				if bytes.Contains(out["drift"], []byte("PRIVATE_CONFIG")) || bytes.Contains(out["drift"], []byte("ram_mb")) {
					t.Fatal("configuration values leaked")
				}
				f.mu.Lock()
				defer f.mu.Unlock()
				if f.writes != 0 {
					t.Fatal("explaining drift executed a release")
				}
			})
		}
	}
}

func TestPlanDriftBudgetRevisionsAndRestart(t *testing.T) {
	f, b, opts := newBudgetFixture(t)
	store, path, key := testStateStore(t)
	opts.planStore, opts.planNamespace = store, "drift-restart"
	_, m, err := newManagementServer(api.NewClient(b.URL, testToken), opts)
	if err != nil {
		t.Fatal(err)
	}
	id := budgetHistoryPlan(t, m)
	other := budgetHistoryPlan(t, m)
	if _, err := m.applyRouteBudgets(t.Context(), applyArgs{PlanID: other}); err != nil {
		t.Fatal(err)
	}
	zero := int64(0)
	if _, err := f.store.SetRouteMonitor(t.Context(), f.account.ID, f.app.ID, api.SetRouteMonitorRequest{Enabled: true, ExpectedRevision: &zero, Routes: []api.RouteMonitorRoute{{Method: "GET", Path: "/checkout", MaxP95MS: 400}}}); err != nil {
		t.Fatal(err)
	}
	first := appCheck(t, m, budgetHistorySelector(id))
	if len(first.Drift) != 2 || first.Drift[0].Expected != int64(0) || first.Drift[0].Observed != int64(1) || first.Drift[1].Field != "app_budget_revision" || first.Drift[1].Observed != int64(1) {
		t.Fatalf("both policy revisions not explained: %+v", first.Drift)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := OpenStateStore(path, key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = next.Close() })
	opts.planStore = next
	_, restored, err := newManagementServer(api.NewClient(b.URL, testToken), opts)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	second := appCheck(t, restored, budgetHistorySelector(id))
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first.Drift, second.Drift) || !bytes.Equal(before, after) {
		t.Fatal("restart changed comparisons or a read persisted new state")
	}
	value, err := restored.checkPlan(t.Context(), budgetHistorySelector(id))
	if err != nil {
		t.Fatal(err)
	}
	delete(restored.opts.Environments.Named, "shop/staging")
	if value.(*guardedRead).check(t.Context()) == nil {
		t.Fatal("drift released after read consent revoked")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writes != 1 {
		t.Fatal("drift read mutated policy")
	}
}

func TestPlanDriftBudgetIntegerBoundary(t *testing.T) {
	f, _, opts := newBudgetFixture(t)
	var changed atomic.Bool
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if changed.Load() && strings.HasSuffix(r.URL.Path, "/route-budgets") {
			maxRev := api.RouteRequirementsMaxRevision
			_ = json.NewEncoder(w).Encode(api.EnvironmentRouteBudgets{AppID: f.app.ID, Scope: "staging", ProjectID: f.project.ID, EnvironmentID: f.env.ID, Revision: maxRev, AppRevision: maxRev, BudgetSource: "environment_policy", Config: api.RouteMonitorConfig{AppID: f.app.ID, Revision: maxRev, Routes: []api.RouteMonitorRoute{}}})
			return
		}
		f.serve(w, r)
	}))
	t.Cleanup(b.Close)
	opts.APIBase = b.URL
	_, m, err := newManagementServer(api.NewClient(b.URL, testToken), opts)
	if err != nil {
		t.Fatal(err)
	}
	id := budgetHistoryPlan(t, m)
	changed.Store(true)
	out := appCheck(t, m, budgetHistorySelector(id))
	encoded, err := m.safeOutput(out)
	if err != nil {
		t.Fatal(err)
	}
	data := encoded.(json.RawMessage)
	if len(out.Drift) != 2 || !bytes.Contains(data, []byte(`"expected":0`)) || !bytes.Contains(data, []byte(`"observed":9007199254740991`)) {
		t.Fatalf("policy boundary lost precision/zero: %s", data)
	}
}

func TestPlanDriftRegressionPartialAndReplay(t *testing.T) {
	for _, change := range []string{"version", "unavailable", "invalid", "attempted"} {
		t.Run(change, func(t *testing.T) {
			f, b, opts := newTriageFixture(t)
			_, m, err := newManagementServer(api.NewClient(b.URL, testToken), opts)
			if err != nil {
				t.Fatal(err)
			}
			value, err := m.planRegressionAction(t.Context(), regressionActionArgs{App: "alpha", DeploymentID: discoveryDeploymentID, Route: "GET /orders", Action: "acknowledge", Reason: "explain observation"})
			if err != nil {
				t.Fatal(err)
			}
			id := value.(releasePlan).ID
			original := f.row.Version
			if change == "attempted" {
				f.mu.Lock()
				f.lost = true
				f.mu.Unlock()
				if _, err := m.applyRegressionAction(t.Context(), applyArgs{PlanID: id}); err == nil {
					t.Fatal("expected committed lost response")
				}
			} else {
				f.mu.Lock()
				switch change {
				case "version":
					f.row.Version = uuid.NewString()
				case "unavailable":
					f.oldAPI = true
				case "invalid":
					f.row.AppID = "PRIVATE_OTHER_APP"
				}
				f.mu.Unlock()
			}
			out := appCheck(t, m, getActionArgs{App: "alpha", PlanID: id})
			if change == "version" {
				if len(out.Drift) != 1 || out.Drift[0].Field != "regression_observation_version" || out.Drift[0].Expected != original || out.Drift[0].Observed != f.row.Version {
					t.Fatal("missing native observation comparison")
				}
			} else if change == "attempted" {
				if len(out.Drift) != 0 || out.ApplyEligibility != "retry_original_plan" || out.BaselineCheck != "retry_preserved" {
					t.Fatal("own committed write treated as drift")
				}
			} else if len(out.Drift) != 0 || out.ApplyEligibility != "unknown" || out.BaselineCheck != "not_verified" {
				t.Fatalf("unavailable/invalid evidence fabricated drift: %+v", out)
			}
		})
	}
}

func TestPlanDriftPromotionRedactionAndRetry(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		t.Run(map[bool]string{false: "promotion", true: "rollback"}[rollback], func(t *testing.T) {
			f, b := newEnvironmentFixture(t)
			m := newHistoryEnvironment(t, b, environmentTestOptions(b, true))
			id := planHistoryEnvironment(t, m, rollback)
			f.mu.Lock()
			f.hash = strings.Repeat("PRIVATE_PREVIEW_", 4)
			f.mu.Unlock()
			out := environmentCheck(t, m, id)
			if out.ApplyEligibility != "blocked" || out.BaselineCheck != "changed" || len(out.Drift) != 1 || out.Drift[0].Field != "promotion_baseline" || !out.Drift[0].ValuesOmitted || out.Drift[0].Expected != nil || out.Drift[0].Observed != nil {
				t.Fatalf("private preview comparison leaked or missing: %+v", out)
			}
			f.mu.Lock()
			f.hash = strings.Repeat("a", 64)
			f.ambiguous = true
			f.mu.Unlock()
			if applyHistoryEnvironment(t, m, id, rollback) == nil {
				t.Fatal("expected committed lost response")
			}
			f.mu.Lock()
			f.hash = strings.Repeat("b", 64)
			f.mu.Unlock()
			out = environmentCheck(t, m, id)
			if len(out.Drift) != 0 || out.BaselineCheck != "retry_preserved" {
				t.Fatal("ambiguous environment retry recomputed drift")
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.writes != 1 || f.reviewRequests != 0 || f.approvalPolls != 0 {
				t.Fatal("drift check performed native or approval writes")
			}
		})
	}
}

func TestPlanDriftEnvironmentFlags(t *testing.T) {
	for _, change := range []string{"allowed", "sync", "running", "rollback", "unavailable"} {
		t.Run(change, func(t *testing.T) {
			f, _ := newEnvironmentFixture(t)
			var changed atomic.Bool
			b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if changed.Load() && strings.HasSuffix(r.URL.Path, "/promotion-preview") {
					if change == "unavailable" {
						http.Error(w, "PRIVATE_UPSTREAM_DETAIL", 503)
						return
					}
					_ = json.NewEncoder(w).Encode(api.ProjectEnvironmentPromotionPreviewResponse{ProjectSlug: "shop", FromEnvironment: "staging", ToEnvironment: "production", PromotionHash: strings.Repeat("a", 64), CanPromote: change != "allowed", SyncConfig: change == "sync"})
					return
				}
				if changed.Load() && strings.HasSuffix(r.URL.Path, "/promotions/promotion-one") {
					status, rollback := "completed", ""
					if change == "running" {
						status = "running"
					}
					if change == "rollback" {
						rollback = "running"
					}
					_ = json.NewEncoder(w).Encode(api.ProjectEnvironmentPromotionStatusResponse{PromotionID: "promotion-one", ProjectSlug: "shop", FromEnvironment: "staging", ToEnvironment: "production", PromotionHash: strings.Repeat("a", 64), Status: status, RollbackStatus: rollback})
					return
				}
				f.serve(w, r)
			}))
			t.Cleanup(b.Close)
			m := newHistoryEnvironment(t, b, environmentTestOptions(b, true))
			id := planHistoryEnvironment(t, m, change == "running" || change == "rollback")
			changed.Store(true)
			out := environmentCheck(t, m, id)
			if change == "unavailable" {
				if out.ApplyEligibility != "unknown" || len(out.Drift) != 0 || out.BaselineCheck != "not_verified" {
					t.Fatalf("missing preview fabricated comparison: %+v", out)
				}
				return
			}
			field := map[string]string{"allowed": "promotion_allowed", "sync": "promotion_sync_config", "running": "promotion_running", "rollback": "rollback_started"}[change]
			expected, observed := false, true
			if change == "allowed" {
				expected, observed = true, false
			}
			if len(out.Drift) != 1 || out.Drift[0].Field != field || out.Drift[0].Expected != expected || out.Drift[0].Observed != observed {
				t.Fatalf("guard flag comparison missing: %+v", out.Drift)
			}
			encoded, err := json.Marshal(out.Drift)
			if err != nil {
				t.Fatal(err)
			}
			var decoded []struct {
				Expected *bool `json:"expected"`
				Observed *bool `json:"observed"`
			}
			if json.Unmarshal(encoded, &decoded) != nil || decoded[0].Expected == nil || decoded[0].Observed == nil {
				t.Fatal("false comparison value omitted")
			}
		})
	}
}
