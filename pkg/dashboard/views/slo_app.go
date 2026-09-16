// Package views holds the dashboard-facing typed view structs
// used by the html/template renderer. The package exists to
// isolate the dashboard from pkg/api — the same package-isolation
// rule that drives dashboard.go::DeploymentDetailData (the
// grype scan payload is mirrored here rather than imported from
// pkg/api, so a future maintainer can swap the dashboard's
// rendering engine without touching the public API surface).
//
// Issue #696 / ADR-082 follow-up PR (dashboard card): AppSLOView
// and AccountSLOView are the dashboard-local mirrors of
// api.AppSLOResponse / api.AccountSLOResponse. The handler
// (cmd/apid/handlers_dashboard.go::fetchDashboardSLO) is the
// only thing that materialises these structs from the wire
// types.
//
// The SparklinePoint slice types are sourced from pkg/appmetrics
// (the type is the same instance the time-bucketed forecast
// library produces). The view package deliberately re-uses
// the appmetrics type rather than re-declaring it so the
// data path (pkg/appmetrics.FetchRange) and the view struct
// agree on the wire shape without a translation hop.
package views

import (
	"html/template"
	"time"

	"github.com/onebox-faas/faas/pkg/appmetrics"
)

// SLOLatencyView is the dashboard-facing slice of api.SLODuration.
// Three percentiles over the SLO window (2xx class only). NaN/Inf
// from histogram_quantile on an empty window is coerced to 0 by
// the handler (mirrors pkg/appmetrics.SafeFloat).
type SLOLatencyView struct {
	P50MS float64
	P95MS float64
	P99MS float64
}

// AppSLOView is the per-app SLO panel rendered on the
// /dashboard/apps/{slug} page (issue #696 / ADR-082). The
// fields mirror api.AppSLOResponse so the customer toggling
// between the dashboard and the GET /v1/apps/{slug}/slo
// surface sees the same numbers.
//
// Source is the same "prometheus" / "degraded: <reason>"
// vocabulary the public /status/slo.json and the per-app
// Metrics card use — the empty-state branch is one shared path.
//
// SparklinePoint slices are the time-bucketed series the
// inline-SVG renderer (sparkline.go) consumes. Rendered as
// inline <svg> in the template — no JS dependency. Empty
// slices render the empty-state badge the same shape the
// Source "degraded:" branch uses.
type AppSLOView struct {
	Window                string         // echoed window, e.g. "24h"
	Source                string         // "prometheus" / "degraded: <reason>"
	AsOf                  string         // RFC3339Nano UTC
	RequestDuration       SLOLatencyView // p50/p95/p99 latency
	ErrorRatePct          float64
	ColdBootRatePct       float64
	InstanceHours         float64
	GBHours               float64
	WakeQueueP95MS        float64
	WakeQueueSampleStatus string
	RequestsTotal         int64
	ThrottledTotal        int64

	// LatencySparkline is the triple of percentile series
	// the renderer draws as a 3-line sparkline. Each sub-slice
	// may be empty (the metric was missing in the window or
	// the query failed) — the renderer drops the absent line
	// and labels the gap.
	LatencySparkline LatencySparklineView
	// LatencySparklineHTML is the pre-rendered inline SVG
	// the template embeds. The handler renders the SVG once
	// at the data-loader edge so the template stays a pure
	// renderer (no template.FuncMap wiring required). Empty
	// string means "no data — render the empty-state badge".
	LatencySparklineHTML template.HTML
	// ErrorSparkline is the time-bucketed error-rate series
	// rendered as a filled-area sparkline.
	ErrorSparkline []appmetrics.SparklinePoint
	// ErrorSparklineHTML is the pre-rendered SVG for the
	// error-rate row.
	ErrorSparklineHTML template.HTML
	// ColdBootSparkline is the time-bucketed cold-boot-rate
	// series, also filled-area.
	ColdBootSparkline []appmetrics.SparklinePoint
	// ColdBootSparklineHTML is the pre-rendered SVG for the
	// cold-boot-rate row.
	ColdBootSparklineHTML template.HTML
	// Step is the bucket size the Promise step param used
	// (e.g. "1m" / "15m" / "1h"). The template renders it
	// as the x-axis label.
	Step string
}

// LatencySparklineView is the triple of latency percentile
// series consumed by the dashboard's 3-line sparkline renderer.
// Each sub-slice is the per-bucket value; absent percentile
// → empty slice (the renderer drops the absent line).
type LatencySparklineView struct {
	P50 []appmetrics.SparklinePoint
	P95 []appmetrics.SparklinePoint
	P99 []appmetrics.SparklinePoint
}

// SLOBadge is the small "SLO" badge shown on the apps-list
// per-row (the per-app row in /dashboard/apps). The handler
// picks the worst-field value (p95 latency if elevated, else
// error rate) and the badge colour reflects the threshold.
// The full per-app SLO panel lives on the per-app detail
// page (the AppSLOView rendered above). nil = no badge
// (Prometheus not configured, or the row is beyond the
// apps-list badge cap).
type SLOBadge struct {
	Label     string // "p95: 87ms" / "err: 0.41%" / "—"
	Glyph     string // "ok" / "warn" / "down" — template maps to CSS class
	Source    string // "prometheus" / "degraded: <reason>"
	ScannedAt time.Time
}

// SLOStamp is the page-level helper that surfaces the
// current SLO window ("1h" / "24h" / "7d") and the
// as-of timestamp. The template uses Window to mark the
// active tab in the window-selector nav; AsOf is
// pre-formatted (RFC3339Nano UTC) at the handler edge
// so the template stays a pure renderer.
//
// The struct is intentionally tiny — three fields, no
// helpers — so a future maintainer can move the
// window-selector rendering into a separate handler
// without unwrapping a heavy view struct.
type SLOStamp struct {
	Window string // "1h" / "24h" / "7d"
	AsOf   string // RFC3339Nano UTC, pre-formatted
	// Step is the bucket size the PromQL query_range used
	// (e.g. "1m" / "15m" / "1h"). The template renders it
	// as the x-axis label on the sparklines.
	Step string
}
