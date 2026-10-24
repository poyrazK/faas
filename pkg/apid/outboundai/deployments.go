package outboundai

import (
	"math"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

// ListDeployments lists an app's AI calls by deployment over the retention
// window, latest call first (ADR 20261022153155).
func (s *Service) ListDeployments(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.Host.Store().(state.OutboundAIDeploymentStatsStore)
	if !ok {
		problem(w, http.StatusServiceUnavailable, "outbound_ai_unavailable", "AI calls by deployment are unavailable")
		return
	}
	since := time.Now().UTC().AddDate(0, 0, -api.OutboundAIDeploymentStatsRetentionDays)
	rows, err := store.ListOutboundAIDeploymentStats(r.Context(), app.ID, since, api.OutboundAIDeploymentsListMax)
	if err != nil {
		problem(w, http.StatusServiceUnavailable, "outbound_ai_unavailable", "AI calls by deployment are unavailable")
		return
	}
	out := api.AIDeploymentUsageList{Items: make([]api.AIDeploymentUsage, 0, len(rows))}
	for _, row := range rows {
		out.Items = append(out.Items, deploymentUsage(row))
	}
	httpkit.WriteJSON(w, http.StatusOK, out)
}

func deploymentUsage(row state.OutboundAIDeploymentStats) api.AIDeploymentUsage {
	out := api.AIDeploymentUsage{
		DeploymentID: row.DeploymentID, FirstCallAt: row.FirstMinute.UTC().Format(time.RFC3339),
		LastCallAt: row.LastMinute.UTC().Format(time.RFC3339), Calls: row.Calls, ErrorCalls: row.ErrorCalls,
		FlaggedCalls: row.FlaggedCalls, CostMillicents: row.CostMillicents, InputTokens: row.InputTokens,
		OutputTokens: row.OutputTokens,
	}
	if row.Calls > 0 {
		out.ErrorRate = math.Round(float64(row.ErrorCalls)/float64(row.Calls)*10_000) / 10_000
		out.FlaggedRate = math.Round(float64(row.FlaggedCalls)/float64(row.Calls)*10_000) / 10_000
		out.CostPerCallMillicents = (row.CostMillicents + row.Calls/2) / row.Calls
	}
	if row.TTFBCalls > 0 {
		mean := (row.TTFBMSTotal + row.TTFBCalls/2) / row.TTFBCalls
		out.MeanTTFBMS = &mean
	}
	return out
}
