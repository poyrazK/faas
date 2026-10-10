package main

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard/views"
	"github.com/onebox-faas/faas/pkg/state"
)

// fetchDashboardSyntheticChecks builds the app page's "Synthetic checks"
// rows (ADR-748) under a 3s budget. nil skips the section.
func (s *server) fetchDashboardSyntheticChecks(ctx context.Context, app state.App, acct state.Account) []views.SyntheticCheckView {
	store, ok := s.store.(state.SyntheticCheckStore)
	if !ok || !acct.Plan.PerAppMetricsAllowed() {
		return nil
	}
	dctx, cancel := budgetCtx(ctx, 3*time.Second)
	defer cancel()
	checks, err := store.ListSyntheticChecks(dctx, app.ID)
	if err != nil || len(checks) == 0 {
		return nil
	}
	now := time.Now()
	out := make([]views.SyntheticCheckView, 0, len(checks))
	for _, c := range checks {
		resp := s.syntheticCheckResponse(app, c)
		out = append(out, syntheticCheckView(resp, s.syntheticCheckResults(dctx, c.ID, now)))
	}
	return out
}

func syntheticCheckView(c api.SyntheticCheckResponse, res *api.SyntheticCheckResults) views.SyntheticCheckView {
	v := views.SyntheticCheckView{
		Name: c.Name, Request: c.Method + " " + c.URL, Every: fmt.Sprintf("%d min", c.IntervalSeconds/60),
		Uptime24h: "—", P95: "—", LastResult: "not run yet", LastClass: "dim", Paused: !c.Enabled,
	}
	if res == nil || len(res.Recent) == 0 {
		return v
	}
	if res.Uptime24hPct != nil {
		v.Uptime24h = fmt.Sprintf("%.2f%%", *res.Uptime24hPct)
	}
	if res.P95LatencyMS24h > 0 {
		v.P95 = fmt.Sprintf("%.0f ms", res.P95LatencyMS24h)
	}
	last := res.Recent[0]
	if last.OK {
		v.LastResult, v.LastClass = fmt.Sprintf("ok · %d · %d ms", last.StatusCode, last.LatencyMS), "ok"
	} else {
		v.LastResult, v.LastClass = "FAIL "+last.ErrorClass, "bad"
		if last.StatusCode != 0 {
			v.LastResult += fmt.Sprintf(" · %d", last.StatusCode)
		}
	}
	return v
}
