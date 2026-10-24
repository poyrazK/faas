// Package releasegate judges an app's release gates (ADR-20261019071734):
// SLO burn, new error groups, custom metric bounds and AI call regressions
// (ADR 20261022153155). It is pure, so meterd's
// canary circuit breaker and apid's post-release watch judge gates the same
// way from evidence they collect themselves.
package releasegate

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// Gate kinds.
const (
	KindSLO          = "slo"
	KindNewErrors    = "new_errors"
	KindCustomMetric = "custom_metric"
	KindAI           = "ai"
)

// AI gate metrics (ADR 20261022153155).
const (
	AIMetricErrorRate   = "error_rate"
	AIMetricFlaggedRate = "flagged_rate"
	AIMetricCostPerCall = "cost_per_call"
	AIMetricTTFB        = "ttfb_ms"
)

// AIMetrics lists the AI gate metrics.
var AIMetrics = []string{AIMetricErrorRate, AIMetricFlaggedRate, AIMetricCostPerCall, AIMetricTTFB}

// aiFloors is how far above its baseline a release's AI metric must also
// be to fail, so a near-zero baseline does not fail on noise: a percentage
// point for the rates, 50 ms, and one millicent per call.
var aiFloors = map[string]float64{AIMetricErrorRate: 0.01, AIMetricFlaggedRate: 0.01, AIMetricCostPerCall: 1, AIMetricTTFB: 50}

// Gate is one release gate with the parameters its kind uses.
type Gate struct {
	Name string
	Kind string
	// SLO gates: the objective in basis points, the latency threshold (0
	// for an availability SLO), and the burn rate that fails the gate.
	ObjectiveBP        int
	LatencyThresholdMS int
	MaxBurnRate        int
	// New-error gates.
	MaxNewGroups int
	MinEvents    int
	// Custom metric gates. An AI gate uses MetricName for its AI metric and
	// Threshold for the largest increase over the baseline, in percent.
	MetricName string
	Comparison string
	Threshold  float64
	// MinSamples is the requests (SLO), metric values (custom metric) or
	// AI calls (AI, on each side) needed before the gate is judged.
	MinSamples int
}

// SLISample is one side's requests over a window.
type SLISample struct {
	Requests     int64
	ServerErrors int64
	SlowRequests int64
}

// Bad returns the requests the SLO counts against its budget.
func (s SLISample) Bad(latencyThresholdMS int) int64 {
	if latencyThresholdMS > 0 {
		return s.SlowRequests
	}
	return s.ServerErrors
}

// ErrorGroup is a new error group's class and size.
type ErrorGroup struct {
	ErrorClass string
	Events     int64
}

// MetricWindow is a custom metric's values over a window.
type MetricWindow struct {
	Samples int64
	Average float64
}

// AIWindow is one side's AI calls over a window (ADR 20261022153155).
type AIWindow struct {
	Calls          int64
	ErrorCalls     int64
	FlaggedCalls   int64
	CostMillicents int64
	TTFBCalls      int64
	TTFBMSTotal    int64
}

// value returns an AI metric and the calls it is taken over: all calls, or
// those with a first byte for time to first byte.
func (w AIWindow) value(metric string) (float64, int64, bool) {
	per := func(n, calls int64) float64 {
		if calls <= 0 {
			return 0
		}
		return float64(n) / float64(calls)
	}
	switch metric {
	case AIMetricErrorRate:
		return per(w.ErrorCalls, w.Calls), w.Calls, true
	case AIMetricFlaggedRate:
		return per(w.FlaggedCalls, w.Calls), w.Calls, true
	case AIMetricCostPerCall:
		return per(w.CostMillicents, w.Calls), w.Calls, true
	case AIMetricTTFB:
		return per(w.TTFBMSTotal, w.TTFBCalls), w.TTFBCalls, true
	}
	return 0, 0, false
}

// Evidence is what one gate measured for the release and, for SLO and AI
// gates, the baseline. Unavailable means the evidence could not be read.
type Evidence struct {
	Release     SLISample
	Baseline    SLISample
	NewGroups   []ErrorGroup
	Metric      MetricWindow
	AIRelease   AIWindow
	AIBaseline  AIWindow
	Unavailable bool
}

// Verdict is the result of judging every gate.
type Verdict struct {
	Results []api.ReleaseGateResult
	// Failed is the first failing gate's result, nil when all passed.
	Failed *api.ReleaseGateResult
}

// Summary names the first failing gate for a decision summary.
func (v Verdict) Summary() string {
	if v.Failed == nil {
		return ""
	}
	return "release gate " + v.Failed.Name + ": " + v.Failed.Summary
}

// Judge judges each gate against its evidence, in gate name order. Gates
// without evidence, or with too few samples, are not judged.
func Judge(gates []Gate, evidence map[string]Evidence) Verdict {
	sorted := append([]Gate(nil), gates...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	var v Verdict
	for _, g := range sorted {
		ev, ok := evidence[g.Name]
		if !ok || ev.Unavailable {
			continue
		}
		result, judged := judgeOne(g, ev)
		if !judged {
			continue
		}
		v.Results = append(v.Results, result)
		if !result.Passed && v.Failed == nil {
			failed := result
			v.Failed = &failed
		}
	}
	return v
}

func judgeOne(g Gate, ev Evidence) (api.ReleaseGateResult, bool) {
	r := api.ReleaseGateResult{Name: g.Name, Kind: g.Kind}
	switch g.Kind {
	case KindSLO:
		if ev.Release.Requests < int64(max(g.MinSamples, 1)) || g.ObjectiveBP <= 0 || g.ObjectiveBP >= 10000 {
			return r, false
		}
		budget := float64(10000-g.ObjectiveBP) / 10000
		badFraction := float64(ev.Release.Bad(g.LatencyThresholdMS)) / float64(ev.Release.Requests)
		burn := badFraction / budget
		baselineFraction := 0.0
		if ev.Baseline.Requests > 0 {
			baselineFraction = float64(ev.Baseline.Bad(g.LatencyThresholdMS)) / float64(ev.Baseline.Requests)
		}
		r.Observed, r.Bound, r.Samples = round(burn), float64(g.MaxBurnRate), ev.Release.Requests
		r.BaselineBadFraction = round(baselineFraction)
		r.Passed = burn < float64(g.MaxBurnRate) || badFraction <= baselineFraction
		r.Summary = fmt.Sprintf("burn rate %.1f (max %d) over %d requests; %.2f%% bad vs %.2f%% before", burn, g.MaxBurnRate, ev.Release.Requests, badFraction*100, baselineFraction*100)
	case KindNewErrors:
		qualifying := 0
		classes := map[string]bool{}
		var events int64
		for _, group := range ev.NewGroups {
			if group.Events < int64(max(g.MinEvents, 1)) {
				continue
			}
			qualifying++
			events += group.Events
			classes[group.ErrorClass] = true
		}
		r.Observed, r.Bound, r.Samples = float64(qualifying), float64(g.MaxNewGroups), events
		for class := range classes {
			r.ErrorClasses = append(r.ErrorClasses, class)
		}
		sort.Strings(r.ErrorClasses)
		if len(r.ErrorClasses) > api.ReleaseGateErrorClassesMax {
			r.ErrorClasses = r.ErrorClasses[:api.ReleaseGateErrorClassesMax]
		}
		r.Passed = qualifying <= g.MaxNewGroups
		r.Summary = fmt.Sprintf("%d new error groups (max %d)", qualifying, g.MaxNewGroups)
		if len(r.ErrorClasses) > 0 {
			r.Summary += ": " + strings.Join(r.ErrorClasses, ", ")
		}
	case KindCustomMetric:
		if ev.Metric.Samples < int64(max(g.MinSamples, 1)) || math.IsNaN(ev.Metric.Average) || math.IsInf(ev.Metric.Average, 0) {
			return r, false
		}
		r.Observed, r.Bound, r.Samples = round(ev.Metric.Average), g.Threshold, ev.Metric.Samples
		switch g.Comparison {
		case "below":
			r.Passed = ev.Metric.Average >= g.Threshold
		case "above":
			r.Passed = ev.Metric.Average <= g.Threshold
		default:
			return r, false
		}
		r.Summary = fmt.Sprintf("%s averaged %g over %d values (fails %s %g)", g.MetricName, round(ev.Metric.Average), ev.Metric.Samples, g.Comparison, g.Threshold)
	case KindAI:
		return judgeAI(g, ev)
	default:
		return r, false
	}
	return r, true
}

// judgeAI fails a release whose AI metric is more than Threshold percent,
// and more than the metric's floor, above its baseline's. Without enough
// calls on both sides it is not judged.
func judgeAI(g Gate, ev Evidence) (api.ReleaseGateResult, bool) {
	r := api.ReleaseGateResult{Name: g.Name, Kind: g.Kind}
	release, releaseCalls, ok := ev.AIRelease.value(g.MetricName)
	baseline, baselineCalls, _ := ev.AIBaseline.value(g.MetricName)
	minSamples := int64(max(g.MinSamples, 1))
	if !ok || releaseCalls < minSamples || baselineCalls < minSamples || g.Threshold <= 0 || math.IsNaN(g.Threshold) || math.IsInf(g.Threshold, 0) {
		return r, false
	}
	bound := max(baseline*(1+g.Threshold/100), baseline+aiFloors[g.MetricName])
	r.Observed, r.Bound, r.Samples = round(release), round(bound), releaseCalls
	r.Passed = release <= bound
	r.Summary = fmt.Sprintf("%s %s over %d calls vs %s before (fails above %s, +%g%%)", g.MetricName, aiValue(g.MetricName, release),
		releaseCalls, aiValue(g.MetricName, baseline), aiValue(g.MetricName, bound), g.Threshold)
	return r, true
}

// aiValue formats an AI metric: rates as percentages, time to first byte
// in milliseconds and cost per call in dollars.
func aiValue(metric string, v float64) string {
	switch metric {
	case AIMetricErrorRate, AIMetricFlaggedRate:
		return fmt.Sprintf("%.2f%%", v*100)
	case AIMetricTTFB:
		return fmt.Sprintf("%.0f ms", v)
	}
	return fmt.Sprintf("$%.5f", v/100_000)
}

// round keeps decisions readable and JSON-stable.
func round(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return math.Round(v*10000) / 10000
}
