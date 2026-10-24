package dashboard

// adr: 20261022173833 — the dashboard pages render resolved widgets as
// cards, unavailable widgets with their reason, and series as sparklines.

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCustomDashboardPagesRender(t *testing.T) {
	p95 := 182.5
	data := NewCustomDashboardData(api.DashboardData{ID: "d-1", Name: "checkout", AsOf: time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC), Widgets: []api.DashboardWidgetValue{
		{DashboardWidget: api.DashboardWidget{Title: "Checkout p95", Kind: api.DashboardWidgetAppMetric, App: "shop", Window: "1h"}, Value: &p95, Unit: "ms"},
		{DashboardWidget: api.DashboardWidget{Title: "Queue", Kind: api.DashboardWidgetCustomMetric, App: "shop", Window: "1h"}, Value: &p95, Unit: "gauge",
			Series: []api.DashboardPoint{{Value: 1}, {Value: 3}, {Value: 2}}},
		{DashboardWidget: api.DashboardWidget{Title: "Budget", Kind: api.DashboardWidgetSLO, App: "shop"}, Error: "no SLO data yet"},
	}})
	if data.Cards[1].Sparkline != "0.0,30.0 60.0,2.0 120.0,16.0" || data.Cards[0].Sparkline != "" {
		t.Fatalf("sparklines = %q / %q", data.Cards[0].Sparkline, data.Cards[1].Sparkline)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	w := httptest.NewRecorder()
	if err := Render(w, log, "nonce", Page{Title: "Dashboard", Body: "custom_dashboard", Data: data}); err != nil {
		t.Fatalf("render dashboard: %v", err)
	}
	for _, s := range []string{"<h1>checkout</h1>", "Checkout p95", "182.5", "shop · app metric · 1h", "<polyline points=", "Unavailable: no SLO data yet"} {
		if !strings.Contains(w.Body.String(), s) {
			t.Errorf("dashboard page missing %q", s)
		}
	}
	w = httptest.NewRecorder()
	list := CustomDashboardsData{Enabled: true, Dashboards: []api.Dashboard{{ID: "d-1", Name: "checkout", UpdatedAt: time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)}}}
	if err := Render(w, log, "nonce", Page{Title: "Dashboards", Body: "custom_dashboards", Data: list}); err != nil {
		t.Fatalf("render list: %v", err)
	}
	if !strings.Contains(w.Body.String(), `href="/dashboard/dashboards/d-1"`) {
		t.Errorf("list page missing the dashboard link:\n%s", w.Body.String())
	}
}
