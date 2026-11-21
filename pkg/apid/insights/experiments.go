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
//
// Subject units (ADR 20261119173702 §1) match analytics events by subject
// hash, and also convert on a conversion metric: the metric event at or
// after first exposure, with no deadline. The result is keyed by unit.
func ExperimentConversions(ctx context.Context, store Store, x state.FlagExperiment, units []state.FlagExperimentUnitRef) (map[string]time.Time, error) {
	out := map[string]time.Time{}
	if len(units) == 0 {
		return out, nil
	}
	kind := state.AnalyticsUnitCustomer
	ref := func(u state.FlagExperimentUnitRef) state.AnalyticsUnitRef {
		return state.AnalyticsUnitRef{CustomerID: u.CustomerID}
	}
	if x.SubjectUnits() {
		kind = state.AnalyticsUnitSubject
		ref = func(u state.FlagExperimentUnitRef) state.AnalyticsUnitRef {
			return state.AnalyticsUnitRef{SubjectHash: u.SubjectHash}
		}
	}
	// byKey maps an analytics unit key back to the experiment unit.
	byKey := make(map[string]state.FlagExperimentUnitRef, len(units))
	for _, u := range units {
		byKey[ref(u).Key()] = u
	}
	def := x.MetricDefinition
	horizon, start := def.Horizon(x.MetricKind), def.StartOffset(x.MetricKind)
	if !flags.AnalyticsMetric(x.MetricKind) {
		horizon = 100 * 365 * 24 * time.Hour
	}
	steps := []flags.MetricStep{{Event: x.MetricEvent}}
	switch x.MetricKind {
	case flags.ExperimentMetricRetention:
		steps = []flags.MetricStep{def.PrimaryEvent(x.MetricKind)}
	case flags.ExperimentMetricFunnel:
		steps = def.Steps
	}
	reached := map[string]time.Time{}
	deadline := map[string]time.Time{}
	for key, u := range byKey {
		reached[key] = u.FirstExposedAt.Add(start)
		deadline[key] = u.FirstExposedAt.Add(horizon)
	}
	for _, step := range steps {
		in := make([]state.AnalyticsStep, 0, len(reached))
		for key, after := range reached {
			in = append(in, state.AnalyticsStep{Unit: ref(byKey[key]), After: after, Deadline: deadline[key]})
		}
		if len(in) == 0 {
			return out, nil
		}
		found, err := store.AnalyticsNextSteps(ctx, state.AnalyticsMatch{EnvironmentID: x.EnvironmentID, Name: step.Event, Properties: step.Where}, kind, in)
		if err != nil {
			return nil, err
		}
		reached = found
	}
	for key, at := range reached {
		out[byKey[key].CustomerID] = at
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
