package operatorobs

import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const AlertResponseTimesPath = "/v1/admin/obs/incidents/platform_alert/response-times"

func ResponseTimesSince(raw string, now time.Time) (time.Time, error) {
	since := now.Add(-api.ObsAlertResponseTimesWindowDefault)
	if raw != "" {
		var err error
		since, err = time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			return time.Time{}, state.ErrInvalidArgument
		}
	}
	since = since.UTC().Truncate(time.Microsecond)
	now = now.UTC().Truncate(time.Microsecond)
	if now.IsZero() || !since.Before(now) || since.Before(now.Add(-api.ObsAlertResponseTimesWindowMax)) {
		return time.Time{}, state.ErrInvalidArgument
	}
	return since, nil
}

func AlertResponseTimes(ctx context.Context, reader state.OperatorAlertResponseTimesReader, enabled bool, since, now time.Time) (api.ObsAlertResponseTimes, error) {
	out := api.ObsAlertResponseTimes{AsOf: now.UTC().Truncate(time.Microsecond), Since: since.UTC().Truncate(time.Microsecond), Scope: "retained_activation_cohort", DurationBasis: "episode_activation", PercentileMethod: "nearest_rank_whole_seconds", Limitations: []string{"retained_episodes_only_not_complete_incident_history", "activation_cohort_excludes_older_active_episodes", "resolution_is_confirmed_observation", "percentiles_exclude_unobserved_unknown_and_invalid_clock"}, Groups: []api.ObsAlertResponseGroup{}}
	if _, err := ResponseTimesSince(since.Format(time.RFC3339Nano), now); err != nil {
		return out, err
	}
	ctx, cancel := context.WithTimeout(ctx, api.ObsAlertResponseTimesBudget)
	defer cancel()
	snap, err := reader.OperatorAlertResponseTimes(ctx, state.OperatorAlertResponseTimesFilter{Since: out.Since, Now: out.AsOf, CollectionEnabled: enabled})
	if err != nil {
		return out, err
	}
	out.Collection = collectionHealth(snap.Collection, enabled, out.AsOf)
	out.Retention = retentionHealth(snap.Retention, out.AsOf)
	coverage, _, err := collectionCoverage(snap.CollectionOutcomes, out.Since, out.AsOf)
	if err != nil {
		return out, err
	}
	out.Coverage = &coverage
	if snap.Collection.LastSuccessAt.IsZero() {
		return out, nil
	}
	total := &responseAccumulator{}
	groups := map[responseGroupKey]*responseAccumulator{}
	for _, e := range snap.Episodes {
		total.add(e, out.AsOf)
		key := responseGroupKey{e.Severity, e.Component}
		if groups[key] == nil {
			groups[key] = &responseAccumulator{}
		}
		groups[key].add(e, out.AsOf)
	}
	result := total.result()
	out.Totals = &result
	for key, a := range groups {
		out.Groups = append(out.Groups, api.ObsAlertResponseGroup{Severity: key.severity, Component: key.component, Metrics: a.result()})
	}
	sort.Slice(out.Groups, func(i, j int) bool {
		a, b := out.Groups[i], out.Groups[j]
		if a.Severity != b.Severity {
			return a.Severity < b.Severity
		}
		return a.Component < b.Component
	})
	return out, nil
}

type responseGroupKey struct{ severity, component string }
type responseAccumulator struct {
	value           api.ObsAlertResponseAggregate
	ack, resolution []int64
}

func (a *responseAccumulator) add(e state.OperatorAlertResponseTimesEpisode, now time.Time) {
	a.value.Episodes++
	if e.Status == "unknown" {
		a.value.UnknownObservation++
	}
	if e.ResolvedAt == nil {
		a.value.Unresolved++
	}
	c := &a.value.ReceiptCoverage
	c.MissingRevisions += e.MissingRevisions
	switch e.ReceiptCoverage {
	case "complete":
		c.CompleteEpisodes++
	case "gaps":
		c.GappedEpisodes++
	default:
		c.InvalidReceiptEpisodes++
	}
	ack := &a.value.Acknowledgment
	switch {
	case e.ReceiptCoverage != "complete":
		ack.Unknown++
	case e.AckClockInvalid:
		ack.InvalidClock++
	case e.FirstAcknowledgedAt == nil:
		ack.NotObserved++
	default:
		ack.Samples++
		a.ack = append(a.ack, int64(e.FirstAcknowledgedAt.Sub(e.StartedAt)/time.Second))
	}
	resolution := &a.value.Resolution
	switch {
	case e.ResolvedAt == nil:
		resolution.NotObserved++
	case e.ResolvedAt.Before(e.StartedAt) || e.ResolvedAt.After(now):
		resolution.InvalidClock++
	default:
		resolution.Samples++
		a.resolution = append(a.resolution, int64(e.ResolvedAt.Sub(e.StartedAt)/time.Second))
	}
}
func (a *responseAccumulator) result() api.ObsAlertResponseAggregate {
	out := a.value
	out.Acknowledgment.P50Seconds, out.Acknowledgment.P95Seconds = responsePercentiles(a.ack)
	out.Resolution.P50Seconds, out.Resolution.P95Seconds = responsePercentiles(a.resolution)
	return out
}
func responsePercentiles(samples []int64) (*int64, *int64) {
	if len(samples) == 0 {
		return nil, nil
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	p50, p95 := samples[(len(samples)*50+99)/100-1], samples[(len(samples)*95+99)/100-1]
	return &p50, &p95
}
