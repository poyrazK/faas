package apidserver

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/mcpstats"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

// getMCPSellerStats serves a sold MCP server's seller stats
// (ADR-20261126082046); the logic is pkg/apid/mcpstats.
func (s *server) getMCPSellerStats(w http.ResponseWriter, r *http.Request, acct state.Account) {
	months := 1
	if raw := r.URL.Query().Get("months"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > api.MCPSellerStatsMaxMonths {
			api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Invalid months",
				"months must be a whole number from 1 to "+strconv.Itoa(api.MCPSellerStatsMaxMonths)).
				WithLimit(api.MCPSellerStatsMaxMonths, int64(n)).WithDocs("https://gregale.dev/docs/api-monetization#seller-stats"))
			return
		}
		months = n
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.store.(mcpstats.Store)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("seller stats"))
		return
	}
	quote := func(ctx context.Context, accountID, appID, consumerID string, since, until time.Time) (billing.APIConsumerUsageQuote, error) {
		return s.quoteConsumerUsageWithBreakdown(ctx, accountID, appID, consumerID, since, until, true)
	}
	resp, err := mcpstats.Read(r.Context(), store, quote, mcpstats.App{Slug: app.Slug, ID: app.ID, AccountID: app.AccountID}, months, time.Now())
	if err != nil {
		s.log.Error("mcp seller stats", "err", err, "app_id", app.ID)
		api.WriteProblem(w, api.ErrCapacity("seller stats"))
		return
	}
	writeJSON(w, http.StatusOK, resp)
}
