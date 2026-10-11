package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

type testRegressionBudget struct {
	P95Percent        *float64 `yaml:"p95_percent,omitempty"`
	ErrorRateIncrease *float64 `yaml:"error_rate_increase,omitempty"`
	MinSamples        *int     `yaml:"min_samples,omitempty"`
}

type testRegressionSpec struct {
	testRegressionBudget `yaml:",inline"`
	Steps                map[string]testRegressionBudget `yaml:"steps,omitempty"`
}

type testRegressionLimits struct {
	P95Percent        float64
	ErrorRateIncrease float64
	MinSamples        int
}

type testRegressionConfig struct {
	testRegressionLimits
	Steps map[string]testRegressionLimits
}

func validateTestRegressionSpec(spec *testRegressionSpec) error {
	if spec == nil {
		return nil
	}
	if err := validateTestRegressionBudget(spec.testRegressionBudget); err != nil {
		return fmt.Errorf("load.regression: %w", err)
	}
	if len(spec.Steps) > 100 {
		return errors.New("load.regression.steps may declare at most 100 step budgets")
	}
	for name, budget := range spec.Steps {
		if !testHTTPNamePattern.MatchString(name) {
			return fmt.Errorf("load.regression.steps has an invalid step name %q", name)
		}
		if budget == (testRegressionBudget{}) {
			return fmt.Errorf("load.regression.steps.%s needs a regression budget", name)
		}
		if err := validateTestRegressionBudget(budget); err != nil {
			return fmt.Errorf("load.regression.steps.%s: %w", name, err)
		}
	}
	return nil
}

func validateTestRegressionBudget(budget testRegressionBudget) error {
	if budget.P95Percent != nil && (!finiteNonnegative(*budget.P95Percent) || *budget.P95Percent > 1000) {
		return errors.New("p95_percent must be a finite percent between 0 and 1000")
	}
	if budget.ErrorRateIncrease != nil && (!finiteNonnegative(*budget.ErrorRateIncrease) || *budget.ErrorRateIncrease > 1) {
		return errors.New("error_rate_increase must be a finite fraction between 0 and 1")
	}
	if budget.MinSamples != nil && (*budget.MinSamples < 1 || *budget.MinSamples > testLoadMaxRequests) {
		return fmt.Errorf("min_samples must be between 1 and %d", testLoadMaxRequests)
	}
	return nil
}

func resolveTestRegressionConfig(spec *testRegressionSpec) testRegressionConfig {
	cfg := testRegressionConfig{testRegressionLimits: testRegressionLimits{P95Percent: 10, MinSamples: 20}}
	if spec != nil {
		cfg.testRegressionLimits = inheritTestRegressionLimits(cfg.testRegressionLimits, spec.testRegressionBudget)
		cfg.Steps = make(map[string]testRegressionLimits, len(spec.Steps))
		for name, budget := range spec.Steps {
			cfg.Steps[name] = inheritTestRegressionLimits(cfg.testRegressionLimits, budget)
		}
	}
	return cfg
}

func inheritTestRegressionLimits(limits testRegressionLimits, budget testRegressionBudget) testRegressionLimits {
	if budget.P95Percent != nil {
		limits.P95Percent = *budget.P95Percent
	}
	if budget.ErrorRateIncrease != nil {
		limits.ErrorRateIncrease = *budget.ErrorRateIncrease
	}
	if budget.MinSamples != nil {
		limits.MinSamples = *budget.MinSamples
	}
	return limits
}

func (cfg testRegressionConfig) stepLimits(name string) testRegressionLimits {
	if limits, ok := cfg.Steps[name]; ok {
		return limits
	}
	return cfg.testRegressionLimits
}

func finiteNonnegative(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}

type testLoadWorkload struct {
	Version int    `json:"version"`
	SHA256  string `json:"sha256"`
	Label   string `json:"label,omitempty"`
}

// This signature identifies the declared journey and schedule, not a machine or
// app revision. Header literals, resolved consumer credentials, environment and
// case values are excluded. load.workload identifies a dataset/environment version.
func testPlanWorkload(plan testRunPlan) (*testLoadWorkload, error) {
	p := plan.Prepared
	steps := append(append([]testHTTPRequest{}, p.Scenario.Requests...), p.Scenario.Checks...)
	for i := range steps {
		headers := make(map[string]string, len(steps[i].Headers))
		for name, value := range steps[i].Headers {
			// Keep template dependencies, but omit literal header credentials.
			headers[strings.ToLower(name)] = strings.Join(testSecretReferencePattern.FindAllString(value, -1), ",")
		}
		steps[i].Headers = headers
	}
	fields := make(map[string]string, len(plan.Case.Values))
	for name, value := range plan.Case.Values {
		switch value.(type) {
		case string:
			fields[name] = "string"
		case bool:
			fields[name] = "boolean"
		default:
			fields[name] = "number"
		}
	}
	label := ""
	if p.Scenario.Load != nil {
		label = p.Scenario.Load.Workload
	}
	// Normalize defaults and durations before hashing; budgets and progress do
	// not change the traffic. App commands/source revisions must be free to change.
	signature := struct {
		Version                         int
		Label                           string
		Managed                         bool
		Timeout                         time.Duration
		Steps                           []testHTTPRequest
		Fields                          map[string]string
		VUs, Iterations, Rate, Requests int
		Duration, Pacing                time.Duration
		Stages                          []testLoadStage
	}{1, label, p.Scenario.Local != nil, 15 * time.Minute, steps, fields,
		p.Load.VUs, p.Load.Iterations, p.Load.Rate, p.Load.RequestLimit, p.Load.Duration, p.Load.Pacing, p.Load.Stages}
	if p.Scenario.Timeout != "" {
		signature.Timeout, _ = time.ParseDuration(p.Scenario.Timeout)
	}
	body, err := json.Marshal(signature)
	if err != nil {
		return nil, fmt.Errorf("encode workload identity: %w", err)
	}
	digest := sha256.Sum256(body)
	return &testLoadWorkload{Version: 1, SHA256: hex.EncodeToString(digest[:]), Label: label}, nil
}

type testBaselineCheck struct {
	Step     string  `json:"step,omitempty"`
	Metric   string  `json:"metric"`
	Baseline float64 `json:"baseline"`
	Current  float64 `json:"current"`
	Limit    float64 `json:"limit"`
	Delta    float64 `json:"delta"`
	Passed   bool    `json:"passed"`
}

type testBaselineEvidence struct {
	Status       string              `json:"status"`
	ReportSHA256 string              `json:"report_sha256"`
	Checks       []testBaselineCheck `json:"checks,omitempty"`
	Error        string              `json:"error,omitempty"`
}

func compareTestBaseline(baseline, current *testLoadEvidence, cfg testRegressionConfig, reportSHA string) *testBaselineEvidence {
	result := &testBaselineEvidence{Status: "passed", ReportSHA256: reportSHA}
	add := func(step, metric string, before, after, limit float64, passed bool) {
		result.Checks = append(result.Checks, testBaselineCheck{Step: step, Metric: metric, Baseline: before, Current: after, Limit: limit, Delta: after - before, Passed: passed})
		if !passed {
			result.Status = "failed"
		}
	}
	compare := func(step string, before, after testLoadMetrics, limits testRegressionLimits) {
		add(step, "samples", float64(before.Latency.Samples), float64(after.Latency.Samples), float64(limits.MinSamples), before.Latency.Samples >= limits.MinSamples && after.Latency.Samples >= limits.MinSamples)
		p95Limit := before.Latency.P95MS + before.Latency.P95MS*limits.P95Percent/100
		add(step, "p95_ms", before.Latency.P95MS, after.Latency.P95MS, p95Limit, after.Latency.P95MS <= p95Limit)
		errorLimit := math.Min(1, before.ErrorRate+limits.ErrorRateIncrease)
		add(step, "error_rate", before.ErrorRate, after.ErrorRate, errorLimit, after.ErrorRate <= errorLimit)
	}
	compare("", baseline.testLoadMetrics, current.testLoadMetrics, cfg.testRegressionLimits)
	for i, step := range current.Steps {
		compare(step.Name, baseline.Steps[i].testLoadMetrics, step.testLoadMetrics, cfg.stepLimits(step.Name))
	}
	return result
}

func testBaselineFailure(result *testBaselineEvidence) string {
	var failures []string
	for _, check := range result.Checks {
		if check.Passed {
			continue
		}
		label := "aggregate"
		if check.Step != "" {
			label = "step " + check.Step
		}
		if check.Metric == "samples" {
			failures = append(failures, fmt.Sprintf("%s samples %.0f (baseline %.0f) need at least %.0f", label, check.Current, check.Baseline, check.Limit))
		} else {
			failures = append(failures, fmt.Sprintf("%s %s %.6g exceeds %.6g (baseline %.6g)", label, check.Metric, check.Current, check.Limit, check.Baseline))
		}
	}
	return "performance baseline failed: " + strings.Join(failures, "; ")
}

func compareTestStagedLoadRelative(baselineStep string, baseline, current *testLoadEvidence, spec *testLoadRelativeSpec) *testStagedLoadComparisonEvidence {
	result := &testStagedLoadComparisonEvidence{BaselineStep: baselineStep, Status: "passed"}
	add := func(step, baselineHTTPStep, currentHTTPStep, metric, operator string, before, after, limit float64, passed bool) {
		result.Checks = append(result.Checks, testStagedLoadComparisonCheck{
			Step: step, BaselineHTTPStep: baselineHTTPStep, CurrentHTTPStep: currentHTTPStep,
			Metric: metric, Operator: operator, Baseline: before, Current: after,
			Delta: after - before, Limit: limit, Passed: passed,
		})
		if !passed {
			result.Status = "failed"
		}
	}
	compareMetrics := func(step, baselineHTTPStep, currentHTTPStep string, before, after testLoadMetrics, minSamples int,
		p95IncreasePercent, p99IncreasePercent, errorRateIncrease, successRateDrop *float64) {
		add(step, baselineHTTPStep, currentHTTPStep, "samples", ">=", float64(before.Requests), float64(after.Requests), float64(minSamples),
			before.Requests >= minSamples && after.Requests >= minSamples)
		if p95IncreasePercent != nil {
			limit := before.Latency.P95MS * (1 + *p95IncreasePercent/100)
			add(step, baselineHTTPStep, currentHTTPStep, "p95_ms", "<=", before.Latency.P95MS, after.Latency.P95MS, limit, after.Latency.P95MS <= limit)
		}
		if p99IncreasePercent != nil {
			limit := before.Latency.P99MS * (1 + *p99IncreasePercent/100)
			add(step, baselineHTTPStep, currentHTTPStep, "p99_ms", "<=", before.Latency.P99MS, after.Latency.P99MS, limit, after.Latency.P99MS <= limit)
		}
		if errorRateIncrease != nil {
			limit := math.Min(1, before.ErrorRate+*errorRateIncrease)
			add(step, baselineHTTPStep, currentHTTPStep, "error_rate", "<=", before.ErrorRate, after.ErrorRate, limit, after.ErrorRate <= limit)
		}
		if successRateDrop != nil {
			limit := math.Max(0, before.SuccessRate-*successRateDrop)
			add(step, baselineHTTPStep, currentHTTPStep, "success_rate", ">=", before.SuccessRate, after.SuccessRate, limit, after.SuccessRate >= limit)
		}
	}
	compareMetrics("", "", "", baseline.testLoadMetrics, current.testLoadMetrics, spec.MinSamples,
		spec.P95IncreasePercent, spec.P99IncreasePercent, spec.ErrorRateIncrease, spec.SuccessRateDrop)
	stepNames := make([]string, 0, len(spec.Steps))
	for name := range spec.Steps {
		stepNames = append(stepNames, name)
	}
	sort.Strings(stepNames)
	for _, name := range stepNames {
		configured := spec.Steps[name]
		before, baselineFound := testLoadMetricsForStep(baseline, configured.BaselineStep)
		after, currentFound := testLoadMetricsForStep(current, configured.CurrentStep)
		if !baselineFound || !currentFound {
			result.Status = "failed"
			var message string
			if !baselineFound && !currentFound {
				message = fmt.Sprintf("baseline HTTP step %q and current HTTP step %q have no load evidence", configured.BaselineStep, configured.CurrentStep)
			} else if !baselineFound {
				message = fmt.Sprintf("baseline HTTP step %q has no load evidence", configured.BaselineStep)
			} else {
				message = fmt.Sprintf("current HTTP step %q has no load evidence", configured.CurrentStep)
			}
			result.Checks = append(result.Checks, testStagedLoadComparisonCheck{
				Step: name, BaselineHTTPStep: configured.BaselineStep, CurrentHTTPStep: configured.CurrentStep,
				Metric: "step_available", Operator: "==", Baseline: 1, Current: 0, Limit: 1, Passed: false, Error: message,
			})
			continue
		}
		minSamples := configured.MinSamples
		if minSamples == 0 {
			minSamples = spec.MinSamples
		}
		compareMetrics(name, configured.BaselineStep, configured.CurrentStep, before, after, minSamples,
			configured.P95IncreasePercent, configured.P99IncreasePercent, configured.ErrorRateIncrease, configured.SuccessRateDrop)
	}
	return result
}

func testLoadMetricsForStep(evidence *testLoadEvidence, name string) (testLoadMetrics, bool) {
	if evidence == nil {
		return testLoadMetrics{}, false
	}
	for _, step := range evidence.Steps {
		if step.Name == name {
			return step.testLoadMetrics, true
		}
	}
	return testLoadMetrics{}, false
}

func testStagedLoadComparisonFailure(result *testStagedLoadComparisonEvidence) string {
	var failures []string
	for _, check := range result.Checks {
		if !check.Passed {
			label := check.Metric
			if check.Step != "" {
				label = fmt.Sprintf("%s (%s → %s) %s", check.Step, check.BaselineHTTPStep, check.CurrentHTTPStep, check.Metric)
			}
			if check.Error != "" {
				failures = append(failures, label+": "+check.Error)
			} else {
				failures = append(failures, fmt.Sprintf("%s %.6g %s %.6g (baseline %.6g)", label, check.Current, check.Operator, check.Limit, check.Baseline))
			}
		}
	}
	return fmt.Sprintf("relative SLO against step %q failed: %s", result.BaselineStep, strings.Join(failures, "; "))
}

func testBenchmarkMetricsValid(metrics testLoadMetrics) error {
	if metrics.Requests < 0 || metrics.Requests > testLoadMaxRequests || metrics.Failures < 0 || metrics.Failures > metrics.Requests || metrics.Latency.Samples != metrics.Requests {
		return errors.New("inconsistent request, failure, or sample counts")
	}
	if !finiteNonnegative(metrics.ErrorRate) || metrics.ErrorRate > 1 {
		return errors.New("invalid error rate")
	}
	wantRate := 0.0
	if metrics.Requests > 0 {
		wantRate = float64(metrics.Failures) / float64(metrics.Requests)
	}
	if math.Abs(metrics.ErrorRate-wantRate) > 1e-12 {
		return errors.New("error rate does not match request and failure counts")
	}
	l := metrics.Latency
	if l.MaxMS > loadMilliseconds(testLoadMaxDuration+testLoadDrainTimeout) {
		return errors.New("latency exceeds the native load execution limit")
	}
	for _, value := range []float64{l.MinMS, l.MeanMS, l.P50MS, l.P95MS, l.P99MS, l.MaxMS} {
		if !finiteNonnegative(value) {
			return errors.New("latency metrics must be finite and nonnegative")
		}
	}
	// Summing many identical floating-point millisecond values can put their
	// mean a few ulps outside min/max. Preserve valid collector summaries.
	epsilon := math.Max(1e-9, l.MaxMS*1e-9)
	if !sort.Float64sAreSorted([]float64{l.MinMS, l.P50MS, l.P95MS, l.P99MS, l.MaxMS}) || l.MeanMS < l.MinMS-epsilon || l.MeanMS > l.MaxMS+epsilon {
		return errors.New("inconsistent latency percentiles")
	}
	return nil
}
