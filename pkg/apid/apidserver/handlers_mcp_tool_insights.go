package apidserver

import (
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/mcpinsights"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// getMCPToolInsights serves per-tool MCP insights (ADR-20261123110137)
// behind the mcp_tool_insights flag; the logic is pkg/apid/mcpinsights.
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
	resp, err := mcpinsights.Read(r.Context(), s.promqlClient, app.Slug, app.ID, appRAM(app.RAMMB, acct.Plan), time.Now().UTC())
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("MCP tool insights"))
		return
	}
	writeJSON(w, http.StatusOK, resp)
}
