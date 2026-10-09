package main

// ADR-904: per-rule hit counts. Gateways flush counts into hourly buckets;
// this handler totals them over a window for one app.

import (
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

var edgeRuleStatsWindows = map[string]time.Duration{
	"1h":  time.Hour,
	"24h": 24 * time.Hour,
	"7d":  7 * 24 * time.Hour,
}

// GET /v1/apps/{slug}/edge-rules/stats?window=1h|24h|7d (default 24h).
func (s *server) getEdgeRuleStats(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	window := r.URL.Query().Get("window")
	if window == "" {
		window = "24h"
	}
	span, ok := edgeRuleStatsWindows[window]
	if !ok {
		api.WriteProblem(w, api.ErrValidation("window must be 1h, 24h or 7d"))
		return
	}
	store, ok := s.store.(state.EdgeRuleHitStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("edge-rule hit counts are unavailable"))
		return
	}
	since := time.Now().UTC().Add(-span).Truncate(time.Hour)
	stats, err := store.EdgeRuleHitStatsForApp(r.Context(), app.ID, since)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read edge-rule hit counts"))
		return
	}
	out := api.EdgeRuleStatsResponse{Window: window, Since: since, Rules: make([]api.EdgeRuleHitStatsResponse, 0, len(stats))}
	for _, st := range stats {
		out.Rules = append(out.Rules, api.EdgeRuleHitStatsResponse{RuleID: st.RuleID, Matched: st.Matched, Logged: st.Logged})
	}
	writeJSON(w, http.StatusOK, out)
}
