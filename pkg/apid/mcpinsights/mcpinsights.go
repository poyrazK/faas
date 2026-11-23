// Package mcpinsights serves GET /v1/apps/{slug}/mcp-tool-insights
// (ADR-20261123110137): per-tool MCP usage and estimated compute over the
// last hour, from the same telemetry as the dashboard's MCP operations page.
package mcpinsights

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcpprotocol"
)

// Read builds the response for an app whose ownership the caller resolved.
// No telemetry in the window is a not_observed response, not an error; an
// error means the read itself failed.
func Read(ctx context.Context, reader mcpprotocol.Reader, slug, appID string, memoryMB int, now time.Time) (api.MCPToolInsightsResponse, error) {
	resp := api.MCPToolInsightsResponse{App: slug, Window: "1h", ObservedAt: now, MemoryMB: memoryMB, Tools: []api.MCPToolInsight{}}
	snapshot, err := mcpprotocol.FetchTools(ctx, reader, appID, now)
	if errors.Is(err, mcpprotocol.ErrNotObserved) {
		resp.Telemetry = api.MCPToolInsightsTelemetryNotObserved
		return resp, nil
	}
	if err != nil {
		return api.MCPToolInsightsResponse{}, err
	}
	resp.Telemetry = api.MCPToolInsightsTelemetryReporting
	resp.LastEvent = &snapshot.LastEvent
	for _, tool := range snapshot.Tools {
		resp.Tools = append(resp.Tools, Row(tool, memoryMB))
	}
	return resp, nil
}

// Row converts one tool's telemetry; latencies become milliseconds and
// compute is estimated at memoryMB of plan RAM.
func Row(tool mcpprotocol.ToolPerformance, memoryMB int) api.MCPToolInsight {
	compute := mcpprotocol.EstimateToolCompute(tool.ExecutionSeconds, memoryMB)
	return api.MCPToolInsight{
		Tool: tool.Tool, Calls: tool.Calls, Errors: tool.Errors, Cancellations: tool.Cancellations,
		Mismatches: tool.Mismatches, ExecutionSeconds: tool.ExecutionSeconds,
		GBSeconds: compute.GBSeconds, EstimatedMillicents: compute.Millicents,
		P50MS: milliseconds(tool.P50), P95MS: milliseconds(tool.P95), P99MS: milliseconds(tool.P99),
	}
}

func milliseconds(seconds *float64) *float64 {
	if seconds == nil {
		return nil
	}
	v := *seconds * 1000
	return &v
}
