package operatorobs

import (
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type recurrenceAccumulator struct {
	value           api.ObsAlertRecurrenceMetrics
	durations, gaps []int64
}

// Each sorted group contributes n durations and n-1 adjacent comparisons.
// No comparison crosses a rule-definition or exact-series boundary.
func (a *recurrenceAccumulator) add(episodes []state.OperatorAlertRecurrenceEpisode, now time.Time) {
	for i, e := range episodes {
		a.value.Episodes++
		if e.Status == "unknown" {
			a.value.UnknownObservation++
		}
		switch {
		case e.ResolvedAt == nil:
			a.value.Unresolved++
			a.value.Duration.NotObserved++
		case invalidRecurrenceResolution(e, now):
			a.value.Duration.InvalidClock++
		default:
			a.value.Duration.Samples++
			a.durations = append(a.durations, int64(e.ResolvedAt.Sub(e.StartedAt)/time.Second))
		}
		if i == 0 {
			continue
		}
		a.value.RepeatActivations++
		previous := episodes[i-1]
		switch {
		case previous.ResolvedAt == nil:
			a.value.RecoveryGap.UnconfirmedRecovery++
		case invalidRecurrenceResolution(previous, now):
			a.value.RecoveryGap.InvalidClock++
		case previous.ResolvedAt.After(e.StartedAt):
			a.value.RecoveryGap.ResolutionOverlap++
		default:
			a.value.RecoveryGap.Samples++
			a.gaps = append(a.gaps, int64(e.StartedAt.Sub(*previous.ResolvedAt)/time.Second))
		}
	}
}

func invalidRecurrenceResolution(e state.OperatorAlertRecurrenceEpisode, now time.Time) bool {
	return e.ResolvedAt.Before(e.StartedAt) || e.ResolvedAt.After(now)
}

func (a *recurrenceAccumulator) result() api.ObsAlertRecurrenceMetrics {
	out := a.value
	out.Duration.MinSeconds, out.Duration.P50Seconds = recurrenceStatistics(a.durations)
	out.RecoveryGap.MinSeconds, out.RecoveryGap.P50Seconds = recurrenceStatistics(a.gaps)
	return out
}

func recurrenceStatistics(samples []int64) (*int64, *int64) {
	if len(samples) == 0 {
		return nil, nil
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	minimum, median := samples[0], samples[(len(samples)+1)/2-1]
	return &minimum, &median
}
