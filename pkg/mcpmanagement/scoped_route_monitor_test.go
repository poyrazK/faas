package mcpmanagement

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routemonitor"
)

func TestScopedRouteMonitoringMCPRejectsCrossScopeAndMalformedReports(t *testing.T) {
	for _, failure := range []string{"none", "scope", "identity", "cohorts", "no_evidence", "status", "stale"} {
		t.Run(failure, func(t *testing.T) {
			appID := uuid.NewString()
			anchor := time.Now().Add(-time.Hour)
			budget := int64(500)
			now := time.Now()
			if failure == "stale" {
				now = now.Add(-10 * time.Minute)
			}
			r := routemonitor.NewReport(api.RouteMonitorConfig{AppID: appID, Enabled: true, Revision: 1, UpdatedAt: &anchor, Routes: []api.RouteMonitorRoute{{Method: "GET", Path: "/safe", Max5xxRateBPS: &budget}}}, now)
			r.DeploymentID = uuid.NewString()
			r.ObservationAnchor = &anchor
			for i := range r.Routes[0].Windows {
				r.Routes[0].Windows[i].Observed.Requests = 100
			}
			routemonitor.Evaluate(&r, "")
			out := api.AppOperationalSummary{AppID: appID, Scope: "default", Monitoring: api.AppOperationalMonitoring{Available: true, CheckedAt: &r.CheckedAt, Status: r.Status, DeploymentID: r.DeploymentID, Coverage: "deployment_attributed_current_windows", RouteBudgets: &api.ScopedRouteBudgetReport{Scope: "default", BudgetSource: "app_policy", Report: r}}, Recovery: api.AppOperationalRecovery{Coverage: "explicit_scope_only"}}
			switch failure {
			case "scope":
				out.Monitoring.RouteBudgets.Scope = "neighbor"
			case "identity":
				out.Monitoring.RouteBudgets.Report.AppID = uuid.NewString()
			case "cohorts":
				out.Monitoring.RouteBudgets.Report.CustomerGroupBy = "tenant"
			case "no_evidence":
				out.Monitoring.RouteBudgets = nil
			case "status":
				out.Monitoring.RouteBudgets.Report.Routes[0].Windows[0].Observed.Requests = 0
			}
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/apps/alpha" {
					_ = json.NewEncoder(w).Encode(api.AppResponse{ID: appID, Slug: "alpha"})
					return
				}
				_ = json.NewEncoder(w).Encode(out)
			}))
			defer backend.Close()
			cs, ctx := connectTestServer(t, backend, 0, "")
			result := callTool(t, ctx, cs, "gregale_get_operational_summary", map[string]any{"app": "alpha"}, failure != "none")
			if failure != "none" && string(result["code"]) != `"invalid_response"` {
				t.Fatal("unsafe budget evidence reached agent")
			}
		})
	}
}
