package dashboard

// AI gateway pages (ADR 20261022145950). pkg/apid/aiconsole fills these
// views; every value is formatted there, since the templates have no
// helper functions.

// AIGatewayData is /dashboard/ai: the account's AI integrations with
// today's usage.
type AIGatewayData struct {
	Unavailable  string
	Day          string
	Integrations []AIIntegrationRow
}

// AIIntegrationRow is one integration on the overview.
type AIIntegrationRow struct {
	Name, Provider, Href                         string
	Requests, ErrorRate, Cost, Flagged, Score    string
	RequestLog, Traces, OnlineEvaluation, Alerts bool
}

// AIIntegrationData is /dashboard/ai/{integration}: one UTC day of one
// integration.
type AIIntegrationData struct {
	Name, Provider, Href, TracesHref string
	Day, MinDay, MaxDay              string
	Unavailable                      string
	Summary                          AIUsageSummary
	Models, Callers                  []AIUsageRow
	Tags, Users                      []AISpendRow
	TagsNote, UsersNote              string
	Latency                          []AILatencyRow
	LatencyNote                      string
	Alerts                           []AIAlertRow
	Traces                           []AITraceRow
	TracesNote                       string
}

// AIUsageSummary is a day's totals and the integration's limits.
type AIUsageSummary struct {
	Requests, Errors, ErrorRate, Tokens, Cost, MonthCost string
	DailyCostLimit, MonthlyCostLimit, CacheSaved         string
}

// AIUsageRow is one model's or caller's day. Bar is its share of the
// day's cost in percent, for the bar chart.
type AIUsageRow struct {
	Name, Requests, ErrorRate, InputTokens, OutputTokens, Cost string
	Quality, Ratings, Score                                    string
	Bar                                                        int
}

// AISpendRow is one tag or end user's day.
type AISpendRow struct {
	Name, Requests, Tokens, Cost, Limit string
	Bar                                 int
}

// AILatencyRow is one model's latency and error rate over recent logged
// calls.
type AILatencyRow struct {
	Model, Calls, ErrorRate, TTFBP50, TTFBP95, DurationP50, DurationP95 string
}

// AIAlertRow is one drift alert.
type AIAlertRow struct {
	Day, Model, Metric, Current, Baseline, Counts, Detected string
}

// AITraceRow is one trace in a list.
type AITraceRow struct {
	ID, Href, LastCall, Calls, Errors, Cost, ToolCalls, Repeated, Models, Caller, EndUser string
	Looping                                                                               bool
}

// AITracesData is /dashboard/ai/{integration}/traces: the trace explorer.
type AITracesData struct {
	IntegrationName, IntegrationHref, Action string
	Unavailable                              string
	EndUser                                  string
	Apps                                     []AIOption
	Traces                                   []AITraceRow
	OlderHref, Note                          string
}

// AIOption is one choice of a select.
type AIOption struct {
	Value, Label string
	Selected     bool
}

// AITraceData is /dashboard/ai/{integration}/traces/{trace}: one trace's
// calls on a timeline.
type AITraceData struct {
	IntegrationName, IntegrationHref, TracesHref string
	Unavailable                                  string
	Trace                                        AITraceRow
	FirstCall, Span                              string
	Timeline                                     AITimeline
	Calls                                        []AITraceCallRow
	Note                                         string
}

// AITimeline is the trace's calls as SVG bars: each starts at X and is W
// wide in a Width×Height box, one row per call. TTFBX marks the first
// byte, when the call reported one.
type AITimeline struct {
	Width, Height int
	Bars          []AITimelineBar
	Ticks         []AITimelineTick
}

// AITimelineBar is one call's bar.
type AITimelineBar struct {
	X, Y, W, H, TTFBX, LabelY int
	HasTTFB, Error, Flagged   bool
	Label                     string
}

// AITimelineTick is one time mark on the timeline's axis.
type AITimelineTick struct {
	X     int
	Label string
}

// AITraceCallRow is one call in the trace's table.
type AITraceCallRow struct {
	Number, Started, Model, Status, Tokens, Cost, Duration, TTFB string
	Tools, Flags, Feedback, Score, Caller                        string
	Error                                                        bool
}
