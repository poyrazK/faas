package dashboard_test

// adr: 20261020161310

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/dashboard"
)

func renderRouteInsights(t *testing.T, view *dashboard.RouteInsightsView) string {
	t.Helper()
	recorder := httptest.NewRecorder()
	page := dashboard.Page{Title: "demo", Body: "app_detail", Data: dashboard.AppDetailData{
		App: dashboard.AppListItem{Slug: "demo", Status: "active"}, RouteInsights: view,
	}}
	if err := dashboard.Render(recorder, slog.New(slog.NewTextHandler(io.Discard, nil)), "insights-nonce", page); err != nil {
		t.Fatal(err)
	}
	return recorder.Body.String()
}

func TestAppDetailRouteInsightsPanel(t *testing.T) {
	body := renderRouteInsights(t, &dashboard.RouteInsightsView{
		Available: true, CSRF: "csrf-token-1", Flash: "fixed", FlashDetail: "The rule change is on the edge-rules page.",
		Active: []dashboard.RouteInsightItem{
			{ID: "ins-1", KindLabel: "Safe to cache", Method: "GET", Route: "/catalog", Message: "GET /catalog returned the same body.",
				FirstDetectedAt: "2026-10-20T06:00:00Z", FixID: "fix-abc", FixSummary: "Create an enabled cache rule for GET and HEAD /catalog."},
			{ID: "ins-2", KindLabel: "Keeps the app awake", Method: "GET", Route: "/health", Message: "GET /health keeps demo awake."},
		},
		Resolved: []dashboard.RouteInsightItem{{ID: "ins-3", KindLabel: "Cache max-age too short", Method: "GET", Route: "/news", FixedAt: "2026-10-19T08:00:00Z"}},
	})
	for _, want := range []string{
		`id="route-insights"`, "Fix applied.", "Safe to cache", "GET /catalog returned the same body.",
		`action="/dashboard/apps/demo/routes/insights/ins-1/fix"`, `name="csrf_token" value="csrf-token-1"`, `name="fix_id" value="fix-abc"`,
		"Create an enabled cache rule for GET and HEAD /catalog.", "Resolved in the last 7 days (1)", "fixed 2026-10-19T08:00:00Z", "New route insights",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in the rendered panel", want)
		}
	}
	if strings.Contains(body, "/routes/insights/ins-2/fix") {
		t.Fatal("an insight without a fix rendered an Apply fix form")
	}
}

func TestAppDetailRouteInsightsPanelStates(t *testing.T) {
	if body := renderRouteInsights(t, nil); strings.Contains(body, `id="route-insights"`) {
		t.Fatal("plans without request telemetry must not show the panel")
	}
	if body := renderRouteInsights(t, &dashboard.RouteInsightsView{}); !strings.Contains(body, "Route insights are temporarily unavailable") {
		t.Fatal("a failed read must say so")
	}
	if body := renderRouteInsights(t, &dashboard.RouteInsightsView{Available: true}); !strings.Contains(body, "No active insights.") {
		t.Fatal("an empty list must say so")
	}
	if body := renderRouteInsights(t, &dashboard.RouteInsightsView{Available: true, Flash: "changed"}); !strings.Contains(body, "The fix changed since the page loaded") {
		t.Fatal("a changed fix must be reported")
	}
}
