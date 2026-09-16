// slo_account.go — Issue #696 / ADR-082 dashboard follow-up PR:
// the per-account SLO view, mirrored from slo_app.go so the
// per-app and per-account view types live in their own
// files (the same one-type-per-file convention
// dashboard.go uses for every other view struct).
//
// `AccountSLOView` is the dashboard-local mirror of
// api.AccountSLOResponse. The fields are scalar sums/rates
// across the account; per-app drill-down is served by the
// existing per-app metric surface. The handler
// (cmd/apid/handlers_dashboard.go::fetchDashboardAccountSLO)
// is the only thing that materialises this struct from
// the wire types.
package views

import (
	"html/template"

	"github.com/onebox-faas/faas/pkg/appmetrics"
)

// AccountSLOView is the per-account SLO panel rendered on
// the /dashboard/account page. Mirrors api.AccountSLOResponse
// field-for-field except for AppID/AppSlug (the rollup is
// account-wide).
//
// Window is the echoed SLO window (1h / 24h / 7d). Source is
// the same "prometheus" / "degraded: <reason>" vocabulary the
// per-app view uses. The latency sub-shape is the same
// SLOLatencyView the per-app view uses. The sparkline slices
// are the time-bucketed fleet-wide series — the same
// LatencySparklineView / appmetrics.SparklinePoint shapes the
// per-app view uses; the difference is the PromQL had no
// `app=…` label filter when the source data was emitted.
type AccountSLOView struct {
	Window                string
	Source                string
	AsOf                  string
	RequestDuration       SLOLatencyView
	ErrorRatePct          float64
	ColdBootRatePct       float64
	InstanceHours         float64
	GBHours               float64
	WakeQueueP95MS        float64
	WakeQueueSampleStatus string
	RequestsTotal         int64
	ThrottledTotal        int64

	LatencySparkline      LatencySparklineView
	LatencySparklineHTML  template.HTML
	ErrorSparkline        []appmetrics.SparklinePoint
	ErrorSparklineHTML    template.HTML
	ColdBootSparkline     []appmetrics.SparklinePoint
	ColdBootSparklineHTML template.HTML
	Step                  string
}
