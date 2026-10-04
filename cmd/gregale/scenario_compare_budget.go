package main

import (
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const testComparisonBudgetFileLimit = 1 << 20

type testComparisonBudgetSpec struct {
	Version int `yaml:"version"`
	Runs    struct {
		RequirePassing             *bool    `yaml:"require_passing,omitempty"`
		RequireSameRuns            bool     `yaml:"require_same_runs,omitempty"`
		MaxDurationIncreasePercent *float64 `yaml:"max_duration_increase_percent,omitempty"`
	} `yaml:"runs,omitempty"`
	HTTP struct {
		MaxDurationIncreasePercent *float64 `yaml:"max_duration_increase_percent,omitempty"`
	} `yaml:"http,omitempty"`
	Load struct {
		MaxP95IncreasePercent                *float64 `yaml:"max_p95_increase_percent,omitempty"`
		MaxP99IncreasePercent                *float64 `yaml:"max_p99_increase_percent,omitempty"`
		MaxErrorRateIncreasePercentagePoints *float64 `yaml:"max_error_rate_increase_percentage_points,omitempty"`
		MinSamples                           *int     `yaml:"min_samples,omitempty"`
	} `yaml:"load,omitempty"`
	Repeat *struct {
		MinRuns                            *int     `yaml:"min_runs,omitempty"`
		MaxSpreadPercent                   *float64 `yaml:"max_spread_percent,omitempty"`
		MaxErrorRateSpreadPercentagePoints *float64 `yaml:"max_error_rate_spread_percentage_points,omitempty"`
	} `yaml:"repeat,omitempty"`
}

type testComparisonBudget struct {
	RequirePassing              bool
	RequireSameRuns             bool
	RunDurationIncreasePercent  *float64
	HTTPDurationIncreasePercent *float64
	LoadP95IncreasePercent      *float64
	LoadP99IncreasePercent      *float64
	LoadErrorRateIncreasePoints *float64
	LoadMinSamples              int
	Repeat                      *testComparisonRepeatBudget
}

type testComparisonRepeatBudget struct {
	MinRuns                            int
	MaxSpreadPercent                   float64
	MaxErrorRateSpreadPercentagePoints float64
}

type testComparisonGate struct {
	Status       string                    `json:"status"`
	Passed       int                       `json:"passed"`
	Failed       int                       `json:"failed"`
	Inconclusive int                       `json:"inconclusive"`
	Checks       []testComparisonGateCheck `json:"checks,omitempty"`
}

type testComparisonGateCheck struct {
	Run          string   `json:"run"`
	Metric       string   `json:"metric"`
	Status       string   `json:"status"`
	Unit         string   `json:"unit,omitempty"`
	Before       *float64 `json:"before,omitempty"`
	After        *float64 `json:"after,omitempty"`
	Limit        *float64 `json:"limit,omitempty"`
	BeforeRuns   int      `json:"before_runs,omitempty"`
	AfterRuns    int      `json:"after_runs,omitempty"`
	BeforeSpread *float64 `json:"before_spread,omitempty"`
	AfterSpread  *float64 `json:"after_spread,omitempty"`
	SpreadUnit   string   `json:"spread_unit,omitempty"`
	Reason       string   `json:"reason,omitempty"`
}

func readTestComparisonBudget(path string) (testComparisonBudget, error) {
	file, err := openCustomerFile(path)
	if err != nil {
		return testComparisonBudget{}, fmt.Errorf("open comparison budget: %w", err)
	}
	defer func() { _ = file.Close() }()
	body, err := io.ReadAll(io.LimitReader(file, testComparisonBudgetFileLimit+1))
	if err != nil {
		return testComparisonBudget{}, fmt.Errorf("read comparison budget: %w", err)
	}
	if len(body) > testComparisonBudgetFileLimit {
		return testComparisonBudget{}, fmt.Errorf("comparison budget exceeds %d bytes", testComparisonBudgetFileLimit)
	}
	decoder := yaml.NewDecoder(strings.NewReader(string(body)))
	decoder.KnownFields(true)
	var spec testComparisonBudgetSpec
	if err := decoder.Decode(&spec); err != nil {
		return testComparisonBudget{}, fmt.Errorf("decode comparison budget: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return testComparisonBudget{}, errors.New("comparison budget must contain one YAML document")
		}
		return testComparisonBudget{}, fmt.Errorf("decode comparison budget: %w", err)
	}
	return validateTestComparisonBudget(spec)
}

func validateTestComparisonBudget(spec testComparisonBudgetSpec) (testComparisonBudget, error) {
	if spec.Version != 1 {
		return testComparisonBudget{}, errors.New("comparison budget version must be 1")
	}
	for _, item := range []struct {
		name  string
		value *float64
	}{
		{"runs.max_duration_increase_percent", spec.Runs.MaxDurationIncreasePercent},
		{"http.max_duration_increase_percent", spec.HTTP.MaxDurationIncreasePercent},
		{"load.max_p95_increase_percent", spec.Load.MaxP95IncreasePercent},
		{"load.max_p99_increase_percent", spec.Load.MaxP99IncreasePercent},
	} {
		if item.value != nil && (!finiteNonnegative(*item.value) || *item.value > 1000) {
			return testComparisonBudget{}, fmt.Errorf("%s must be a finite percent between 0 and 1000", item.name)
		}
	}
	if value := spec.Load.MaxErrorRateIncreasePercentagePoints; value != nil && (!finiteNonnegative(*value) || *value > 100) {
		return testComparisonBudget{}, errors.New("load.max_error_rate_increase_percentage_points must be between 0 and 100")
	}
	if value := spec.Load.MinSamples; value != nil && (*value < 1 || *value > testLoadMaxRequests) {
		return testComparisonBudget{}, fmt.Errorf("load.min_samples must be between 1 and %d", testLoadMaxRequests)
	}
	var repeat *testComparisonRepeatBudget
	if spec.Repeat != nil {
		if spec.Repeat.MinRuns == nil || *spec.Repeat.MinRuns < 2 || *spec.Repeat.MinRuns > 20 {
			return testComparisonBudget{}, errors.New("repeat.min_runs must be between 2 and 20")
		}
		maxSpread := 25.0
		if spec.Repeat.MaxSpreadPercent != nil {
			maxSpread = *spec.Repeat.MaxSpreadPercent
		}
		if !finiteNonnegative(maxSpread) || maxSpread > 1000 {
			return testComparisonBudget{}, errors.New("repeat.max_spread_percent must be a finite percent between 0 and 1000")
		}
		maxErrorRateSpread := 1.0
		if spec.Repeat.MaxErrorRateSpreadPercentagePoints != nil {
			maxErrorRateSpread = *spec.Repeat.MaxErrorRateSpreadPercentagePoints
		}
		if !finiteNonnegative(maxErrorRateSpread) || maxErrorRateSpread > 100 {
			return testComparisonBudget{}, errors.New("repeat.max_error_rate_spread_percentage_points must be between 0 and 100")
		}
		repeat = &testComparisonRepeatBudget{
			MinRuns: *spec.Repeat.MinRuns, MaxSpreadPercent: maxSpread,
			MaxErrorRateSpreadPercentagePoints: maxErrorRateSpread,
		}
	}
	requirePassing := true
	if spec.Runs.RequirePassing != nil {
		requirePassing = *spec.Runs.RequirePassing
	}
	budget := testComparisonBudget{
		RequirePassing:              requirePassing,
		RequireSameRuns:             spec.Runs.RequireSameRuns,
		RunDurationIncreasePercent:  spec.Runs.MaxDurationIncreasePercent,
		HTTPDurationIncreasePercent: spec.HTTP.MaxDurationIncreasePercent,
		LoadP95IncreasePercent:      spec.Load.MaxP95IncreasePercent,
		LoadP99IncreasePercent:      spec.Load.MaxP99IncreasePercent,
		LoadErrorRateIncreasePoints: spec.Load.MaxErrorRateIncreasePercentagePoints,
		LoadMinSamples:              20,
		Repeat:                      repeat,
	}
	if spec.Load.MinSamples != nil {
		budget.LoadMinSamples = *spec.Load.MinSamples
	}
	return budget, nil
}

func evaluateTestComparisonBudget(comparison testReportComparison, before, after []testRunReceipt, budget testComparisonBudget) (testComparisonGate, error) {
	if budget.Repeat != nil {
		return evaluateTestComparisonRepeatBudget(comparison, before, after, budget)
	}
	beforeIndex, err := indexTestComparisonReceipts(before, "earlier")
	if err != nil {
		return testComparisonGate{}, err
	}
	afterIndex, err := indexTestComparisonReceipts(after, "later")
	if err != nil {
		return testComparisonGate{}, err
	}
	gate := testComparisonGate{Status: "passed"}
	for _, run := range comparison.Runs {
		key := testComparisonKey{Scenario: run.Scenario, Engine: run.Engine, Profile: run.Profile, Case: run.Case, Attempt: run.Attempt}
		oldReceipt, hasBefore := beforeIndex[key]
		newReceipt, hasAfter := afterIndex[key]
		if budget.RequireSameRuns && (!hasBefore || !hasAfter) {
			gate.add(testComparisonGateCheck{Run: run.Key, Metric: "run.identity", Status: "failed", Reason: "run exists in only one report"})
		}
		if !hasAfter {
			if budget.RunDurationIncreasePercent != nil {
				gate.addInconclusive(run.Key, "run.duration_ms", "run is missing from the later report")
			}
			if budget.HTTPDurationIncreasePercent != nil {
				addTestHTTPBudgetChecks(&gate, run.Key, oldReceipt.Requests, nil, *budget.HTTPDurationIncreasePercent)
			}
			if budget.hasLoadBudgets() {
				addTestLoadBudgetChecks(&gate, run.Key, oldReceipt.Load, nil, budget)
			}
			continue
		}
		if budget.RequirePassing {
			status := "passed"
			reason := testNonpassingRunReason(newReceipt)
			if reason != "" {
				status = "failed"
			} else if hasBefore {
				reason = fmt.Sprintf("earlier status %s; later status %s", oldReceipt.Status, newReceipt.Status)
			} else {
				reason = "new run status " + newReceipt.Status
			}
			gate.add(testComparisonGateCheck{Run: run.Key, Metric: "run.status", Status: status, Reason: reason})
		}
		if !hasBefore {
			if budget.RunDurationIncreasePercent != nil {
				gate.addInconclusive(run.Key, "run.duration_ms", "new run has no earlier duration")
			}
			if budget.HTTPDurationIncreasePercent != nil {
				addTestHTTPBudgetChecks(&gate, run.Key, nil, newReceipt.Requests, *budget.HTTPDurationIncreasePercent)
			}
			if budget.hasLoadBudgets() {
				addTestLoadBudgetChecks(&gate, run.Key, nil, newReceipt.Load, budget)
			}
			continue
		}
		if budget.RunDurationIncreasePercent != nil {
			gate.add(compareTestGateMetric(run.Key, "run.duration_ms", "ms", float64(oldReceipt.DurationMS), float64(newReceipt.DurationMS), *budget.RunDurationIncreasePercent))
		}
		if budget.HTTPDurationIncreasePercent != nil {
			addTestHTTPBudgetChecks(&gate, run.Key, oldReceipt.Requests, newReceipt.Requests, *budget.HTTPDurationIncreasePercent)
		}
		if budget.hasLoadBudgets() {
			addTestLoadBudgetChecks(&gate, run.Key, oldReceipt.Load, newReceipt.Load, budget)
		}
	}
	return gate, nil
}

func (budget testComparisonBudget) hasLoadBudgets() bool {
	return budget.LoadP95IncreasePercent != nil || budget.LoadP99IncreasePercent != nil || budget.LoadErrorRateIncreasePoints != nil
}

func testNonpassingRunReason(receipt testRunReceipt) string {
	var reasons []string
	if receipt.Status != "passed" {
		reasons = append(reasons, "run status is "+receipt.Status)
	}
	if receipt.Error != "" {
		reasons = append(reasons, receipt.Error)
	}
	if receipt.CleanupError != "" {
		reasons = append(reasons, "cleanup failed: "+receipt.CleanupError)
	}
	if receipt.Load != nil && receipt.Load.Status != "passed" {
		reasons = append(reasons, "load status is "+receipt.Load.Status)
	}
	if receipt.Baseline != nil && receipt.Baseline.Status != "passed" {
		reasons = append(reasons, "baseline status is "+receipt.Baseline.Status)
	}
	if receipt.Status != "passed" && receipt.SkipReason != "" {
		reasons = append(reasons, receipt.SkipReason)
	}
	return strings.Join(reasons, "; ")
}

func compareTestGateMetric(run, metric, unit string, before, after, maxIncreasePercent float64) testComparisonGateCheck {
	if !finiteNonnegative(before) || !finiteNonnegative(after) {
		return testComparisonGateCheck{Run: run, Metric: metric, Status: "inconclusive", Unit: unit, Reason: "metric is not finite and nonnegative"}
	}
	limit := before + before*maxIncreasePercent/100
	passed := after <= limit
	status := "passed"
	if !passed {
		status = "failed"
	}
	return testComparisonGateCheck{Run: run, Metric: metric, Status: status, Unit: unit, Before: &before, After: &after, Limit: &limit}
}

func compareTestGateErrorRate(run, metric string, before, after, maxIncreasePoints float64) testComparisonGateCheck {
	if !finiteNonnegative(before) || !finiteNonnegative(after) || before > 1 || after > 1 {
		return testComparisonGateCheck{Run: run, Metric: metric, Status: "inconclusive", Unit: "%", Reason: "error rate is outside 0..1"}
	}
	beforePercent, afterPercent := before*100, after*100
	limit := math.Min(100, beforePercent+maxIncreasePoints)
	passed := afterPercent <= limit
	status := "passed"
	if !passed {
		status = "failed"
	}
	return testComparisonGateCheck{Run: run, Metric: metric, Status: status, Unit: "%", Before: &beforePercent, After: &afterPercent, Limit: &limit}
}

func addTestHTTPBudgetChecks(gate *testComparisonGate, run string, before, after []testHTTPRequestEvidence, maxIncreasePercent float64) {
	oldByName, oldErr := indexTestHTTPRequestEvidence(before)
	newByName, newErr := indexTestHTTPRequestEvidence(after)
	if oldErr != nil || newErr != nil {
		reason := "duplicate or empty HTTP step name in report"
		gate.addInconclusive(run, "http.duration_ms", reason)
		return
	}
	if len(oldByName) == 0 && len(newByName) == 0 {
		gate.addInconclusive(run, "http.duration_ms", "reports have no HTTP request timings")
		return
	}
	for _, name := range sortedComparisonNames(oldByName) {
		old := oldByName[name]
		current, ok := newByName[name]
		if !ok {
			gate.addInconclusive(run, "http."+name+".duration_ms", "HTTP step is missing from the later report")
			continue
		}
		if old.Method != current.Method || old.Path != current.Path {
			gate.addInconclusive(run, "http."+name+".duration_ms", "HTTP step method or path changed")
			continue
		}
		check := compareTestGateMetric(run, "http."+name+".duration_ms", "ms", float64(old.DurationMS), float64(current.DurationMS), maxIncreasePercent)
		gate.add(check)
	}
	for _, name := range sortedComparisonNames(newByName) {
		if _, ok := oldByName[name]; !ok {
			gate.addInconclusive(run, "http."+name+".duration_ms", "HTTP step is new and has no earlier duration")
		}
	}
}

func indexTestHTTPRequestEvidence(steps []testHTTPRequestEvidence) (map[string]testHTTPRequestEvidence, error) {
	index := make(map[string]testHTTPRequestEvidence, len(steps))
	for _, step := range steps {
		if strings.TrimSpace(step.Name) == "" {
			return nil, errors.New("empty HTTP step name")
		}
		if _, exists := index[step.Name]; exists {
			return nil, errors.New("duplicate HTTP step name")
		}
		index[step.Name] = step
	}
	return index, nil
}

func addTestLoadBudgetChecks(gate *testComparisonGate, run string, before, after *testLoadEvidence, budget testComparisonBudget) {
	if before == nil || after == nil {
		gate.addInconclusive(run, "load", "load evidence is present in only one report")
		return
	}
	if before.Workload == nil || after.Workload == nil || before.Workload.Version != 1 || after.Workload.Version != 1 || before.Workload.SHA256 == "" || after.Workload.SHA256 == "" {
		gate.addInconclusive(run, "load.workload", "report lacks supported workload identity metadata")
		return
	}
	if before.Workload.SHA256 != after.Workload.SHA256 || before.Workload.Label != after.Workload.Label {
		gate.addInconclusive(run, "load.workload", "workload identity changed; load metrics are not comparable")
		return
	}
	if err := testBenchmarkMetricsValid(before.testLoadMetrics); err != nil {
		gate.addInconclusive(run, "load.metrics", "earlier load metrics are invalid: "+err.Error())
		return
	}
	if err := testBenchmarkMetricsValid(after.testLoadMetrics); err != nil {
		gate.addInconclusive(run, "load.metrics", "later load metrics are invalid: "+err.Error())
		return
	}
	if before.Latency.Samples < budget.LoadMinSamples || after.Latency.Samples < budget.LoadMinSamples {
		gate.addInconclusive(run, "load.samples", fmt.Sprintf("need at least %d samples in both reports; earlier %d, later %d", budget.LoadMinSamples, before.Latency.Samples, after.Latency.Samples))
		return
	}
	if budget.LoadP95IncreasePercent != nil {
		gate.add(compareTestGateMetric(run, "load.p95_latency", "ms", before.Latency.P95MS, after.Latency.P95MS, *budget.LoadP95IncreasePercent))
	}
	if budget.LoadP99IncreasePercent != nil {
		gate.add(compareTestGateMetric(run, "load.p99_latency", "ms", before.Latency.P99MS, after.Latency.P99MS, *budget.LoadP99IncreasePercent))
	}
	if budget.LoadErrorRateIncreasePoints != nil {
		gate.add(compareTestGateErrorRate(run, "load.error_rate", before.ErrorRate, after.ErrorRate, *budget.LoadErrorRateIncreasePoints))
	}
	if budget.LoadP95IncreasePercent != nil || budget.LoadP99IncreasePercent != nil || budget.LoadErrorRateIncreasePoints != nil {
		addTestLoadStepBudgetChecks(gate, run, before.Steps, after.Steps, budget)
	}
}

func addTestLoadStepBudgetChecks(gate *testComparisonGate, run string, before, after []testLoadStepEvidence, budget testComparisonBudget) {
	oldByName, oldErr := indexTestLoadStepEvidence(before)
	newByName, newErr := indexTestLoadStepEvidence(after)
	if oldErr != nil || newErr != nil {
		gate.addInconclusive(run, "load.steps", "duplicate or empty load step name in report")
		return
	}
	for _, name := range sortedComparisonNames(oldByName) {
		old := oldByName[name]
		current, ok := newByName[name]
		if !ok {
			gate.addInconclusive(run, "load."+name, "load step is missing from the later report")
			continue
		}
		if old.Method != current.Method || old.Path != current.Path {
			gate.addInconclusive(run, "load."+name, "load step method or path changed")
			continue
		}
		if err := testBenchmarkMetricsValid(old.testLoadMetrics); err != nil {
			gate.addInconclusive(run, "load."+name, "earlier step metrics are invalid: "+err.Error())
			continue
		}
		if err := testBenchmarkMetricsValid(current.testLoadMetrics); err != nil {
			gate.addInconclusive(run, "load."+name, "later step metrics are invalid: "+err.Error())
			continue
		}
		if old.Latency.Samples < budget.LoadMinSamples || current.Latency.Samples < budget.LoadMinSamples {
			gate.addInconclusive(run, "load."+name+".samples", fmt.Sprintf("need at least %d samples in both reports; earlier %d, later %d", budget.LoadMinSamples, old.Latency.Samples, current.Latency.Samples))
			continue
		}
		if budget.LoadP95IncreasePercent != nil {
			gate.add(compareTestGateMetric(run, "load."+name+".p95_latency", "ms", old.Latency.P95MS, current.Latency.P95MS, *budget.LoadP95IncreasePercent))
		}
		if budget.LoadP99IncreasePercent != nil {
			gate.add(compareTestGateMetric(run, "load."+name+".p99_latency", "ms", old.Latency.P99MS, current.Latency.P99MS, *budget.LoadP99IncreasePercent))
		}
		if budget.LoadErrorRateIncreasePoints != nil {
			gate.add(compareTestGateErrorRate(run, "load."+name+".error_rate", old.ErrorRate, current.ErrorRate, *budget.LoadErrorRateIncreasePoints))
		}
	}
	for _, name := range sortedComparisonNames(newByName) {
		if _, ok := oldByName[name]; !ok {
			gate.addInconclusive(run, "load."+name, "load step is new and has no earlier metrics")
		}
	}
}

func sortedComparisonNames[T any](index map[string]T) []string {
	names := make([]string, 0, len(index))
	for name := range index {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func indexTestLoadStepEvidence(steps []testLoadStepEvidence) (map[string]testLoadStepEvidence, error) {
	index := make(map[string]testLoadStepEvidence, len(steps))
	for _, step := range steps {
		if strings.TrimSpace(step.Name) == "" {
			return nil, errors.New("empty load step name")
		}
		if _, exists := index[step.Name]; exists {
			return nil, errors.New("duplicate load step name")
		}
		index[step.Name] = step
	}
	return index, nil
}

func (gate *testComparisonGate) addInconclusive(run, metric, reason string) {
	gate.add(testComparisonGateCheck{Run: run, Metric: metric, Status: "inconclusive", Reason: reason})
}

func (gate *testComparisonGate) add(check testComparisonGateCheck) {
	if check.Status == "passed" {
		gate.Passed++
	} else {
		gate.Status = "failed"
		if check.Status == "inconclusive" {
			gate.Inconclusive++
		} else {
			gate.Failed++
		}
	}
	gate.Checks = append(gate.Checks, check)
}
