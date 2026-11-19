package insights

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/flags"
	"github.com/onebox-faas/faas/pkg/state"
)

// Funnel and retention experiment conversions and CUPED pre-period values
// from analytics events (ADR 20261119102741 §1–2).

// ExperimentConversions returns, by customer, when each unit converted on a
// funnel or retention metric. A funnel unit converts at its last step: step
// 1 after its first exposure and each later step after the previous one,
// all by exposure plus the window. A retention unit converts at its first
// return event in (exposure + N·period, exposure + (N+1)·period].
func ExperimentConversions(ctx context.Context, store Store, x state.FlagExperiment, units []state.FlagExperimentUnitRef) (map[string]time.Time, error) {
	out := map[string]time.Time{}
	if len(units) == 0 {
		return out, nil
	}
	def := x.MetricDefinition
	horizon, start := def.Horizon(x.MetricKind), def.StartOffset(x.MetricKind)
	switch x.MetricKind {
	case flags.ExperimentMetricRetention:
		steps := make([]state.AnalyticsStep, 0, len(units))
		for _, u := range units {
			steps = append(steps, state.AnalyticsStep{Unit: state.AnalyticsUnitRef{CustomerID: u.CustomerID},
				After: u.FirstExposedAt.Add(start), Deadline: u.FirstExposedAt.Add(horizon)})
		}
		ret := def.PrimaryEvent(x.MetricKind)
		found, err := store.AnalyticsNextSteps(ctx, state.AnalyticsMatch{EnvironmentID: x.EnvironmentID, Name: ret.Event, Properties: ret.Where},
			state.AnalyticsUnitCustomer, steps)
		if err != nil {
			return nil, err
		}
		return found, nil
	case flags.ExperimentMetricFunnel:
		deadline := map[string]time.Time{}
		reached := map[string]time.Time{}
		for _, u := range units {
			deadline[u.CustomerID] = u.FirstExposedAt.Add(horizon)
			reached[u.CustomerID] = u.FirstExposedAt
		}
		for _, s := range def.Steps {
			steps := make([]state.AnalyticsStep, 0, len(reached))
			for id, after := range reached {
				steps = append(steps, state.AnalyticsStep{Unit: state.AnalyticsUnitRef{CustomerID: id}, After: after, Deadline: deadline[id]})
			}
			if len(steps) == 0 {
				return out, nil
			}
			found, err := store.AnalyticsNextSteps(ctx, state.AnalyticsMatch{EnvironmentID: x.EnvironmentID, Name: s.Event, Properties: s.Where},
				state.AnalyticsUnitCustomer, steps)
			if err != nil {
				return nil, err
			}
			reached = found
		}
		return reached, nil
	}
	return out, nil
}

// PreValues returns each listed customer's CUPED covariate from the
// ExperimentCupedPreDays before the experiment started: the sum of the
// metric event's values for mean metrics, otherwise 1 when it sent the
// metric's primary event and 0 when not.
func PreValues(ctx context.Context, store state.AnalyticsCohortStore, x state.FlagExperiment, customers []string) (map[string]float64, error) {
	if x.StartedAt == nil {
		return nil, nil
	}
	ev := flags.MetricStep{Event: x.MetricEvent}
	if flags.AnalyticsMetric(x.MetricKind) {
		ev = x.MetricDefinition.PrimaryEvent(x.MetricKind)
	}
	until := *x.StartedAt
	totals, err := store.AnalyticsCustomerTotals(ctx, state.AnalyticsMatch{EnvironmentID: x.EnvironmentID, Name: ev.Event, Properties: ev.Where,
		From: until.AddDate(0, 0, -api.ExperimentCupedPreDays), Until: until}, customers)
	if err != nil {
		return nil, err
	}
	out := make(map[string]float64, len(customers))
	for _, id := range customers {
		t := totals[id]
		switch {
		case x.MetricKind == flags.ExperimentMetricMean:
			out[id] = t.Value
		case t.Events > 0:
			out[id] = 1
		default:
			out[id] = 0
		}
	}
	return out, nil
}
