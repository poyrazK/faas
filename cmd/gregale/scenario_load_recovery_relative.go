package main

import (
	"fmt"
	"math"
	"time"
)

type testLoadRecoveryRelativeSpec struct {
	P95IncreasePercent *float64 `yaml:"p95_increase_percent,omitempty"`
	P99IncreasePercent *float64 `yaml:"p99_increase_percent,omitempty"`
	ErrorRateIncrease  *float64 `yaml:"error_rate_increase,omitempty"`
	SuccessRateDrop    *float64 `yaml:"success_rate_drop,omitempty"`
	BaselineMinSamples int      `yaml:"baseline_min_samples"`
}
type testLoadRecoveryBaseline struct {
	Phase      string `json:"phase"`
	MinSamples int    `json:"min_samples"`
	testLoadMetrics
}
type testLoadRecoveryRelativeEvidence struct {
	Metric   string  `json:"metric"`
	Baseline float64 `json:"baseline"`
	Operator string  `json:"operator"`
	Limit    float64 `json:"limit"`
	Actual   float64 `json:"actual"`
	Passed   bool    `json:"passed"`
}

func validateTestLoadRecoveryRelative(spec *testLoadRecoveryRelativeSpec) error {
	if spec == nil {
		return nil
	}
	if spec.BaselineMinSamples < 1 || spec.BaselineMinSamples > testLoadMaxRequests {
		return fmt.Errorf("relative.baseline_min_samples must be between 1 and %d", testLoadMaxRequests)
	}
	if spec.P95IncreasePercent == nil && spec.P99IncreasePercent == nil && spec.ErrorRateIncrease == nil && spec.SuccessRateDrop == nil {
		return fmt.Errorf("relative requires a latency or rate budget")
	}
	return validateTestLoadRelativeBudgets(spec.P95IncreasePercent, spec.P99IncreasePercent, spec.ErrorRateIncrease, spec.SuccessRateDrop)
}

// Only baseline requests completed before the fault began can calibrate a
// recovery budget. Slow requests that overlap the fault cannot relax it.
func testLoadRecoveryBaselineFor(name string, window testLoadPhaseWindow, samples []testLoadRecoverySample, minSamples int) *testLoadRecoveryBaseline {
	baseline := &testLoadRecoveryBaseline{Phase: name, MinSamples: minSamples}
	var latencies []time.Duration
	if !window.StartedAt.IsZero() && !window.FinishedAt.IsZero() {
		for _, sample := range samples {
			if sample.StartedAt.Before(window.StartedAt) || !sample.StartedAt.Before(window.FinishedAt) || sample.StartedAt.Add(sample.Duration).After(window.FinishedAt) {
				continue
			}
			baseline.Requests++
			if !sample.Passed {
				baseline.Failures++
			}
			latencies = append(latencies, sample.Duration)
		}
	}
	baseline.Latency = testLoadLatencySummary(latencies)
	if baseline.Requests > 0 {
		baseline.ErrorRate = float64(baseline.Failures) / float64(baseline.Requests)
		baseline.SuccessRate = 1 - baseline.ErrorRate
	}
	return baseline
}

func testLoadRecoveryRelativeChecks(spec *testLoadRecoveryRelativeSpec, baseline, actual testLoadMetrics) []testLoadRecoveryRelativeEvidence {
	if spec == nil {
		return nil
	}
	var checks []testLoadRecoveryRelativeEvidence
	add := func(metric string, base, limit, observed float64, operator string) {
		// Rate limits involve adding/subtracting fractions. Allow rounding at
		// the boundary, well below the resolution of the 100000-request cap.
		tolerance := 0.0
		if metric == "error_rate" || metric == "success_rate" {
			tolerance = 1e-12
		}
		passed := actual.Requests > 0 && observed <= limit+tolerance
		if operator == ">=" {
			passed = actual.Requests > 0 && observed >= limit-tolerance
		}
		checks = append(checks, testLoadRecoveryRelativeEvidence{Metric: metric, Baseline: base, Operator: operator, Limit: limit, Actual: observed, Passed: passed})
	}
	if spec.P95IncreasePercent != nil {
		add("p95_ms", baseline.Latency.P95MS, baseline.Latency.P95MS*(1+*spec.P95IncreasePercent/100), actual.Latency.P95MS, "<=")
	}
	if spec.P99IncreasePercent != nil {
		add("p99_ms", baseline.Latency.P99MS, baseline.Latency.P99MS*(1+*spec.P99IncreasePercent/100), actual.Latency.P99MS, "<=")
	}
	if spec.ErrorRateIncrease != nil {
		add("error_rate", baseline.ErrorRate, math.Min(1, baseline.ErrorRate+*spec.ErrorRateIncrease), actual.ErrorRate, "<=")
	}
	if spec.SuccessRateDrop != nil {
		add("success_rate", baseline.SuccessRate, math.Max(0, baseline.SuccessRate-*spec.SuccessRateDrop), actual.SuccessRate, ">=")
	}
	return checks
}
