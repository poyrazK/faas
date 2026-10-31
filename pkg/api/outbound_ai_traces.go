package api

import "time"

// AI traces (ADR 20261020202035): the calls of one agent task, named by
// X-Gregale-AI-Trace or else by the trace ID of a W3C traceparent header.

// OutboundAITraceHeader names a call's trace. Its value is one ID with the
// characters and length of an end-user ID.
const OutboundAITraceHeader = "X-Gregale-AI-Trace"

// ValidOutboundAITraceID reports a trace ID the gateway accepts.
func ValidOutboundAITraceID(id string) bool { return ValidOutboundAIUserID(id) }

// ValidOutboundAITraceLimits reports trace limits that can be stored: each
// absent, or within its bound on an integration with an AI provider.
func ValidOutboundAITraceLimits(provider string, calls, cost, repeats *int64) bool {
	within := func(value *int64, maximum int64) bool {
		return value == nil || (provider != "" && *value >= 1 && *value <= maximum)
	}
	return within(calls, MaxOutboundAITraceCalls) && within(cost, MaxOutboundAICostMillicents) &&
		within(repeats, MaxOutboundAITraceRepeatedToolCalls)
}

// OutboundAITrace is one trace's totals. AppID and EndUser are its first
// call's; RepeatedToolCalls is its current streak of identical tool
// requests.
type OutboundAITrace struct {
	TraceID           string    `json:"trace_id"`
	AppID             *string   `json:"app_id"`
	EndUser           *string   `json:"end_user"`
	FirstAt           time.Time `json:"first_at"`
	LastAt            time.Time `json:"last_at"`
	Calls             int64     `json:"calls"`
	ErrorCalls        int64     `json:"error_calls"`
	UsageUnknownCalls int64     `json:"usage_unknown_calls"`
	InputTokens       int64     `json:"input_tokens"`
	OutputTokens      int64     `json:"output_tokens"`
	CostMillicents    int64     `json:"cost_millicents"`
	ToolCalls         int64     `json:"tool_calls"`
	RepeatedToolCalls int64     `json:"repeated_tool_calls"`
	Models            []string  `json:"models"`
}

// OutboundAITraceList pages traces newest first; pass NextBefore to
// continue.
type OutboundAITraceList struct {
	Items      []OutboundAITrace `json:"items"`
	NextBefore *time.Time        `json:"next_before"`
}

// OutboundAITraceCall is one logged call of a trace, in order.
type OutboundAITraceCall struct {
	ID                string    `json:"id"`
	CreatedAt         time.Time `json:"created_at"`
	Caller            string    `json:"caller"`
	Model             string    `json:"model"`
	Status            int       `json:"status"`
	Outcome           string    `json:"outcome"`
	InputTokens       int64     `json:"input_tokens"`
	OutputTokens      int64     `json:"output_tokens"`
	CostMillicents    int64     `json:"cost_millicents"`
	DurationMS        int64     `json:"duration_ms"`
	TimeToFirstByteMS *int64    `json:"ttfb_ms"`
	ToolCalls         []string  `json:"tool_calls"`
}

// OutboundAITraceResponse is one trace with its logged calls, oldest first.
// CallsTruncated reports a trace with more logged calls than were returned.
type OutboundAITraceResponse struct {
	Trace          OutboundAITrace       `json:"trace"`
	Calls          []OutboundAITraceCall `json:"calls"`
	CallsTruncated bool                  `json:"calls_truncated"`
}
