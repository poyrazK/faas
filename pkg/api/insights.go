package api

import "time"

// Product analytics (ADR 20261117084505): event series, flag breakdowns,
// funnels and retention over track() events, under
// /v1/projects/{slug}/environments/{environment}/insights.

// InsightsNone is the breakdown value of a unit or event without the
// property or flag; InsightsOther collects values past the breakdown bound.
const (
	InsightsNone  = "(none)"
	InsightsOther = "(other)"
)

// InsightEventPoint is one time bucket and breakdown value of an event
// series. Units counts distinct customers or subjects; for (other) it sums
// the merged values' units, which may count a unit more than once.
type InsightEventPoint struct {
	Start     time.Time `json:"start"`
	Breakdown string    `json:"breakdown,omitempty"`
	Events    int64     `json:"events"`
	Units     int64     `json:"units"`
	Value     int64     `json:"value"`
}

// InsightEventSeries is GET .../insights/events.
type InsightEventSeries struct {
	Event    string              `json:"event"`
	Unit     string              `json:"unit"`
	Interval string              `json:"interval"`
	By       string              `json:"by,omitempty"`
	From     time.Time           `json:"from"`
	Until    time.Time           `json:"until"`
	Points   []InsightEventPoint `json:"points"`
}

// InsightStepRequest is one funnel step: an event name and property values
// its events must carry.
type InsightStepRequest struct {
	Event string            `json:"event"`
	Where map[string]string `json:"where,omitempty"`
}

// InsightFunnelRequest is POST .../insights/funnel. Window is the time from a
// unit's first step to its last (Go duration, 1h to 720h; default 168h).
// Unit is customer (default) or subject; By is flag:KEY or property:KEY.
type InsightFunnelRequest struct {
	Steps  []InsightStepRequest `json:"steps"`
	From   *time.Time           `json:"from,omitempty"`
	Until  *time.Time           `json:"until,omitempty"`
	Window string               `json:"window,omitempty"`
	Unit   string               `json:"unit,omitempty"`
	By     string               `json:"by,omitempty"`
}

// InsightFunnelStep is one step's units and conversion.
type InsightFunnelStep struct {
	Event               string  `json:"event"`
	Units               int64   `json:"units"`
	ConversionFromFirst float64 `json:"conversion_from_first"`
	ConversionFromPrev  float64 `json:"conversion_from_previous"`
}

// InsightFunnelBreakdown is a funnel for one breakdown value.
type InsightFunnelBreakdown struct {
	Value string              `json:"value"`
	Steps []InsightFunnelStep `json:"steps"`
}

// InsightFunnel is a funnel's result. Truncated says the query read
// AnalyticsQueryMaxUnits units and left the rest out.
type InsightFunnel struct {
	Unit       string                   `json:"unit"`
	Window     string                   `json:"window"`
	By         string                   `json:"by,omitempty"`
	From       time.Time                `json:"from"`
	Until      time.Time                `json:"until"`
	Steps      []InsightFunnelStep      `json:"steps"`
	Breakdowns []InsightFunnelBreakdown `json:"breakdowns,omitempty"`
	Truncated  bool                     `json:"truncated"`
}

// InsightRetentionRequest is POST .../insights/retention: units whose first
// start event falls in a period's cohort, and whether they sent the return
// event in each following period. Period is day (default) or week.
type InsightRetentionRequest struct {
	Start   InsightStepRequest `json:"start"`
	Return  InsightStepRequest `json:"return"`
	From    *time.Time         `json:"from,omitempty"`
	Until   *time.Time         `json:"until,omitempty"`
	Period  string             `json:"period,omitempty"`
	Periods int                `json:"periods,omitempty"`
	Unit    string             `json:"unit,omitempty"`
	By      string             `json:"by,omitempty"`
}

// InsightRetentionRow is a cohort or a breakdown value. Returned[k-1] and
// Rates[k-1] are period k; Eligible[k-1] counts the units whose period k has
// ended, the denominator of Rates. A period that has not ended for any unit
// is left out.
type InsightRetentionRow struct {
	Start    *time.Time `json:"start,omitempty"`
	Value    string     `json:"value,omitempty"`
	Units    int64      `json:"units"`
	Eligible []int64    `json:"eligible"`
	Returned []int64    `json:"returned"`
	Rates    []float64  `json:"rates"`
}

// InsightRetention is a retention query's result.
type InsightRetention struct {
	Unit       string                `json:"unit"`
	Period     string                `json:"period"`
	Periods    int                   `json:"periods"`
	By         string                `json:"by,omitempty"`
	From       time.Time             `json:"from"`
	Until      time.Time             `json:"until"`
	Overall    InsightRetentionRow   `json:"overall"`
	Cohorts    []InsightRetentionRow `json:"cohorts"`
	Breakdowns []InsightRetentionRow `json:"breakdowns,omitempty"`
	Truncated  bool                  `json:"truncated"`
}

// InsightCohortRequest creates a cohort (ADR 20261119102741 §3). Kind is
// did (Event sent at least MinCount times, default 1), did_not (customers
// active in the last 30 days that did not send Event) or funnel (Steps
// completed within Window); all look back WithinDays.
type InsightCohortRequest struct {
	Key        string               `json:"key"`
	Kind       string               `json:"kind"`
	Event      string               `json:"event,omitempty"`
	Where      map[string]string    `json:"where,omitempty"`
	WithinDays int                  `json:"within_days"`
	MinCount   int                  `json:"min_count,omitempty"`
	Steps      []InsightStepRequest `json:"steps,omitempty"`
	Window     string               `json:"window,omitempty"`
}

// InsightCohort is a cohort and its last refresh. Rules target it with the
// gregale.cohort attribute.
type InsightCohort struct {
	InsightCohortRequest
	Members            int        `json:"members"`
	Truncated          bool       `json:"truncated"`
	MaxMembers         int        `json:"max_members"`
	RefreshedAt        *time.Time `json:"refreshed_at,omitempty"`
	RefreshRequestedAt time.Time  `json:"refresh_requested_at"`
	RefreshError       string     `json:"refresh_error,omitempty"`
	CreatedBy          string     `json:"created_by"`
	CreatedAt          time.Time  `json:"created_at"`
}

// InsightCohortList is an environment's cohorts.
type InsightCohortList struct {
	Cohorts []InsightCohort `json:"cohorts"`
}

// InsightRequestVariant is request telemetry for one value of a flag.
type InsightRequestVariant struct {
	Value           string  `json:"value"`
	Requests        int64   `json:"requests"`
	ServerErrors    int64   `json:"server_errors"`
	ServerErrorRate float64 `json:"server_error_rate"`
	P95MS           int64   `json:"p95_ms"`
}

// InsightRequests is GET .../insights/requests: requests by flag value, from
// request telemetry and its retention.
type InsightRequests struct {
	Flag     string                  `json:"flag"`
	From     time.Time               `json:"from"`
	Until    time.Time               `json:"until"`
	Variants []InsightRequestVariant `json:"variants"`
}
