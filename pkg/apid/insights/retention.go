package insights

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/flags"
	"github.com/onebox-faas/faas/pkg/state"
)

// DefaultRetentionPeriods is a retention query's periods when the request
// sets none.
const DefaultRetentionPeriods = 7

// RetentionQuery is a retention request for one environment, evaluated at
// Now: a period counts only for units whose period has ended by then.
type RetentionQuery struct {
	EnvironmentID string
	Request       api.InsightRetentionRequest
	Range         Range
	Now           time.Time
}

// periodStart is the UTC start of the day or ISO week holding t.
func periodStart(t time.Time, week bool) time.Time {
	t = t.UTC()
	d := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	if week {
		d = d.AddDate(0, 0, -((int(d.Weekday()) + 6) % 7))
	}
	return d
}

type retentionTally struct {
	units              int64
	eligible, returned []int64
}

func (t *retentionTally) row(periods int) api.InsightRetentionRow {
	r := api.InsightRetentionRow{Units: t.units, Eligible: []int64{}, Returned: []int64{}, Rates: []float64{}}
	for k := 0; k < periods && t.eligible[k] > 0; k++ {
		r.Eligible = append(r.Eligible, t.eligible[k])
		r.Returned = append(r.Returned, t.returned[k])
		r.Rates = append(r.Rates, float64(t.returned[k])/float64(t.eligible[k]))
	}
	return r
}

// Retention counts, per cohort of first start events, the units that sent
// the return event in each following period (ADR 20261117084505 §3).
func Retention(ctx context.Context, store Store, q RetentionQuery) (api.InsightRetention, error) {
	req := q.Request
	if err := validStep(req.Start); err != nil {
		return api.InsightRetention{}, err
	}
	if err := validStep(req.Return); err != nil {
		return api.InsightRetention{}, err
	}
	unit, err := Unit(req.Unit)
	if err != nil {
		return api.InsightRetention{}, err
	}
	by, err := ParseBreakdown(req.By)
	if err != nil {
		return api.InsightRetention{}, err
	}
	week := false
	period := 24 * time.Hour
	switch req.Period {
	case "", "day":
		req.Period = "day"
	case "week":
		week, period = true, 7*24*time.Hour
	default:
		return api.InsightRetention{}, invalid("period is day or week")
	}
	periods := req.Periods
	if periods == 0 {
		periods = DefaultRetentionPeriods
	}
	if periods < 1 || periods > api.AnalyticsRetentionMaxPeriods {
		return api.InsightRetention{}, invalid("periods is 1 to %d", api.AnalyticsRetentionMaxPeriods)
	}
	entries, err := store.AnalyticsEntries(ctx, state.AnalyticsMatch{EnvironmentID: q.EnvironmentID, Name: req.Start.Event,
		From: q.Range.From, Until: q.Range.Until, Properties: req.Start.Where}, unit, api.AnalyticsQueryMaxUnits+1)
	if err != nil {
		return api.InsightRetention{}, err
	}
	out := api.InsightRetention{Unit: unit, Period: req.Period, Periods: periods, By: byString(by), From: q.Range.From, Until: q.Range.Until}
	if len(entries) > api.AnalyticsQueryMaxUnits {
		entries, out.Truncated = entries[:api.AnalyticsQueryMaxUnits], true
	}
	breakdowns, err := unitBreakdowns(ctx, store, q.EnvironmentID, unit, by, entries, q.Range)
	if err != nil {
		return api.InsightRetention{}, err
	}
	weight := map[string]int64{}
	for _, v := range breakdowns {
		weight[v]++
	}
	top := topValues(weight)
	units := make([]state.AnalyticsCohortUnit, 0, len(entries))
	for _, e := range entries {
		units = append(units, state.AnalyticsCohortUnit{Unit: e.Unit, FirstAt: e.At, Cohort: periodStart(e.At, week), Breakdown: top[breakdowns[e.Unit.Key()]]})
	}
	returns, err := store.AnalyticsReturns(ctx, state.AnalyticsMatch{EnvironmentID: q.EnvironmentID, Name: req.Return.Event, Properties: req.Return.Where},
		unit, units, period, periods)
	if err != nil {
		return api.InsightRetention{}, err
	}
	newTally := func() *retentionTally {
		return &retentionTally{eligible: make([]int64, periods), returned: make([]int64, periods)}
	}
	overall := newTally()
	cohorts := map[time.Time]*retentionTally{}
	values := map[string]*retentionTally{}
	ended := func(cohort time.Time, k int) bool { return !cohort.Add(time.Duration(k+1) * period).After(q.Now) }
	for _, u := range units {
		for _, t := range []*retentionTally{overall, tallyFor(cohorts, u.Cohort, newTally), tallyFor(values, u.Breakdown, newTally)} {
			t.units++
			for k := 1; k <= periods; k++ {
				if ended(u.Cohort, k) {
					t.eligible[k-1]++
				}
			}
		}
	}
	for _, r := range returns {
		if r.Period < 1 || r.Period > periods || !ended(r.Cohort, r.Period) {
			continue
		}
		for _, t := range []*retentionTally{overall, cohorts[r.Cohort], values[r.Breakdown]} {
			if t != nil {
				t.returned[r.Period-1] += r.Units
			}
		}
	}
	out.Overall = overall.row(periods)
	starts := make([]time.Time, 0, len(cohorts))
	for s := range cohorts {
		starts = append(starts, s)
	}
	slices.SortFunc(starts, func(a, b time.Time) int { return a.Compare(b) })
	out.Cohorts = make([]api.InsightRetentionRow, 0, len(starts))
	for _, s := range starts {
		row := cohorts[s].row(periods)
		start := s
		row.Start = &start
		out.Cohorts = append(out.Cohorts, row)
	}
	if by.Kind == "" {
		return out, nil
	}
	names := make([]string, 0, len(values))
	for v := range values {
		names = append(names, v)
	}
	slices.SortFunc(names, func(a, b string) int {
		if values[a].units != values[b].units {
			if values[a].units > values[b].units {
				return -1
			}
			return 1
		}
		return strings.Compare(a, b)
	})
	for _, v := range names {
		row := values[v].row(periods)
		row.Value = v
		out.Breakdowns = append(out.Breakdowns, row)
	}
	return out, nil
}

func tallyFor[K comparable](m map[K]*retentionTally, k K, mk func() *retentionTally) *retentionTally {
	t := m[k]
	if t == nil {
		t = mk()
		m[k] = t
	}
	return t
}

// RequestsQuery reads request telemetry by flag value.
type RequestsQuery struct {
	AccountID, ProjectID, EnvironmentSlug string
	Flag                                  string
	Range                                 Range
}

// Requests reports requests, server errors and p95 latency per value of one
// flag, from request telemetry.
func Requests(ctx context.Context, store Store, q RequestsQuery) (api.InsightRequests, error) {
	if !flags.ValidKey(q.Flag) {
		return api.InsightRequests{}, invalid("flag is a flag key")
	}
	rows, err := store.AnalyticsRequestsByFlag(ctx, state.AnalyticsRequestsQuery{AccountID: q.AccountID, ProjectID: q.ProjectID,
		EnvironmentSlug: q.EnvironmentSlug, Flag: q.Flag, From: q.Range.From, Until: q.Range.Until})
	if err != nil {
		return api.InsightRequests{}, err
	}
	type acc struct {
		requests, errors int64
		hist             map[int]int64
	}
	byValue := map[string]*acc{}
	weight := map[string]int64{}
	for _, r := range rows {
		weight[r.Value] += r.Requests
	}
	top := topValues(weight)
	for _, r := range rows {
		v := top[r.Value]
		a := byValue[v]
		if a == nil {
			a = &acc{hist: map[int]int64{}}
			byValue[v] = a
		}
		a.requests += r.Requests
		a.errors += r.Errors
		a.hist[r.Bucket] += r.Requests
	}
	out := api.InsightRequests{Flag: q.Flag, From: q.Range.From, Until: q.Range.Until, Variants: []api.InsightRequestVariant{}}
	for v, a := range byValue {
		iv := api.InsightRequestVariant{Value: v, Requests: a.requests, ServerErrors: a.errors, P95MS: flags.HistogramQuantile(a.hist, 0.95)}
		if a.requests > 0 {
			iv.ServerErrorRate = float64(a.errors) / float64(a.requests)
		}
		out.Variants = append(out.Variants, iv)
	}
	slices.SortFunc(out.Variants, func(a, b api.InsightRequestVariant) int {
		if a.Requests != b.Requests {
			if a.Requests > b.Requests {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Value, b.Value)
	})
	return out, nil
}
