package dashboard

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// CustomDashboardsData is the /dashboard/dashboards page
// (ADR-20261022173833): the account's saved dashboards.
type CustomDashboardsData struct {
	Enabled    bool
	Error      string
	Dashboards []api.Dashboard
}

// CustomDashboardData is the /dashboard/dashboards/{id} page: every widget
// resolved into a card.
type CustomDashboardData struct {
	Enabled bool
	Error   string
	ID      string
	Name    string
	AsOf    string
	Cards   []CustomDashboardCard
}

// CustomDashboardCard is one widget ready to render.
type CustomDashboardCard struct {
	Title, Subtitle, Value, Unit, Detail, Error string
	// Sparkline is SVG polyline points in a 120×32 box; empty without a series.
	Sparkline string
}

// NewCustomDashboardData turns resolved widgets into cards.
func NewCustomDashboardData(data api.DashboardData) CustomDashboardData {
	out := CustomDashboardData{Enabled: true, ID: data.ID, Name: data.Name, AsOf: data.AsOf.UTC().Format("2006-01-02 15:04:05 UTC")}
	for _, w := range data.Widgets {
		card := CustomDashboardCard{Title: w.Title, Unit: w.Unit, Detail: w.Detail, Error: w.Error, Subtitle: w.App + " · " + strings.ReplaceAll(w.Kind, "_", " ")}
		if w.Window != "" {
			card.Subtitle += " · " + w.Window
		}
		if w.Value != nil {
			card.Value = strconv.FormatFloat(*w.Value, 'f', -1, 64)
			if len(card.Value) > 10 {
				card.Value = strconv.FormatFloat(*w.Value, 'g', 6, 64)
			}
		}
		card.Sparkline = sparkline(w.Series)
		out.Cards = append(out.Cards, card)
	}
	return out
}

// sparkline scales points into a 120×32 polyline; flat series sit mid-height.
func sparkline(points []api.DashboardPoint) string {
	if len(points) < 2 {
		return ""
	}
	lo, hi := points[0].Value, points[0].Value
	for _, p := range points {
		lo, hi = min(lo, p.Value), max(hi, p.Value)
	}
	parts := make([]string, 0, len(points))
	for i, p := range points {
		x := float64(i) * 120 / float64(len(points)-1)
		y := 16.0
		if hi > lo {
			y = 30 - (p.Value-lo)/(hi-lo)*28
		}
		parts = append(parts, fmt.Sprintf("%.1f,%.1f", x, y))
	}
	return strings.Join(parts, " ")
}
