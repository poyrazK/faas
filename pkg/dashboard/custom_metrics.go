package dashboard

import (
	"html/template"
	"time"
)

// CustomMetricsData is the /dashboard/apps/{slug}/custom-metrics page
// (ADR-745): each pushed metric with its latest value and a 24h sparkline.
type CustomMetricsData struct {
	Enabled    bool
	AppSlug    string
	MaxMetrics int
	Metrics    []CustomMetricRow
}

// CustomMetricRow is one metric. Spark is pre-rendered SVG; Note explains a
// missing chart (stale pusher, unavailable telemetry).
type CustomMetricRow struct {
	Name       string
	Value      float64
	ObservedAt time.Time
	Fresh      bool
	Spark      template.HTML
	Note       string
}
