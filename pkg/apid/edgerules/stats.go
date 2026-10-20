package edgerules

// ADR-960: per-rule hit counts. Gateways flush counts into hourly buckets;
// this handler totals them over a window for one app, and with
// ?series=true also returns each rule's hourly totals (ADR-20261020105001).

import (
	"net/http"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

var edgeRuleStatsWindows = map[string]time.Duration{
	"1h":  time.Hour,
	"24h": 24 * time.Hour,
	"7d":  7 * 24 * time.Hour,
}

// GET /v1/apps/{slug}/edge-rules/stats?window=1h|24h|7d (default 24h)&series=true.
func (s *Service) GetEdgeRuleStats(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	window, span, withSeries, prob := edgeRuleStatsQuery(r)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	store, ok := s.Host.Store().(state.EdgeRuleHitStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("edge-rule hit counts are unavailable"))
		return
	}
	now := time.Now().UTC()
	since := now.Add(-span).Truncate(time.Hour)
	stats, err := store.EdgeRuleHitStatsForApp(r.Context(), app.ID, since)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read edge-rule hit counts"))
		return
	}
	out := api.EdgeRuleStatsResponse{Window: window, Since: since, Rules: make([]api.EdgeRuleHitStatsResponse, 0, len(stats))}
	for _, st := range stats {
		out.Rules = append(out.Rules, api.EdgeRuleHitStatsResponse{RuleID: st.RuleID, Matched: st.Matched, Logged: st.Logged})
	}
	if withSeries {
		hits, err := store.EdgeRuleHitSeriesForApp(r.Context(), app.ID, since)
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not read edge-rule hit counts"))
			return
		}
		series := EdgeRuleHitSeries(hits, since, now)
		for i := range out.Rules {
			out.Rules[i].Series = series[out.Rules[i].RuleID]
		}
	}
	httpkit.WriteJSON(w, http.StatusOK, out)
}

// edgeRuleStatsQuery reads ?window (default 24h) and ?series.
func edgeRuleStatsQuery(r *http.Request) (window string, span time.Duration, withSeries bool, prob *api.Problem) {
	window = r.URL.Query().Get("window")
	if window == "" {
		window = "24h"
	}
	span, ok := edgeRuleStatsWindows[window]
	if !ok {
		return "", 0, false, api.ErrValidation("window must be 1h, 24h or 7d")
	}
	if raw := r.URL.Query().Get("series"); raw != "" {
		var err error
		if withSeries, err = strconv.ParseBool(raw); err != nil {
			return "", 0, false, api.ErrValidation("series must be true or false")
		}
	}
	return window, span, withSeries, nil
}

// EdgeRuleHitSeries groups hourly buckets by rule: matched plus logged per
// hour, one value for every hour from start through now's hour.
func EdgeRuleHitSeries(hits []state.EdgeRuleHit, start, now time.Time) map[string][]int64 {
	start = start.UTC().Truncate(time.Hour)
	n := int(now.UTC().Truncate(time.Hour).Sub(start)/time.Hour) + 1
	out := map[string][]int64{}
	for _, h := range hits {
		i := int(h.Bucket.UTC().Truncate(time.Hour).Sub(start) / time.Hour)
		if i < 0 || i >= n {
			continue
		}
		series := out[h.RuleID]
		if series == nil {
			series = make([]int64, n)
			out[h.RuleID] = series
		}
		series[i] += h.Hits
	}
	return out
}
