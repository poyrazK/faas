package alerts

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appmetrics"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	anomalyPreviewDay          = 24 * time.Hour
	anomalyPreviewWeek         = 7 * anomalyPreviewDay
	weekdayPreviewWeeks        = 3
	zeroFallbackErrorRatePct   = 5
	zeroFallbackFailedRequests = 5
)

// AnomalyPreviewComparison describes the selected rule and its optional comparator.
type AnomalyPreviewComparison struct {
	EnvironmentScope     string
	Metric               string
	Comparison           string
	Threshold            float64
	CandidateThreshold   *float64
	SameWeekday          bool
	ZeroBaselineFallback bool
	ConfirmRecovery      bool
}

type anomalyPreviewSamples struct {
	daily    [api.AnomalyBaselineDays + 1][]appmetrics.AnomalyPreviewSample
	weekday  [weekdayPreviewWeeks][]appmetrics.AnomalyPreviewSample
	failures []int64
}

func (c AnomalyPreviewComparison) metric() string {
	if c.Metric == "" {
		return "request_count"
	}
	return c.Metric
}

// AnomalyPreviewFirstCheck returns the first hourly check in the weekly preview.
func AnomalyPreviewFirstCheck(now time.Time) time.Time {
	return now.UTC().Truncate(api.AnomalyPreviewStep).Add(-(api.AnomalyPreviewChecks - 1) * api.AnomalyPreviewStep)
}

// PreviewAnomaly compares rule configurations over the same hourly windows.
func PreviewAnomaly(ctx context.Context, fetcher appmetrics.PromQL, appID, window string, config AnomalyPreviewComparison, now time.Time) (api.AlertAnomalyPreviewResponse, error) {
	const step = api.AnomalyPreviewStep
	const checksPerDay = int(anomalyPreviewDay / step)
	const baselineChecks = api.AnomalyBaselineDays * checksPerDay
	first := AnomalyPreviewFirstCheck(now).Add(-api.AnomalyBaselineDays * anomalyPreviewDay)
	counts, err := appmetrics.FetchAnomalySamples(ctx, fetcher, appID, config.EnvironmentScope, window, config.metric(), first, step, baselineChecks+api.AnomalyPreviewChecks)
	if err != nil {
		return api.AlertAnomalyPreviewResponse{}, err
	}
	var samples anomalyPreviewSamples
	for day := range samples.daily {
		at := baselineChecks - day*checksPerDay
		samples.daily[day] = counts[at : at+api.AnomalyPreviewChecks]
	}
	if config.SameWeekday {
		if err := samples.fetchWeekdayHistory(ctx, fetcher, appID, config.EnvironmentScope, window, step); err != nil {
			return api.AlertAnomalyPreviewResponse{}, err
		}
	}
	if config.ZeroBaselineFallback {
		if err := samples.fetchCurrentFailures(ctx, fetcher, appID, config.EnvironmentScope, window, step); err != nil {
			return api.AlertAnomalyPreviewResponse{}, err
		}
	}
	return buildAnomalyPreview(samples, config, step, nil), nil
}

// ReplayAnomalyIncidents replays two hours of minute checks. It
// reads only those minutes and the same minutes on the baseline days.
func ReplayAnomalyIncidents(ctx context.Context, fetcher appmetrics.PromQL, appID, window string, config AnomalyPreviewComparison, start time.Time, cooldownMinutes int) (api.AlertAnomalyPreviewResponse, error) {
	const step = api.AnomalyIncidentReplayStep
	var samples anomalyPreviewSamples
	for day := range samples.daily {
		first := start.Add(-time.Duration(day) * anomalyPreviewDay)
		counts, err := appmetrics.FetchAnomalySamples(ctx, fetcher, appID, config.EnvironmentScope, window, config.metric(), first, step, api.AnomalyIncidentReplayChecks)
		if err != nil {
			return api.AlertAnomalyPreviewResponse{}, err
		}
		samples.daily[day] = counts
	}
	if config.SameWeekday {
		if err := samples.fetchWeekdayHistory(ctx, fetcher, appID, config.EnvironmentScope, window, step); err != nil {
			return api.AlertAnomalyPreviewResponse{}, err
		}
	}
	if config.ZeroBaselineFallback {
		if err := samples.fetchCurrentFailures(ctx, fetcher, appID, config.EnvironmentScope, window, step); err != nil {
			return api.AlertAnomalyPreviewResponse{}, err
		}
	}
	replay := &api.AlertAnomalyIncidentReplay{CooldownMinutes: cooldownMinutes}
	return buildAnomalyPreview(samples, config, step, replay), nil
}

func (s *anomalyPreviewSamples) fetchCurrentFailures(ctx context.Context, fetcher appmetrics.PromQL, appID, scope, window string, step time.Duration) error {
	current := s.daily[0]
	failures, err := appmetrics.FetchAnomalyPreviewFailedRequests(ctx, fetcher, appID, scope, window, current[0].At, step, len(current))
	if err != nil {
		return err
	}
	s.failures = failures
	return nil
}

func (s *anomalyPreviewSamples) fetchWeekdayHistory(ctx context.Context, fetcher appmetrics.PromQL, appID, scope, window string, step time.Duration) error {
	s.weekday[0] = s.daily[int(anomalyPreviewWeek/anomalyPreviewDay)]
	current := s.daily[0]
	for week := 1; week < len(s.weekday); week++ {
		first := current[0].At.Add(-time.Duration(week+1) * anomalyPreviewWeek)
		counts, err := appmetrics.FetchAnomalySamples(ctx, fetcher, appID, scope, window, "request_count", first, step, len(current))
		if err != nil {
			return err
		}
		s.weekday[week] = counts
	}
	return nil
}

func anomalyBaselineAt(history [][]appmetrics.AnomalyPreviewSample, i int, minimumRequests int64) anomalyBaseline {
	values := make([]float64, 0, len(history))
	for _, day := range history {
		past := day[i]
		if past.Covered && past.Requests >= minimumRequests {
			values = append(values, past.Value)
		}
	}
	return anomalyBaseline{value: median(values), days: len(values)}
}

func buildAnomalyPreview(samples anomalyPreviewSamples, config AnomalyPreviewComparison, step time.Duration, replay *api.AlertAnomalyIncidentReplay) api.AlertAnomalyPreviewResponse {
	currentSamples := samples.daily[0]
	metric := config.metric()
	requiresEligibleRequests := metric != "request_count"
	minimumHistoryRequests := int64(1)
	if requiresEligibleRequests {
		minimumHistoryRequests = api.AnomalyAlertMinRequests
	}
	preview := api.AlertAnomalyPreviewResponse{
		EnvironmentScope: config.EnvironmentScope,
		FirstCheckAt:     currentSamples[0].At,
		LastCheckAt:      currentSamples[len(currentSamples)-1].At,
		StepSeconds:      int(step / time.Second),
		SkippedWindows:   []api.AlertAnomalyPreviewSkippedWindow{},
		Breaches:         []api.AlertAnomalyPreviewBreach{},
		IncidentReplay:   replay,
	}
	var weekday *api.AlertAnomalyWeekdayPreview
	var fallback *api.AlertAnomalyZeroBaselineFallback
	if config.SameWeekday {
		weekday = &api.AlertAnomalyWeekdayPreview{
			SkippedWindows: []api.AlertAnomalyPreviewSkippedWindow{},
			FallbackAt:     []time.Time{},
		}
		preview.Candidate = &api.AlertAnomalyPreviewCandidate{
			Threshold:       config.Threshold,
			Breaches:        []api.AlertAnomalyPreviewBreach{},
			WeekdayBaseline: weekday,
		}
	} else if config.ZeroBaselineFallback {
		fallback = &api.AlertAnomalyZeroBaselineFallback{
			MinimumErrorRatePct:   zeroFallbackErrorRatePct,
			MinimumFailedRequests: zeroFallbackFailedRequests,
		}
		preview.Candidate = &api.AlertAnomalyPreviewCandidate{
			Threshold:            config.Threshold,
			Breaches:             []api.AlertAnomalyPreviewBreach{},
			ZeroBaselineFallback: fallback,
		}
	} else if config.CandidateThreshold != nil {
		preview.Candidate = &api.AlertAnomalyPreviewCandidate{
			Threshold: *config.CandidateThreshold,
			Breaches:  []api.AlertAnomalyPreviewBreach{},
		}
	}
	for i, current := range currentSamples {
		if !current.Covered {
			skipped := api.AlertAnomalyPreviewSkippedWindow{
				EvaluatedAt: current.At, Reason: api.AlertAnomalyTelemetryMissing,
			}
			preview.SkippedWindows = append(preview.SkippedWindows, skipped)
			if weekday != nil {
				weekday.SkippedWindows = append(weekday.SkippedWindows, skipped)
			}
			continue
		}
		var eligibleRequests *int64
		if requiresEligibleRequests {
			requests := current.Requests
			eligibleRequests = &requests
			if requests < api.AnomalyAlertMinRequests {
				preview.SkippedWindows = append(preview.SkippedWindows, api.AlertAnomalyPreviewSkippedWindow{
					EvaluatedAt: current.At, Reason: api.AlertAnomalyInsufficientTraffic,
					EligibleRequests: eligibleRequests, RequiredRequests: api.AnomalyAlertMinRequests,
				})
				continue
			}
		}
		baseline := anomalyBaselineAt(samples.daily[1:], i, minimumHistoryRequests)
		decision := evaluateAnomaly(current.Value, baseline, api.AnomalyBaselineMinDays, metric, config.Comparison, config.Threshold)
		if decision.skip != "" {
			preview.SkippedWindows = append(preview.SkippedWindows, api.AlertAnomalyPreviewSkippedWindow{
				EvaluatedAt: current.At, Reason: decision.evidence.Reason, EligibleRequests: eligibleRequests,
			})
		} else {
			preview.Evaluated++
			preview.Breaches = appendPreviewBreach(preview.Breaches, current.At, decision, eligibleRequests)
		}
		if preview.Candidate == nil {
			continue
		}
		candidateBaseline, requiredDays := baseline, api.AnomalyBaselineMinDays
		if weekday != nil {
			matching := anomalyBaselineAt(samples.weekday[:], i, minimumHistoryRequests)
			if matching.days == weekdayPreviewWeeks {
				candidateBaseline, requiredDays = matching, weekdayPreviewWeeks
			} else {
				weekday.FallbackAt = append(weekday.FallbackAt, current.At)
			}
		}
		candidateDecision := evaluateAnomaly(current.Value, candidateBaseline, requiredDays, metric, config.Comparison, preview.Candidate.Threshold)
		if candidateDecision.skip != "" {
			if weekday != nil {
				weekday.SkippedWindows = append(weekday.SkippedWindows, api.AlertAnomalyPreviewSkippedWindow{
					EvaluatedAt: current.At, Reason: candidateDecision.evidence.Reason,
				})
			}
			continue
		}
		if weekday != nil {
			weekday.Evaluated++
		}
		if fallback != nil && candidateDecision.evidence.Reason == api.AlertAnomalyZeroBaseline &&
			current.Value >= fallback.MinimumErrorRatePct && samples.failures[i] >= fallback.MinimumFailedRequests {
			preview.Candidate.Breaches = append(preview.Candidate.Breaches, api.AlertAnomalyPreviewBreach{
				EvaluatedAt:          current.At,
				Observed:             current.Value,
				Baseline:             candidateBaseline.value,
				TriggerValue:         fallback.MinimumErrorRatePct,
				EligibleRequests:     eligibleRequests,
				FailedRequests:       &samples.failures[i],
				ZeroBaselineFallback: true,
			})
			continue
		}
		preview.Candidate.Breaches = appendPreviewBreach(preview.Candidate.Breaches, current.At, candidateDecision, eligibleRequests)
	}
	preview.Skipped = len(preview.SkippedWindows)
	if replay != nil {
		populateReplayIncidents(&preview, currentSamples, config.ConfirmRecovery)
	}
	return preview
}

func populateReplayIncidents(preview *api.AlertAnomalyPreviewResponse, samples []appmetrics.AnomalyPreviewSample, confirmRecovery bool) {
	replay := preview.IncidentReplay
	cooldown := replay.CooldownMinutes
	replay.Incidents = replayAnomalyIncidents(samples, preview.Breaches, preview.SkippedWindows, cooldown, 1)
	if confirmRecovery {
		replay.RecoveryConfirmation = &api.AlertAnomalyRecoveryComparison{
			RequiredHealthyChecks: api.AnomalyReplayRecoveryChecks,
			Incidents:             replayAnomalyIncidents(samples, preview.Breaches, preview.SkippedWindows, cooldown, api.AnomalyReplayRecoveryChecks),
		}
	}
	if candidate := preview.Candidate; candidate != nil {
		skipped := preview.SkippedWindows
		if candidate.WeekdayBaseline != nil {
			skipped = candidate.WeekdayBaseline.SkippedWindows
		}
		replay.CandidateIncidents = replayAnomalyIncidents(samples, candidate.Breaches, skipped, cooldown, 1)
		if confirmation := replay.RecoveryConfirmation; confirmation != nil {
			confirmation.CandidateIncidents = replayAnomalyIncidents(samples, candidate.Breaches, skipped, cooldown, confirmation.RequiredHealthyChecks)
		}
	}
}

// Breaches and skips are ordered subsets of the same minute checkpoints.
func replayAnomalyIncidents(samples []appmetrics.AnomalyPreviewSample, breaches []api.AlertAnomalyPreviewBreach, skipped []api.AlertAnomalyPreviewSkippedWindow, cooldownMinutes, requiredHealthyChecks int) []api.AlertObservedIncident {
	incidents := []api.AlertObservedIncident{}
	healthyChecks := 0
	for _, sample := range samples {
		at := sample.At
		switch {
		case len(skipped) > 0 && skipped[0].EvaluatedAt.Equal(at):
			skipped = skipped[1:]
			healthyChecks = 0
		case len(breaches) > 0 && breaches[0].EvaluatedAt.Equal(at):
			breaches = breaches[1:]
			healthyChecks = 0
			incidents = state.ApplyAlertObservation(incidents, api.AnomalyIncidentReplayChecks, at, cooldownMinutes, api.AlertAnomalyObservedBreach, "")
		case len(incidents) > 0 && incidents[0].EndedAt == nil:
			healthyChecks++
			if healthyChecks == requiredHealthyChecks {
				incidents = state.ApplyAlertObservation(incidents, api.AnomalyIncidentReplayChecks, at, cooldownMinutes, api.AlertAnomalyThresholdNotReached, "")
			}
		}
	}
	return incidents
}

func appendPreviewBreach(breaches []api.AlertAnomalyPreviewBreach, at time.Time, decision anomalyObservation, eligibleRequests *int64) []api.AlertAnomalyPreviewBreach {
	if !decision.fires {
		return breaches
	}
	return append(breaches, api.AlertAnomalyPreviewBreach{
		EvaluatedAt: at, Observed: decision.value, Baseline: decision.baseline.value, TriggerValue: decision.trigger,
		EligibleRequests: eligibleRequests,
	})
}
