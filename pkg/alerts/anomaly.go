package alerts

import (
	"context"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appmetrics"
	"github.com/onebox-faas/faas/pkg/state"
)

// anomalyBaseline is one rule's ADR-744 baseline: the median of the metric
// at the same time of day over the previous days that had traffic.
type anomalyBaseline struct {
	value float64
	days  int
}

type anomalyKey struct {
	appID, scope, metric, window string
}

type anomalyRead struct {
	done     chan struct{}
	baseline anomalyBaseline
	ok       bool
}

const (
	anomalyBaselineStep = time.Minute
	anomalyWarmBudget   = 30 * time.Second
	anomalyWarmLimit    = 2
)

type anomalyBaselineFrame struct {
	first     time.Time
	baselines []anomalyBaseline
}

func (f anomalyBaselineFrame) at(check time.Time) (anomalyBaseline, bool) {
	offset := check.Sub(f.first)
	if offset < 0 || offset%anomalyBaselineStep != 0 || offset/anomalyBaselineStep >= time.Duration(len(f.baselines)) {
		return anomalyBaseline{}, false
	}
	return f.baselines[offset/anomalyBaselineStep], true
}

// Share matching reads within a tick and retain aligned historical checkpoints
// across ticks. The frame retains time-of-day changes inside the cache period.
type anomalyState struct {
	mu        sync.Mutex
	at        time.Time
	reads     map[anomalyKey]*anomalyRead
	frames    map[anomalyKey]anomalyBaselineFrame
	warming   map[anomalyKey]bool
	requested []anomalyKey
}

func (s *anomalyState) begin(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	at := now.UTC().Truncate(time.Second)
	if !s.at.Equal(at) || s.reads == nil {
		s.at = at
		s.reads = make(map[anomalyKey]*anomalyRead)
	}
	for key, frame := range s.frames {
		if !at.Before(frame.first.Add(api.AnomalyBaselineCacheTTL)) {
			delete(s.frames, key)
		}
	}
}

type anomalyObservation struct {
	value    float64
	trigger  float64
	fires    bool
	skip     string
	baseline anomalyBaseline
	evidence api.AlertAnomalyEvaluation
}

// observeAnomaly evaluates an above_baseline / below_baseline rule.
func (e *Evaluator) observeAnomaly(ctx context.Context, rule state.AlertRule) anomalyObservation {
	metric, window := string(rule.Metric), string(rule.WindowSpec)
	observed, source, evidence := appmetrics.FetchAnomalyAlertMetric(ctx, e.promQL, e.log, rule.AppID, rule.EnvironmentScope, window, metric, e.anomaly.at)
	if appmetrics.IsInsufficientSource(source) {
		return anomalyObservation{skip: skipInsufficient, evidence: evidence}
	}
	if source != appmetrics.SourcePrometheus {
		if evidence.Reason == "" {
			evidence.Reason = api.AlertAnomalyTelemetryUnavailable
		}
		return anomalyObservation{skip: skipDegraded, evidence: evidence}
	}
	baseline, ok := e.anomalyBaselineFor(ctx, rule)
	if !ok {
		return anomalyObservation{skip: skipDegraded, evidence: api.AlertAnomalyEvaluation{Reason: api.AlertAnomalyTelemetryUnavailable}}
	}
	result := evaluateAnomaly(observed, baseline, api.AnomalyBaselineMinDays, metric, string(rule.Comparison), rule.Threshold)
	if rule.EnvironmentScope != "" {
		result.evidence.BaselineDays = &baseline.days
	}
	return result
}

func evaluateAnomaly(observed float64, baseline anomalyBaseline, requiredDays int, metric, comparison string, multiplier float64) anomalyObservation {
	result := anomalyObservation{value: observed, baseline: baseline}
	if baseline.days < requiredDays {
		result.skip = skipInsufficient
		result.evidence = api.AlertAnomalyEvaluation{
			Reason: api.AlertAnomalyInsufficientHistory, Count: int64(baseline.days), RequiredCount: int64(requiredDays),
		}
		return result
	}
	trigger := baseline.value * multiplier
	result.trigger = trigger
	reason := api.AlertAnomalyThresholdNotReached
	var minimumValue *float64
	switch comparison {
	case api.AlertComparisonAboveBaseline:
		minimum := api.AnomalyAlertMetrics[metric]
		switch {
		case baseline.value == 0:
			reason = api.AlertAnomalyZeroBaseline
		case observed < minimum:
			reason, minimumValue = api.AlertAnomalyMinimumNotReached, &minimum
		default:
			result.fires = observed >= trigger
		}
	case api.AlertComparisonBelowBaseline:
		minimum := float64(api.AnomalyAlertMinRequests)
		if baseline.value < minimum {
			reason, minimumValue = api.AlertAnomalyMinimumNotReached, &minimum
		} else {
			result.fires = observed <= trigger
		}
	default:
		return anomalyObservation{skip: skipDegraded, evidence: api.AlertAnomalyEvaluation{Reason: api.AlertAnomalyTelemetryUnavailable}}
	}
	// Breaches carry the same values when a cooldown prevents a repeat fire.
	result.evidence = api.AlertAnomalyEvaluation{Observed: &observed, MinimumValue: minimumValue}
	if !result.fires {
		result.evidence.Reason = reason
	}
	if !math.IsInf(baseline.value, 0) {
		result.evidence.Baseline = &baseline.value
	}
	if baseline.value > 0 && !math.IsInf(trigger, 0) && !math.IsNaN(trigger) {
		result.evidence.TriggerValue = &trigger
	}
	return result
}

// anomalyBaselineFor returns the cached baseline or recomputes it. ok is
// false when Prometheus could not answer, so the rule degrades rather than
// firing on a partial history.
func (e *Evaluator) anomalyBaselineFor(ctx context.Context, rule state.AlertRule) (anomalyBaseline, bool) {
	key := anomalyKey{appID: rule.AppID, scope: rule.EnvironmentScope, metric: string(rule.Metric), window: string(rule.WindowSpec)}
	at := e.anomaly.at
	e.anomaly.mu.Lock()
	frame := e.anomaly.frames[key]
	if baseline, ok := frame.at(at); ok {
		e.anomaly.mu.Unlock()
		return baseline, true
	}
	if cached := e.anomaly.reads[key]; cached != nil {
		e.anomaly.mu.Unlock()
		select {
		case <-cached.done:
			return cached.baseline, cached.ok
		case <-ctx.Done():
			return anomalyBaseline{}, false
		}
	}
	read := &anomalyRead{done: make(chan struct{})}
	e.anomaly.reads[key] = read
	e.anomaly.mu.Unlock()

	values, qualified, source := appmetrics.FetchAnomalyAlertHistory(ctx, e.promQL, e.log, key.appID, key.scope, key.window, key.metric, at)
	read.ok = source == appmetrics.SourcePrometheus
	if read.ok {
		read.baseline = anomalyBaseline{value: median(values), days: qualified}
	}
	if !read.ok && ctx.Err() != nil {
		// A canceled account or rule budget should not poison later callers.
		e.anomaly.mu.Lock()
		delete(e.anomaly.reads, key)
		e.anomaly.mu.Unlock()
	}
	close(read.done)
	if read.ok {
		e.anomaly.requestWarm(key)
	}
	return read.baseline, read.ok
}

func (s *anomalyState) requestWarm(key anomalyKey) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, attempted := s.frames[key]; attempted || s.warming[key] || len(s.warming) >= anomalyWarmLimit {
		return
	}
	if s.warming == nil {
		s.warming = make(map[anomalyKey]bool)
	}
	s.warming[key] = true
	s.requested = append(s.requested, key)
}

// Cache warming starts after the current reads finish and shares the
// evaluator loop's lifetime. A missing or unfinished frame uses a direct read.
func (e *Evaluator) warmAnomalyBaselines(ctx context.Context) {
	s := &e.anomaly
	s.mu.Lock()
	at, requested := s.at, s.requested
	s.requested = nil
	s.mu.Unlock()
	for _, key := range requested {
		go e.warmAnomalyBaseline(ctx, key, at)
	}
}

func (e *Evaluator) warmAnomalyBaseline(ctx context.Context, key anomalyKey, at time.Time) {
	warmCtx, cancel := context.WithTimeout(ctx, anomalyWarmBudget)
	defer cancel()
	frame := e.prefetchAnomalyBaselines(warmCtx, key, at)
	s := &e.anomaly
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.warming, key)
	if ctx.Err() == nil && s.at.Before(at.Add(api.AnomalyBaselineCacheTTL)) {
		if s.frames == nil {
			s.frames = make(map[anomalyKey]anomalyBaselineFrame)
		}
		s.frames[key] = frame
	}
}

func (e *Evaluator) prefetchAnomalyBaselines(ctx context.Context, key anomalyKey, first time.Time) anomalyBaselineFrame {
	frame := anomalyBaselineFrame{first: first}
	const checkpoints = int(api.AnomalyBaselineCacheTTL / anomalyBaselineStep)
	history := make([][]appmetrics.AnomalyPreviewSample, 0, api.AnomalyBaselineDays)
	for day := 1; day <= api.AnomalyBaselineDays; day++ {
		past := first.Add(-time.Duration(day) * 24 * time.Hour)
		samples, err := appmetrics.FetchAnomalySamples(ctx, e.promQL, key.appID, key.scope, key.window, key.metric, past, anomalyBaselineStep, checkpoints)
		if err != nil {
			return frame
		}
		history = append(history, samples)
	}
	minimum := int64(api.AnomalyAlertMinRequests)
	if key.metric == "request_count" {
		minimum = 1
	}
	frame.baselines = make([]anomalyBaseline, checkpoints)
	for i := range frame.baselines {
		frame.baselines[i] = anomalyBaselineAt(history, i, minimum)
	}
	return frame
}

func (b anomalyBaseline) payload(observed float64) map[string]any {
	if b.days == 0 {
		return nil
	}
	extra := map[string]any{"baseline": b.value, "baseline_days": b.days}
	if b.value > 0 {
		extra["ratio"] = observed / b.value
	}
	return extra
}

func median(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) / 2
}
