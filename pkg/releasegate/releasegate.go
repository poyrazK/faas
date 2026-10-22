// Package releasegate judges an app's release gates (ADR-20261019071734,
// ADR-20261022082305): SLO burn, new error groups, custom metric bounds,
// synthetic check probes and metric changes. It is pure, so meterd's
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
	KindSynthetic    = "synthetic"
	KindMetricChange = "metric_change"
)

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
	// Custom metric gates.
	MetricName string
	Comparison string
	Threshold  float64
	// Synthetic gates: the check's name and the share of failed probes
	// allowed.
	CheckName         string
	MaxFailurePercent int
	// Metric change gates (MetricName and Comparison too): how far the
	// metric may move, in percent of its average before the rollout.
	MaxChangePercent int
	// MinSamples is the requests (SLO), probes (synthetic) or metric values
	// (custom metric, metric change) the release needs before the gate is
	// judged.
	MinSamples int
}

// ProbeCounts is one revision's synthetic probes over a window.
type ProbeCounts struct {
	Requests int64
	Failures int64
}

func (p ProbeCounts) failurePercent() float64 {
	if p.Requests == 0 {
		return 0
	}
	return float64(p.Failures) * 100 / float64(p.Requests)
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

// Evidence is what one gate measured for the release and its baseline.
// Unavailable means the evidence could not be read.
type Evidence struct {
	Release   SLISample
	Baseline  SLISample
	NewGroups []ErrorGroup
	Metric    MetricWindow
	// Probes and BaselineProbes are the candidate's and the stable
	// revision's synthetic probes (synthetic gates).
	Probes         ProbeCounts
	BaselineProbes ProbeCounts
	// PriorMetric is the metric before the rollout started (metric change).
	PriorMetric MetricWindow
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
	case KindSynthetic:
		return judgeSynthetic(g, ev, r)
	case KindMetricChange:
		return judgeMetricChange(g, ev, r)
	default:
		return r, false
	}
	return r, true
}

// judgeSynthetic fails a candidate that failed more of its probes than the
// gate allows and more than the stable revision did.
func judgeSynthetic(g Gate, ev Evidence, r api.ReleaseGateResult) (api.ReleaseGateResult, bool) {
	if ev.Probes.Requests < int64(max(g.MinSamples, 1)) || g.MaxFailurePercent < 0 || g.MaxFailurePercent > 100 {
		return r, false
	}
	failed, baseline := ev.Probes.failurePercent(), ev.BaselineProbes.failurePercent()
	r.Observed, r.Bound, r.Samples, r.Baseline = round(failed), float64(g.MaxFailurePercent), ev.Probes.Requests, round(baseline)
	r.Passed = failed <= float64(g.MaxFailurePercent) || failed <= baseline
	stable := "no probes of the stable revision"
	if ev.BaselineProbes.Requests > 0 {
		stable = fmt.Sprintf("%.0f%% on the stable revision", baseline)
	}
	r.Summary = fmt.Sprintf("%s failed %d of %d probes (%.0f%%, max %d%%) vs %s", g.CheckName, ev.Probes.Failures,
		ev.Probes.Requests, failed, g.MaxFailurePercent, stable)
	return r, true
}

// judgeMetricChange fails a metric that moved in the gate's direction by
// more than the gate allows, relative to its average before the rollout.
func judgeMetricChange(g Gate, ev Evidence, r api.ReleaseGateResult) (api.ReleaseGateResult, bool) {
	minSamples := int64(max(g.MinSamples, 1))
	if ev.Metric.Samples < minSamples || ev.PriorMetric.Samples < minSamples || ev.PriorMetric.Average == 0 ||
		!finite(ev.Metric.Average) || !finite(ev.PriorMetric.Average) || g.MaxChangePercent < 1 {
		return r, false
	}
	change := (ev.Metric.Average - ev.PriorMetric.Average) / math.Abs(ev.PriorMetric.Average) * 100
	r.Observed, r.Samples, r.Baseline = round(change), ev.Metric.Samples, round(ev.PriorMetric.Average)
	switch g.Comparison {
	case "above":
		r.Bound = float64(g.MaxChangePercent)
		r.Passed = change <= r.Bound
	case "below":
		r.Bound = -float64(g.MaxChangePercent)
		r.Passed = change >= r.Bound
	default:
		return r, false
	}
	direction := "rose"
	if change < 0 {
		direction = "fell"
	}
	r.Summary = fmt.Sprintf("%s %s %.0f%% (%g vs %g before the rollout; fails %s %+g%%)", g.MetricName, direction,
		math.Abs(change), round(ev.Metric.Average), round(ev.PriorMetric.Average), g.Comparison, r.Bound)
	return r, true
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// round keeps decisions readable and JSON-stable.
func round(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return math.Round(v*10000) / 10000
}
