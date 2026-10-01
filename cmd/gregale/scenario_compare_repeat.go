package main

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

type testComparisonRepeatKey struct {
	Scenario string
	Engine   string
	Profile  string
	Case     string
}

func evaluateTestComparisonRepeatBudget(comparison testReportComparison, before, after []testRunReceipt, budget testComparisonBudget) (testComparisonGate, error) {
	beforeAttempts, err := indexTestComparisonReceipts(before, "earlier")
	if err != nil {
		return testComparisonGate{}, err
	}
	afterAttempts, err := indexTestComparisonReceipts(after, "later")
	if err != nil {
		return testComparisonGate{}, err
	}
	beforeGroups := groupTestComparisonReceipts(before)
	afterGroups := groupTestComparisonReceipts(after)
	gate := testComparisonGate{Status: "passed"}
	for _, run := range comparison.Runs {
		key := testComparisonKey{Scenario: run.Scenario, Engine: run.Engine, Profile: run.Profile, Case: run.Case, Attempt: run.Attempt}
		oldReceipt, hasBefore := beforeAttempts[key]
		newReceipt, hasAfter := afterAttempts[key]
		if budget.RequireSameRuns && (!hasBefore || !hasAfter) {
			gate.add(testComparisonGateCheck{Run: run.Key, Metric: "run.identity", Status: "failed", Reason: "run exists in only one report"})
		}
		if !hasAfter || !budget.RequirePassing {
			continue
		}
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
	if !budget.hasPerformanceBudgets() {
		return gate, nil
	}

	keys := make(map[testComparisonRepeatKey]bool, len(beforeGroups)+len(afterGroups))
	for key := range beforeGroups {
		keys[key] = true
	}
	for key := range afterGroups {
		keys[key] = true
	}
	ordered := make([]testComparisonRepeatKey, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	sort.Slice(ordered, func(i, j int) bool {
		return testComparisonRepeatKeyString(ordered[i]) < testComparisonRepeatKeyString(ordered[j])
	})
	for _, key := range ordered {
		name := testComparisonRepeatKeyString(key)
		oldRuns, hasBefore := beforeGroups[key]
		newRuns, hasAfter := afterGroups[key]
		if !hasBefore || !hasAfter {
			gate.addInconclusive(name, "repeat.group", "comparison group is present in only one report")
			continue
		}
		if len(oldRuns) < budget.Repeat.MinRuns || len(newRuns) < budget.Repeat.MinRuns {
			gate.add(testComparisonGateCheck{
				Run: name, Metric: "repeat.count", Status: "inconclusive",
				BeforeRuns: len(oldRuns), AfterRuns: len(newRuns),
				Reason: fmt.Sprintf("need at least %d attempts in each report", budget.Repeat.MinRuns),
			})
			continue
		}
		addTestRepeatedRunDurationCheck(&gate, name, oldRuns, newRuns, budget)
		if budget.HTTPDurationIncreasePercent != nil {
			addTestRepeatedHTTPChecks(&gate, name, oldRuns, newRuns, budget)
		}
		if budget.hasLoadBudgets() {
			addTestRepeatedLoadChecks(&gate, name, oldRuns, newRuns, budget)
		}
	}
	return gate, nil
}

func (budget testComparisonBudget) hasPerformanceBudgets() bool {
	return budget.RunDurationIncreasePercent != nil || budget.HTTPDurationIncreasePercent != nil || budget.hasLoadBudgets()
}

func groupTestComparisonReceipts(receipts []testRunReceipt) map[testComparisonRepeatKey][]testRunReceipt {
	groups := make(map[testComparisonRepeatKey][]testRunReceipt)
	for _, receipt := range receipts {
		key := testComparisonRepeatKey{Scenario: receipt.Scenario, Engine: receipt.Engine, Profile: receipt.Profile, Case: receipt.Case}
		groups[key] = append(groups[key], receipt)
	}
	for key := range groups {
		sort.Slice(groups[key], func(i, j int) bool { return groups[key][i].Attempt < groups[key][j].Attempt })
	}
	return groups
}

func testComparisonRepeatKeyString(key testComparisonRepeatKey) string {
	name := fmt.Sprintf("%s/%s/%s", key.Scenario, key.Engine, key.Profile)
	if key.Case != "" {
		name += "/" + key.Case
	}
	return name
}

func addTestRepeatedRunDurationCheck(gate *testComparisonGate, name string, before, after []testRunReceipt, budget testComparisonBudget) {
	if budget.RunDurationIncreasePercent == nil {
		return
	}
	oldValues := make([]float64, len(before))
	newValues := make([]float64, len(after))
	for i := range before {
		oldValues[i] = float64(before[i].DurationMS)
	}
	for i := range after {
		newValues[i] = float64(after[i].DurationMS)
	}
	gate.add(compareRepeatedTestGateMetric(name, "run.duration_ms", "ms", oldValues, newValues, *budget.RunDurationIncreasePercent, budget.Repeat.MaxSpreadPercent, budget.Repeat.MinRuns))
}

func addTestRepeatedHTTPChecks(gate *testComparisonGate, name string, before, after []testRunReceipt, budget testComparisonBudget) {
	oldSteps, oldErr := repeatedTestHTTPSteps(before)
	newSteps, newErr := repeatedTestHTTPSteps(after)
	if oldErr != nil || newErr != nil {
		gate.addInconclusive(name, "http.steps", "repeated attempts have missing, duplicate, or inconsistent HTTP steps")
		return
	}
	names := make(map[string]bool, len(oldSteps)+len(newSteps))
	for step := range oldSteps {
		names[step] = true
	}
	for step := range newSteps {
		names[step] = true
	}
	ordered := make([]string, 0, len(names))
	for step := range names {
		ordered = append(ordered, step)
	}
	sort.Strings(ordered)
	for _, step := range ordered {
		old, hasOld := oldSteps[step]
		current, hasCurrent := newSteps[step]
		if !hasOld || !hasCurrent {
			gate.addInconclusive(name, "http."+step+".duration_ms", "HTTP step is present in only one report")
			continue
		}
		if old.Method != current.Method || old.Path != current.Path {
			gate.addInconclusive(name, "http."+step+".duration_ms", "HTTP step method or path changed")
			continue
		}
		oldValues := make([]float64, len(old.Durations))
		newValues := make([]float64, len(current.Durations))
		for i, value := range old.Durations {
			oldValues[i] = float64(value)
		}
		for i, value := range current.Durations {
			newValues[i] = float64(value)
		}
		gate.add(compareRepeatedTestGateMetric(name, "http."+step+".duration_ms", "ms", oldValues, newValues, *budget.HTTPDurationIncreasePercent, budget.Repeat.MaxSpreadPercent, budget.Repeat.MinRuns))
	}
}

type testRepeatedHTTPStep struct {
	Method    string
	Path      string
	Durations []int64
}

func repeatedTestHTTPSteps(receipts []testRunReceipt) (map[string]testRepeatedHTTPStep, error) {
	stepsByRun := make([]map[string]testHTTPRequestEvidence, len(receipts))
	for i, receipt := range receipts {
		steps, err := indexTestHTTPRequestEvidence(receipt.Requests)
		if err != nil {
			return nil, err
		}
		stepsByRun[i] = steps
	}
	if len(stepsByRun) == 0 || len(stepsByRun[0]) == 0 {
		return nil, fmt.Errorf("no HTTP evidence")
	}
	result := make(map[string]testRepeatedHTTPStep, len(stepsByRun[0]))
	for name, first := range stepsByRun[0] {
		result[name] = testRepeatedHTTPStep{Method: first.Method, Path: first.Path, Durations: make([]int64, 0, len(receipts))}
	}
	for _, steps := range stepsByRun {
		if len(steps) != len(result) {
			return nil, fmt.Errorf("HTTP step set changed")
		}
		for name, step := range steps {
			group, exists := result[name]
			if !exists || group.Method != step.Method || group.Path != step.Path {
				return nil, fmt.Errorf("HTTP step identity changed")
			}
			group.Durations = append(group.Durations, step.DurationMS)
			result[name] = group
		}
	}
	return result, nil
}

func addTestRepeatedLoadChecks(gate *testComparisonGate, name string, before, after []testRunReceipt, budget testComparisonBudget) {
	oldLoads, oldErr := repeatedTestLoads(before, budget.LoadMinSamples)
	newLoads, newErr := repeatedTestLoads(after, budget.LoadMinSamples)
	if oldErr != nil || newErr != nil {
		reason := "load evidence is missing or invalid in a repeated attempt"
		if oldErr != nil {
			reason = "earlier " + oldErr.Error()
		} else if newErr != nil {
			reason = "later " + newErr.Error()
		}
		gate.addInconclusive(name, "load", reason)
		return
	}
	workload := oldLoads[0].Workload
	for _, load := range append(append([]*testLoadEvidence{}, oldLoads...), newLoads...) {
		if load.Workload.Version != workload.Version || load.Workload.SHA256 != workload.SHA256 || load.Workload.Label != workload.Label {
			gate.addInconclusive(name, "load.workload", "workload identity changed within or between repeated comparisons")
			return
		}
	}
	addTestRepeatedLoadMetric(gate, name, "load.p95_latency", "ms", oldLoads, newLoads, func(load *testLoadEvidence) float64 { return load.Latency.P95MS }, budget.LoadP95IncreasePercent, budget)
	addTestRepeatedLoadMetric(gate, name, "load.p99_latency", "ms", oldLoads, newLoads, func(load *testLoadEvidence) float64 { return load.Latency.P99MS }, budget.LoadP99IncreasePercent, budget)
	addTestRepeatedLoadMetric(gate, name, "load.error_rate", "%", oldLoads, newLoads, func(load *testLoadEvidence) float64 { return load.ErrorRate }, budget.LoadErrorRateIncreasePoints, budget)
	if budget.LoadP95IncreasePercent == nil && budget.LoadP99IncreasePercent == nil && budget.LoadErrorRateIncreasePoints == nil {
		return
	}
	oldSteps, oldErr := repeatedTestLoadSteps(oldLoads, budget.LoadMinSamples)
	newSteps, newErr := repeatedTestLoadSteps(newLoads, budget.LoadMinSamples)
	if oldErr != nil || newErr != nil {
		gate.addInconclusive(name, "load.steps", "repeated load attempts have missing or inconsistent steps")
		return
	}
	stepNames := make(map[string]bool, len(oldSteps)+len(newSteps))
	for step := range oldSteps {
		stepNames[step] = true
	}
	for step := range newSteps {
		stepNames[step] = true
	}
	ordered := make([]string, 0, len(stepNames))
	for step := range stepNames {
		ordered = append(ordered, step)
	}
	sort.Strings(ordered)
	for _, step := range ordered {
		old, hasOld := oldSteps[step]
		current, hasCurrent := newSteps[step]
		if !hasOld || !hasCurrent {
			gate.addInconclusive(name, "load."+step, "load step is present in only one report")
			continue
		}
		if old.Method != current.Method || old.Path != current.Path {
			gate.addInconclusive(name, "load."+step, "load step method or path changed")
			continue
		}
		addTestRepeatedLoadStepMetric(gate, name, step, "p95_latency", "ms", old.Steps, current.Steps, func(item testLoadStepEvidence) float64 { return item.Latency.P95MS }, budget.LoadP95IncreasePercent, budget)
		addTestRepeatedLoadStepMetric(gate, name, step, "p99_latency", "ms", old.Steps, current.Steps, func(item testLoadStepEvidence) float64 { return item.Latency.P99MS }, budget.LoadP99IncreasePercent, budget)
		addTestRepeatedLoadStepMetric(gate, name, step, "error_rate", "%", old.Steps, current.Steps, func(item testLoadStepEvidence) float64 { return item.ErrorRate }, budget.LoadErrorRateIncreasePoints, budget)
	}
}

func repeatedTestLoads(receipts []testRunReceipt, minSamples int) ([]*testLoadEvidence, error) {
	loads := make([]*testLoadEvidence, 0, len(receipts))
	for _, receipt := range receipts {
		load := receipt.Load
		if load == nil {
			return nil, fmt.Errorf("load evidence is missing")
		}
		if load.Workload == nil || load.Workload.Version != 1 || load.Workload.SHA256 == "" {
			return nil, fmt.Errorf("report lacks supported workload identity metadata")
		}
		if err := testBenchmarkMetricsValid(load.testLoadMetrics); err != nil {
			return nil, fmt.Errorf("load metrics are invalid: %w", err)
		}
		if load.Latency.Samples < minSamples {
			return nil, fmt.Errorf("need at least %d samples in every attempt; found %d", minSamples, load.Latency.Samples)
		}
		loads = append(loads, load)
	}
	return loads, nil
}

func addTestRepeatedLoadMetric(gate *testComparisonGate, name, metric, unit string, before, after []*testLoadEvidence, value func(*testLoadEvidence) float64, increaseLimit *float64, budget testComparisonBudget) {
	if increaseLimit == nil {
		return
	}
	oldValues := make([]float64, len(before))
	newValues := make([]float64, len(after))
	for i, load := range before {
		oldValues[i] = value(load)
	}
	for i, load := range after {
		newValues[i] = value(load)
	}
	if metric == "load.error_rate" {
		gate.add(compareRepeatedTestGateErrorRate(name, metric, oldValues, newValues, *increaseLimit, budget.Repeat.MaxErrorRateSpreadPercentagePoints, budget.Repeat.MinRuns))
		return
	}
	gate.add(compareRepeatedTestGateMetric(name, metric, unit, oldValues, newValues, *increaseLimit, budget.Repeat.MaxSpreadPercent, budget.Repeat.MinRuns))
}

type testRepeatedLoadStep struct {
	Method string
	Path   string
	Steps  []testLoadStepEvidence
}

func repeatedTestLoadSteps(loads []*testLoadEvidence, minSamples int) (map[string]testRepeatedLoadStep, error) {
	if len(loads) == 0 {
		return nil, fmt.Errorf("no load evidence")
	}
	first, err := indexTestLoadStepEvidence(loads[0].Steps)
	if err != nil || len(first) == 0 {
		return nil, fmt.Errorf("invalid load step evidence")
	}
	result := make(map[string]testRepeatedLoadStep, len(first))
	for name, step := range first {
		result[name] = testRepeatedLoadStep{Method: step.Method, Path: step.Path, Steps: make([]testLoadStepEvidence, 0, len(loads))}
	}
	for _, load := range loads {
		steps, err := indexTestLoadStepEvidence(load.Steps)
		if err != nil || len(steps) != len(result) {
			return nil, fmt.Errorf("load step set is inconsistent")
		}
		for name, step := range steps {
			group, exists := result[name]
			if !exists || group.Method != step.Method || group.Path != step.Path {
				return nil, fmt.Errorf("load step identity changed")
			}
			if err := testBenchmarkMetricsValid(step.testLoadMetrics); err != nil || step.Latency.Samples < minSamples {
				return nil, fmt.Errorf("load step metrics are invalid or sparse")
			}
			group.Steps = append(group.Steps, step)
			result[name] = group
		}
	}
	return result, nil
}

func addTestRepeatedLoadStepMetric(gate *testComparisonGate, name, step, metric, unit string, before, after []testLoadStepEvidence, value func(testLoadStepEvidence) float64, increaseLimit *float64, budget testComparisonBudget) {
	if increaseLimit == nil {
		return
	}
	oldValues := make([]float64, len(before))
	newValues := make([]float64, len(after))
	for i, item := range before {
		oldValues[i] = value(item)
	}
	for i, item := range after {
		newValues[i] = value(item)
	}
	metricName := "load." + step + "." + metric
	if metric == "error_rate" {
		gate.add(compareRepeatedTestGateErrorRate(name, metricName, oldValues, newValues, *increaseLimit, budget.Repeat.MaxErrorRateSpreadPercentagePoints, budget.Repeat.MinRuns))
		return
	}
	gate.add(compareRepeatedTestGateMetric(name, metricName, unit, oldValues, newValues, *increaseLimit, budget.Repeat.MaxSpreadPercent, budget.Repeat.MinRuns))
}

func compareRepeatedTestGateMetric(run, metric, unit string, before, after []float64, maxIncreasePercent, maxSpreadPercent float64, minRuns int) testComparisonGateCheck {
	oldMedian, oldSpread, oldErr := summarizeRepeatedMetric(before, false)
	newMedian, newSpread, newErr := summarizeRepeatedMetric(after, false)
	check := testComparisonGateCheck{Run: run, Metric: metric, Unit: unit, BeforeRuns: len(before), AfterRuns: len(after), SpreadUnit: "%"}
	if oldErr != "" || newErr != "" {
		check.Status = "inconclusive"
		check.Reason = firstNonempty(oldErr, newErr)
		return check
	}
	check.Before, check.After = &oldMedian, &newMedian
	check.BeforeSpread, check.AfterSpread = &oldSpread, &newSpread
	if len(before) < minRuns || len(after) < minRuns {
		check.Status = "inconclusive"
		check.Reason = fmt.Sprintf("need at least %d attempts in each report", minRuns)
		return check
	}
	if oldSpread > maxSpreadPercent || newSpread > maxSpreadPercent {
		check.Status = "inconclusive"
		check.Reason = fmt.Sprintf("repeat spread exceeds %.3g%% limit", maxSpreadPercent)
		return check
	}
	check = compareTestGateMetric(run, metric, unit, oldMedian, newMedian, maxIncreasePercent)
	check.BeforeRuns, check.AfterRuns = len(before), len(after)
	check.BeforeSpread, check.AfterSpread, check.SpreadUnit = &oldSpread, &newSpread, "%"
	return check
}

func compareRepeatedTestGateErrorRate(run, metric string, before, after []float64, maxIncreasePoints, maxSpreadPoints float64, minRuns int) testComparisonGateCheck {
	oldMedian, oldSpread, oldErr := summarizeRepeatedMetric(before, true)
	newMedian, newSpread, newErr := summarizeRepeatedMetric(after, true)
	check := testComparisonGateCheck{Run: run, Metric: metric, Unit: "%", BeforeRuns: len(before), AfterRuns: len(after), SpreadUnit: "pp"}
	if oldErr != "" || newErr != "" {
		check.Status = "inconclusive"
		check.Reason = firstNonempty(oldErr, newErr)
		return check
	}
	check.Before, check.After = &oldMedian, &newMedian
	check.BeforeSpread, check.AfterSpread = &oldSpread, &newSpread
	if len(before) < minRuns || len(after) < minRuns {
		check.Status = "inconclusive"
		check.Reason = fmt.Sprintf("need at least %d attempts in each report", minRuns)
		return check
	}
	if oldSpread > maxSpreadPoints || newSpread > maxSpreadPoints {
		check.Status = "inconclusive"
		check.Reason = fmt.Sprintf("repeat spread exceeds %.3g percentage-point limit", maxSpreadPoints)
		return check
	}
	check = compareTestGateErrorRate(run, metric, oldMedian, newMedian, maxIncreasePoints)
	check.BeforeRuns, check.AfterRuns = len(before), len(after)
	check.BeforeSpread, check.AfterSpread, check.SpreadUnit = &oldSpread, &newSpread, "pp"
	return check
}

func summarizeRepeatedMetric(values []float64, fraction bool) (median, spread float64, reason string) {
	if len(values) == 0 {
		return 0, 0, "metric has no repeated measurements"
	}
	ordered := append([]float64(nil), values...)
	for _, value := range ordered {
		if !finiteNonnegative(value) || (fraction && value > 1) {
			return 0, 0, "a repeated metric is not finite or is outside its valid range"
		}
	}
	sort.Float64s(ordered)
	middle := len(ordered) / 2
	if len(ordered)%2 == 0 {
		median = ordered[middle-1] + (ordered[middle]-ordered[middle-1])/2
	} else {
		median = ordered[middle]
	}
	spread = ordered[len(ordered)-1] - ordered[0]
	if fraction {
		spread *= 100
		return median, spread, ""
	}
	if median == 0 {
		if spread > 0 {
			return median, 0, "relative spread is undefined for a zero median"
		}
		return median, 0, ""
	}
	spread = math.Max(0, spread/median*100)
	return median, spread, ""
}

func firstNonempty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return "repeated metric is invalid"
}
