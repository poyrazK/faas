package apidserver

import (
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcpprotocol"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// getMCPToolInsights serves per-tool MCP usage and estimated compute
// (ADR-20261123110137) behind the mcp_tool_insights flag.
func (s *server) getMCPToolInsights(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.platformFlagFor(platformflags.MCPToolInsights, platformflags.Subject{AccountID: acct.ID, Plan: string(acct.Plan)}) {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "MCP tool insights unavailable", "MCP tool insights are not enabled for this account").
			WithHint("An operator releases MCP tool insights account by account. Your credentials are not the cause."))
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	if s.promqlClient == nil {
		api.WriteProblem(w, api.ErrCapacity("MCP tool insights"))
		return
	}
	now := time.Now().UTC()
	resp := api.MCPToolInsightsResponse{App: app.Slug, Window: "1h", ObservedAt: now, MemoryMB: appRAM(app.RAMMB, acct.Plan), Tools: []api.MCPToolInsight{}}
	snapshot, err := mcpprotocol.Fetch(r.Context(), s.promqlClient, app.ID, now)
	switch {
	case errors.Is(err, mcpprotocol.ErrNotObserved):
		resp.Telemetry = api.MCPToolInsightsTelemetryNotObserved
		writeJSON(w, http.StatusOK, resp)
		return
	case err != nil:
		api.WriteProblem(w, api.ErrCapacity("MCP tool insights"))
		return
	}
	resp.Telemetry = api.MCPToolInsightsTelemetryReporting
	resp.LastEvent = &snapshot.LastEvent
	for _, tool := range snapshot.Tools {
		resp.Tools = append(resp.Tools, mcpToolInsight(tool, resp.MemoryMB))
	}
	writeJSON(w, http.StatusOK, resp)
}

func mcpToolInsight(tool mcpprotocol.ToolPerformance, memoryMB int) api.MCPToolInsight {
	compute := mcpprotocol.EstimateToolCompute(tool.ExecutionSeconds, memoryMB)
	ms := func(seconds *float64) *float64 {
		if seconds == nil {
			return nil
		}
		v := *seconds * 1000
		return &v
	}
	return api.MCPToolInsight{
		Tool: tool.Tool, Calls: tool.Calls, Errors: tool.Errors, Cancellations: tool.Cancellations,
		Mismatches: tool.Mismatches, ExecutionSeconds: tool.ExecutionSeconds,
		GBSeconds: compute.GBSeconds, EstimatedMillicents: compute.Millicents,
		P50MS: ms(tool.P50), P95MS: ms(tool.P95), P99MS: ms(tool.P99),
	}
}
