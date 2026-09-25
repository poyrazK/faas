package dashboard_test

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/dashboard"
)

func TestRender_AppDetail_RequestAnalyticsLinksToDebugger(t *testing.T) {
	rec := httptest.NewRecorder()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	page := dashboard.Page{
		Title: "demo",
		Body:  "app_detail",
		Data: dashboard.AppDetailData{
			App: dashboard.AppListItem{Slug: "demo", AppID: "demo-uuid", Status: "active"},
			RequestAnalytics: &dashboard.RequestAnalyticsView{
				GroupBy: "route", Since: "24h", Requests: 3,
				ComputeCost: &dashboard.RequestAnalyticsComputeCostView{
					EstimatedEUR: "1.23456", AllocatedEUR: "1.23456", RateEUR: "0.01000", RequestCount: 3,
				},
				DeploymentCosts: &dashboard.RequestAnalyticsDeploymentCostBreakdownView{
					EstimatedEUR: "1.23456", AllocatedEUR: "1.23456", RequestCount: 3,
					Deployments: []dashboard.RequestAnalyticsDeploymentCostView{{
						DeploymentID: "deploy-1234567890", Revision: "abcdef1234567890", Tag: "v39",
						Requests: 3, RequestSharePct: 100, EstimatedEUR: "1.23456",
					}},
				},
				Routes: []dashboard.RequestAnalyticsRouteView{{
					Route: "/checkout", Method: "GET", Requests: 3,
					EstimatedComputeCostEUR: "1.23456", RequestSharePct: 100,
					TrendURL: "/dashboard/apps/demo?analytics_method=GET&analytics_route=%2Fcheckout",
					DebugURL: "/dashboard/apps/demo/debug?route=%2Fcheckout&since=24h",
				}},
			},
		},
	}
	if err := dashboard.Render(rec, log, "", page); err != nil {
		t.Fatalf("render: %v", err)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"Top routes",
		"Estimated compute value for this window: €1.23456",
		"Est. compute",
		"€1.23456",
		"Estimated compute by deployment",
		"v39",
		"Inspect requests",
		"/dashboard/apps/demo/debug?route=%2Fcheckout&amp;since=24h",
		"?analytics_by=consumer_id",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\n--- body ---\n%s", want, body)
		}
	}
}
