package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard/views"
	"github.com/onebox-faas/faas/pkg/state"
)

// fetchDashboardCustomSLOs builds the app page's "Your SLOs" rows (ADR-747)
// under the same 3s budget as the ADR-082 panel. nil skips the section: no
// SLOs, a plan without per-app metrics, or a store that predates ADR-747.
func (s *server) fetchDashboardCustomSLOs(ctx context.Context, app state.App, acct state.Account) []views.CustomSLOView {
	store, ok := s.store.(state.SLOStore)
	if !ok || !acct.Plan.PerAppMetricsAllowed() {
		return nil
	}
	dctx, cancel := budgetCtx(ctx, 3*time.Second)
	defer cancel()
	defs, err := store.ListSLOs(dctx, app.ID)
	if err != nil || len(defs) == 0 {
		return nil
	}
	now := time.Now()
	out := make([]views.CustomSLOView, 0, len(defs))
	for _, def := range defs {
		out = append(out, customSLOView(def, s.sloStatus(dctx, def, now)))
	}
	return out
}

func customSLOView(def state.SLO, st *api.SLOStatus) views.CustomSLOView {
	resp := sloResponse(def)
	v := views.CustomSLOView{
		Name:            def.Name,
		Objective:       fmt.Sprintf("%s over %dd", describeObjective(resp), def.WindowDays),
		Attained:        "no traffic",
		BudgetRemaining: "—",
		BudgetClass:     "dim",
		BurnRate:        "—",
		History:         fmt.Sprintf("%d / %d h", st.HoursRecorded, st.HoursExpected),
	}
	if st.AttainmentPct != nil {
		v.Attained = fmt.Sprintf("%.3f%%", *st.AttainmentPct)
	}
	if b := st.BudgetRemainingPct; b != nil {
		v.BudgetRemaining = fmt.Sprintf("%.1f%%", *b)
		switch {
		case *b <= 0:
			v.BudgetClass = "bad"
		case *b < 25:
			v.BudgetClass = "warn"
		default:
			v.BudgetClass = "ok"
		}
	}
	if st.BurnRate1h != nil {
		v.BurnRate = fmt.Sprintf("%.2f× (1h)", *st.BurnRate1h)
	}
	if strings.HasPrefix(st.Source, "degraded") {
		v.Degraded = st.Source
	}
	return v
}

// describeObjective renders "99.9% available" or "99.5% under 250ms".
func describeObjective(slo api.SLOResponse) string {
	if slo.SLI == state.SLILatency {
		return fmt.Sprintf("%g%% under %dms", slo.ObjectivePct, slo.LatencyThresholdMS)
	}
	return fmt.Sprintf("%g%% available", slo.ObjectivePct)
}
