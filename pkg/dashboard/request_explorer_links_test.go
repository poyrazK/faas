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
				Routes: []dashboard.RequestAnalyticsRouteView{{
					Route: "/checkout", Method: "GET", Requests: 3,
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
		"Inspect requests",
		"/dashboard/apps/demo/debug?route=%2Fcheckout&amp;since=24h",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\n--- body ---\n%s", want, body)
		}
	}
}
