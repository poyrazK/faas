package main

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appmetrics"
	"github.com/onebox-faas/faas/pkg/slo"
	"github.com/onebox-faas/faas/pkg/state"
)

// sloStatus assembles an SLO's budget position (ADR-747): window totals from
// the meterd hourly rows, burn rates live from Prometheus. Either source
// failing degrades only its own fields, reported in Source.
func (s *server) sloStatus(ctx context.Context, def state.SLO, now time.Time) *api.SLOStatus {
	current := now.UTC().Truncate(time.Hour)
	start := current.Add(-time.Duration(def.WindowDays) * 24 * time.Hour)
	if created := def.CreatedAt.UTC().Truncate(time.Hour); created.After(start) {
		start = created
	}
	st := &api.SLOStatus{
		WindowStart:   start.Format(time.RFC3339),
		HoursExpected: int(current.Sub(start) / time.Hour),
		Source:        appmetrics.SourcePrometheus,
	}
	if budgets, ok := s.store.(state.SLOBudgetStore); ok {
		if totals, err := budgets.SumSLOHours(ctx, def.ID, start); err == nil {
			st.Good, st.Total, st.HoursRecorded = totals.Good, totals.Total, totals.Hours
			if a, ok := slo.Attainment(totals.Good, totals.Total); ok {
				st.AttainmentPct = pct(a)
			}
			if b, ok := slo.BudgetRemaining(totals.Good, totals.Total, def.ObjectiveBP); ok {
				st.BudgetRemainingPct = pct(b)
			}
		} else {
			st.Source = appmetrics.SourceDegradedPrefix + "budget history unavailable"
		}
	}
	if s.promqlClient == nil {
		st.Source = appmetrics.SourceDegradedPrefix + "prometheus not configured"
		return st
	}
	for _, w := range []struct {
		rng string
		out **float64
	}{{"1h", &st.BurnRate1h}, {"6h", &st.BurnRate6h}} {
		good, total, err := slo.Counts(ctx, s.promqlClient, def, w.rng, time.Time{})
		if err != nil {
			st.Source = appmetrics.SourceDegradedPrefix + "burn rate unavailable"
			continue
		}
		if burn, ok := slo.BurnRate(good, total, def.ObjectiveBP); ok {
			*w.out = &burn
		}
	}
	return st
}

func pct(ratio float64) *float64 {
	v := ratio * 100
	return &v
}
