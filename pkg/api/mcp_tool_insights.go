package api

import (
	"context"
	"net/url"
	"time"
)

// MCPToolInsightsResponse is GET /v1/apps/{slug}/mcp-tool-insights
// (ADR-20261123110137): per-tool calls, failures, argument mismatches and
// estimated compute over the last hour, from the same telemetry as the
// dashboard's MCP operations page.
type MCPToolInsightsResponse struct {
	App string `json:"app"`
	// Window is the trailing window every figure covers.
	Window     string    `json:"window"`
	ObservedAt time.Time `json:"observed_at"`
	// Telemetry is "reporting" or "not_observed" (no MCP telemetry in the
	// window; tools is empty).
	Telemetry string     `json:"telemetry"`
	LastEvent *time.Time `json:"last_event,omitempty"`
	// MemoryMB is the app's plan RAM; compute is estimated at MemoryMB + 8.
	MemoryMB int              `json:"memory_mb"`
	Tools    []MCPToolInsight `json:"tools"`
}

// MCPToolInsight is one tool's row. Counts are Prometheus increases and may
// be fractional. EstimatedMillicents is an upper bound at the overage rate:
// usage inside the plan's included GB-hours costs nothing extra.
type MCPToolInsight struct {
	Tool                string   `json:"tool"`
	Calls               float64  `json:"calls"`
	Errors              float64  `json:"errors"`
	Cancellations       float64  `json:"cancellations"`
	Mismatches          float64  `json:"mismatches"`
	ExecutionSeconds    float64  `json:"execution_seconds"`
	GBSeconds           float64  `json:"gb_seconds"`
	EstimatedMillicents int64    `json:"estimated_millicents"`
	P50MS               *float64 `json:"p50_ms,omitempty"`
	P95MS               *float64 `json:"p95_ms,omitempty"`
	P99MS               *float64 `json:"p99_ms,omitempty"`
}

const (
	MCPToolInsightsTelemetryReporting   = "reporting"
	MCPToolInsightsTelemetryNotObserved = "not_observed"
)

// GetMCPToolInsights reads an app's per-tool MCP insights.
func (c *Client) GetMCPToolInsights(ctx context.Context, slug string) (MCPToolInsightsResponse, error) {
	var out MCPToolInsightsResponse
	return out, c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(slug)+"/mcp-tool-insights", nil, &out)
}
