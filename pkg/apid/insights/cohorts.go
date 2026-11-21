package insights

import (
	"context"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/flags"
	"github.com/onebox-faas/faas/pkg/state"
)

// Analytics cohorts (ADR 20261119102741 §3).

// ValidateCohort checks a cohort's key and definition against the plan's
// analytics retention.
func ValidateCohort(key string, d state.AnalyticsCohortDefinition, retentionDays int) error {
	if !flags.ValidKey(key) {
		return invalid("a cohort key matches ^[a-z][a-z0-9_-]{0,63}$")
	}
	maxDays := min(retentionDays, api.AnalyticsCohortMaxDays)
	if d.WithinDays < 1 || d.WithinDays > maxDays {
		return invalid("within_days is 1 to %d on this plan", maxDays)
	}
	step := func(event string, where map[string]string) error {
		if !flags.ValidEventName(event) {
			return invalid("invalid event name %q", event)
		}
		if len(where) > api.AnalyticsEventMaxProperties {
			return invalid("at most %d property filters", api.AnalyticsEventMaxProperties)
		}
		for k, v := range where {
			if !ValidPropertyKey(k) || len(v) > api.AnalyticsPropertyValueMaxBytes {
				return invalid("invalid property filter %q", k)
			}
		}
		return nil
	}
	switch d.Kind {
	case state.CohortDid, state.CohortDidNot:
		if len(d.Steps) > 0 || d.Window != "" {
			return invalid("%s cohorts take no steps or window", d.Kind)
		}
		if d.Kind == state.CohortDidNot && d.MinCount != 0 {
			return invalid("did_not cohorts take no min_count")
		}
		if d.Kind == state.CohortDid && (d.MinCount < 0 || d.MinCount > api.AnalyticsCohortMaxMinCount) {
			return invalid("min_count is 1 to %d", api.AnalyticsCohortMaxMinCount)
		}
		return step(d.Event, d.Where)
	case state.CohortFunnel:
		if d.Event != "" || len(d.Where) > 0 || d.MinCount != 0 {
			return invalid("funnel cohorts take steps and a window")
		}
		return flagsErr(flags.ValidateMetricDefinition(flags.ExperimentMetricFunnel, flags.MetricDefinition{Steps: d.Steps, Window: d.Window}, ValidPropertyKey))
	default:
		return invalid("a cohort kind is did, did_not or funnel")
	}
}

func flagsErr(err error) error {
	if err == nil {
		return nil
	}
	return invalid("%s", err.Error())
}

// CohortMembers computes a cohort's customers as of now, at most
// maxMembers in customer order; truncated says more matched.
func CohortMembers(ctx context.Context, store Store, cohorts state.AnalyticsCohortStore, c state.AnalyticsCohort, now time.Time, maxMembers int) ([]string, bool, error) {
	if maxMembers <= 0 {
		return nil, false, nil
	}
	d := c.Definition
	since := now.AddDate(0, 0, -d.WithinDays)
	var members []string
	var err error
	switch d.Kind {
	case state.CohortDid:
		minCount := max(d.MinCount, 1)
		members, err = cohorts.AnalyticsCohortDid(ctx, state.AnalyticsMatch{EnvironmentID: c.EnvironmentID, Name: d.Event, Properties: d.Where, From: since},
			minCount, maxMembers+1)
	case state.CohortDidNot:
		members, err = cohorts.AnalyticsCohortDidNot(ctx, state.AnalyticsMatch{EnvironmentID: c.EnvironmentID, Name: d.Event, Properties: d.Where, From: since},
			now.AddDate(0, 0, -api.AnalyticsCohortDidNotActiveDays), maxMembers+1)
	case state.CohortFunnel:
		members, err = funnelCompleters(ctx, store, c, since, now)
	}
	if err != nil {
		return nil, false, err
	}
	slices.Sort(members)
	if len(members) > maxMembers {
		return members[:maxMembers], true, nil
	}
	return members, false, nil
}

// funnelCompleters lists customers that sent the funnel's first step since
// since and completed every step within the window of it.
func funnelCompleters(ctx context.Context, store Store, c state.AnalyticsCohort, since, now time.Time) ([]string, error) {
	d := c.Definition
	window := flags.MetricDefinition{Window: d.Window}.WindowDuration()
	first := d.Steps[0]
	entries, err := store.AnalyticsEntries(ctx, state.AnalyticsMatch{EnvironmentID: c.EnvironmentID, Name: first.Event, Properties: first.Where,
		From: since, Until: now}, state.AnalyticsUnitCustomer, api.AnalyticsQueryMaxUnits)
	if err != nil {
		return nil, err
	}
	reached := map[string]time.Time{}
	deadline := map[string]time.Time{}
	for _, e := range entries {
		reached[e.Unit.CustomerID] = e.At
		deadline[e.Unit.CustomerID] = e.At.Add(window)
	}
	for _, s := range d.Steps[1:] {
		if len(reached) == 0 {
			break
		}
		steps := make([]state.AnalyticsStep, 0, len(reached))
		for id, after := range reached {
			steps = append(steps, state.AnalyticsStep{Unit: state.AnalyticsUnitRef{CustomerID: id}, After: after, Deadline: deadline[id]})
		}
		if reached, err = store.AnalyticsNextSteps(ctx, state.AnalyticsMatch{EnvironmentID: c.EnvironmentID, Name: s.Event, Properties: s.Where},
			state.AnalyticsUnitCustomer, steps); err != nil {
			return nil, err
		}
	}
	out := make([]string, 0, len(reached))
	for id := range reached {
		out = append(out, id)
	}
	return out, nil
}
