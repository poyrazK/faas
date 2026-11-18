package insights

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// DefaultFunnelWindow is a funnel's conversion window when the request sets
// none.
const DefaultFunnelWindow = 7 * 24 * time.Hour

// FunnelQuery is a validated-on-run funnel request for one environment.
type FunnelQuery struct {
	EnvironmentID string
	Request       api.InsightFunnelRequest
	Range         Range
}

func funnelWindow(s string) (time.Duration, error) {
	if s == "" {
		return DefaultFunnelWindow, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < api.AnalyticsFunnelMinWindow || d > api.AnalyticsFunnelMaxWindow {
		return 0, invalid("window is a duration from %s to %s", api.AnalyticsFunnelMinWindow, api.AnalyticsFunnelMaxWindow)
	}
	return d, nil
}

type funnelUnit struct {
	ref       state.AnalyticsUnitRef
	first     time.Time
	prev      time.Time
	breakdown string
}

// Funnel counts the units that completed each step in order within the
// window of their first step (ADR 20261117084505 §3).
func Funnel(ctx context.Context, store Store, q FunnelQuery) (api.InsightFunnel, error) {
	req := q.Request
	if n := len(req.Steps); n < api.AnalyticsFunnelMinSteps || n > api.AnalyticsFunnelMaxSteps {
		return api.InsightFunnel{}, invalid("a funnel has %d to %d steps", api.AnalyticsFunnelMinSteps, api.AnalyticsFunnelMaxSteps)
	}
	for _, s := range req.Steps {
		if err := validStep(s); err != nil {
			return api.InsightFunnel{}, err
		}
	}
	window, err := funnelWindow(req.Window)
	if err != nil {
		return api.InsightFunnel{}, err
	}
	unit, err := Unit(req.Unit)
	if err != nil {
		return api.InsightFunnel{}, err
	}
	by, err := ParseBreakdown(req.By)
	if err != nil {
		return api.InsightFunnel{}, err
	}
	first := req.Steps[0]
	entries, err := store.AnalyticsEntries(ctx, state.AnalyticsMatch{EnvironmentID: q.EnvironmentID, Name: first.Event,
		From: q.Range.From, Until: q.Range.Until, Properties: first.Where}, unit, api.AnalyticsQueryMaxUnits+1)
	if err != nil {
		return api.InsightFunnel{}, err
	}
	out := api.InsightFunnel{Unit: unit, Window: window.String(), By: byString(by), From: q.Range.From, Until: q.Range.Until}
	if len(entries) > api.AnalyticsQueryMaxUnits {
		entries, out.Truncated = entries[:api.AnalyticsQueryMaxUnits], true
	}
	breakdowns, err := unitBreakdowns(ctx, store, q.EnvironmentID, unit, by, entries, q.Range)
	if err != nil {
		return api.InsightFunnel{}, err
	}
	alive := make([]*funnelUnit, 0, len(entries))
	for _, e := range entries {
		alive = append(alive, &funnelUnit{ref: e.Unit, first: e.At, prev: e.At, breakdown: breakdowns[e.Unit.Key()]})
	}
	// counts[step][breakdown] is the units that completed the step.
	counts := make([]map[string]int64, len(req.Steps))
	tally := func(step int) {
		counts[step] = map[string]int64{}
		for _, u := range alive {
			counts[step][u.breakdown]++
		}
	}
	tally(0)
	for i := 1; i < len(req.Steps) && len(alive) > 0; i++ {
		steps := make([]state.AnalyticsStep, 0, len(alive))
		for _, u := range alive {
			steps = append(steps, state.AnalyticsStep{Unit: u.ref, After: u.prev, Deadline: u.first.Add(window)})
		}
		s := req.Steps[i]
		found, err := store.AnalyticsNextSteps(ctx, state.AnalyticsMatch{EnvironmentID: q.EnvironmentID, Name: s.Event, Properties: s.Where}, unit, steps)
		if err != nil {
			return api.InsightFunnel{}, err
		}
		next := alive[:0]
		for _, u := range alive {
			if at, ok := found[u.ref.Key()]; ok {
				u.prev = at
				next = append(next, u)
			}
		}
		alive = next
		tally(i)
	}
	for i := range counts {
		if counts[i] == nil {
			counts[i] = map[string]int64{}
		}
	}
	out.Steps = funnelSteps(req.Steps, counts, func(m map[string]int64) int64 {
		var n int64
		for _, c := range m {
			n += c
		}
		return n
	})
	if by.Kind == "" {
		return out, nil
	}
	top := topValues(counts[0])
	merged := make([]map[string]int64, len(counts))
	for i, m := range counts {
		merged[i] = map[string]int64{}
		for v, c := range m {
			merged[i][top[v]] += c
		}
	}
	values := make([]string, 0, len(merged[0]))
	for v := range merged[0] {
		values = append(values, v)
	}
	slices.SortFunc(values, func(a, b string) int {
		if merged[0][a] != merged[0][b] {
			if merged[0][a] > merged[0][b] {
				return -1
			}
			return 1
		}
		return strings.Compare(a, b)
	})
	for _, v := range values {
		out.Breakdowns = append(out.Breakdowns, api.InsightFunnelBreakdown{Value: v,
			Steps: funnelSteps(req.Steps, merged, func(m map[string]int64) int64 { return m[v] })})
	}
	return out, nil
}

func funnelSteps(steps []api.InsightStepRequest, counts []map[string]int64, pick func(map[string]int64) int64) []api.InsightFunnelStep {
	out := make([]api.InsightFunnelStep, 0, len(steps))
	var firstN, prevN int64
	for i, s := range steps {
		n := pick(counts[i])
		st := api.InsightFunnelStep{Event: s.Event, Units: n}
		if i == 0 {
			firstN, prevN = n, n
		}
		if firstN > 0 {
			st.ConversionFromFirst = float64(n) / float64(firstN)
		}
		if prevN > 0 {
			st.ConversionFromPrev = float64(n) / float64(prevN)
		}
		prevN = n
		out = append(out, st)
	}
	return out
}
