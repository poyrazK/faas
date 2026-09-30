package main

import (
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
)

type testReportComparison struct {
	Before  string                    `json:"before"`
	After   string                    `json:"after"`
	Summary testReportComparisonStats `json:"summary"`
	Runs    []testRunComparison       `json:"runs"`
}

type testReportComparisonStats struct {
	Matched       int `json:"matched"`
	Added         int `json:"added"`
	Removed       int `json:"removed"`
	StatusChanged int `json:"status_changed"`
}

type testRunComparison struct {
	Key                  string               `json:"key"`
	Scenario             string               `json:"scenario"`
	Engine               string               `json:"engine"`
	Profile              string               `json:"profile"`
	Case                 string               `json:"case,omitempty"`
	Attempt              int                  `json:"attempt"`
	Change               string               `json:"change"`
	BeforeStatus         string               `json:"before_status,omitempty"`
	AfterStatus          string               `json:"after_status,omitempty"`
	BeforeBaselineStatus string               `json:"before_baseline_status,omitempty"`
	AfterBaselineStatus  string               `json:"after_baseline_status,omitempty"`
	BeforeLoadStatus     string               `json:"before_load_status,omitempty"`
	AfterLoadStatus      string               `json:"after_load_status,omitempty"`
	BeforeError          string               `json:"before_error,omitempty"`
	AfterError           string               `json:"after_error,omitempty"`
	BeforeCleanupError   string               `json:"before_cleanup_error,omitempty"`
	AfterCleanupError    string               `json:"after_cleanup_error,omitempty"`
	BeforeWorkload       *testLoadWorkload    `json:"before_workload,omitempty"`
	AfterWorkload        *testLoadWorkload    `json:"after_workload,omitempty"`
	WorkloadChanged      bool                 `json:"workload_changed,omitempty"`
	Metrics              []testMetricDelta    `json:"metrics,omitempty"`
	Steps                []testStepComparison `json:"steps,omitempty"`
}

type testMetricDelta struct {
	Name         string   `json:"name"`
	Unit         string   `json:"unit,omitempty"`
	Before       *float64 `json:"before,omitempty"`
	After        *float64 `json:"after,omitempty"`
	Delta        *float64 `json:"delta,omitempty"`
	DeltaPercent *float64 `json:"delta_percent,omitempty"`
}

type testStepComparison struct {
	Kind         string            `json:"kind"`
	Name         string            `json:"name"`
	BeforeStatus string            `json:"before_status,omitempty"`
	AfterStatus  string            `json:"after_status,omitempty"`
	Metrics      []testMetricDelta `json:"metrics,omitempty"`
}

type testComparisonKey struct {
	Scenario string
	Engine   string
	Profile  string
	Case     string
	Attempt  int
}

func cmdTestCompare(args []string) int {
	parseArgs := make([]string, 0, len(args))
	var htmlArgs []string
	htmlSeen := false
	for index := 0; index < len(args); index++ {
		argument := args[index]
		if argument == "--html" || strings.HasPrefix(argument, "--html=") {
			if htmlSeen {
				return printErr("Invalid comparison report options", fmt.Errorf("--html may be supplied once"))
			}
			htmlSeen = true
			htmlArgs = append(htmlArgs, argument)
			if argument == "--html" {
				index++
				if index == len(args) {
					return printErr("Invalid comparison report options", fmt.Errorf("--html needs a path"))
				}
				htmlArgs = append(htmlArgs, args[index])
			}
			continue
		}
		parseArgs = append(parseArgs, argument)
	}
	fs := newFlagSet("test compare", flag.ContinueOnError)
	htmlPath := fs.String("html", "", "write a standalone HTML comparison report")
	if err := fs.Parse(append(htmlArgs, parseArgs...)); err != nil {
		return 1
	}
	if fs.NArg() != 2 {
		PrintUsage(osStderr, "usage: gregale test compare <before.json> <after.json> [--html PATH]", "test")
		return 1
	}
	beforePath, afterPath := fs.Arg(0), fs.Arg(1)
	if *htmlPath != "" {
		for _, input := range []string{beforePath, afterPath} {
			aliases, err := testReportPathAliases(*htmlPath, input)
			if err != nil {
				return printErr("Invalid comparison report path", err)
			}
			if aliases {
				return printErr("Invalid comparison report path", fmt.Errorf("HTML output %q must not overwrite input report %q", *htmlPath, input))
			}
		}
	}
	before, _, err := readTestBaselineReport(beforePath, nil)
	if err != nil {
		return printErr("Could not read earlier test report", &exitErr{msg: err.Error(), code: 1})
	}
	after, _, err := readTestBaselineReport(afterPath, nil)
	if err != nil {
		return printErr("Could not read later test report", &exitErr{msg: err.Error(), code: 1})
	}
	comparison, err := compareTestReports(filepath.Base(beforePath), filepath.Base(afterPath), before, after)
	if err != nil {
		return printErr("Could not compare test reports", &exitErr{msg: err.Error(), code: 1})
	}
	if *htmlPath != "" {
		if err := writeTestComparisonHTML(*htmlPath, comparison); err != nil {
			return printErr("Could not save HTML comparison", err)
		}
	}
	if jsonOutput {
		if err := writeJSON(comparison); err != nil {
			return printErr("Could not print report comparison", err)
		}
	} else {
		printTestReportComparison(osStdout, comparison)
		if *htmlPath != "" {
			_, _ = fmt.Fprintf(osStdout, "HTML report: %s\n", *htmlPath)
		}
	}
	return 0
}

func compareTestReports(beforeName, afterName string, before, after []testRunReceipt) (testReportComparison, error) {
	beforeIndex, err := indexTestComparisonReceipts(before, "earlier")
	if err != nil {
		return testReportComparison{}, err
	}
	afterIndex, err := indexTestComparisonReceipts(after, "later")
	if err != nil {
		return testReportComparison{}, err
	}
	keys := make([]testComparisonKey, 0, len(beforeIndex)+len(afterIndex))
	seen := make(map[testComparisonKey]bool, len(beforeIndex)+len(afterIndex))
	for key := range beforeIndex {
		keys = append(keys, key)
		seen[key] = true
	}
	for key := range afterIndex {
		if !seen[key] {
			keys = append(keys, key)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return testComparisonKeyString(keys[i]) < testComparisonKeyString(keys[j]) })
	comparison := testReportComparison{Before: beforeName, After: afterName}
	for _, key := range keys {
		oldReceipt, hasBefore := beforeIndex[key]
		newReceipt, hasAfter := afterIndex[key]
		run := testRunComparison{
			Key: testComparisonKeyString(key), Scenario: key.Scenario, Engine: key.Engine,
			Profile: key.Profile, Case: key.Case, Attempt: key.Attempt,
		}
		switch {
		case hasBefore && hasAfter:
			run.Change = "matched"
			run.BeforeStatus, run.AfterStatus = oldReceipt.Status, newReceipt.Status
			run.BeforeError, run.AfterError = oldReceipt.Error, newReceipt.Error
			run.BeforeCleanupError, run.AfterCleanupError = oldReceipt.CleanupError, newReceipt.CleanupError
			run.setWorkloadIdentities(oldReceipt.Load, newReceipt.Load)
			if oldReceipt.Baseline != nil {
				run.BeforeBaselineStatus = oldReceipt.Baseline.Status
			}
			if newReceipt.Baseline != nil {
				run.AfterBaselineStatus = newReceipt.Baseline.Status
			}
			if oldReceipt.Status != newReceipt.Status {
				comparison.Summary.StatusChanged++
			}
			run.Metrics = append(run.Metrics, makeTestMetricDelta("duration_ms", "ms", float64(oldReceipt.DurationMS), float64(newReceipt.DurationMS)))
			run.Steps = compareTestReceiptPhases(oldReceipt.Phases, newReceipt.Phases)
			run.Steps = append(run.Steps, compareTestHTTPRequestSteps(oldReceipt.Requests, newReceipt.Requests)...)
			run.setLoadStatuses(oldReceipt.Load, newReceipt.Load)
			loadMetrics, loadSteps := compareTestLoad(oldReceipt.Load, newReceipt.Load)
			run.Metrics = append(run.Metrics, loadMetrics...)
			run.Steps = append(run.Steps, loadSteps...)
			comparison.Summary.Matched++
		case hasAfter:
			run.Change = "added"
			run.AfterStatus = newReceipt.Status
			run.AfterError, run.AfterCleanupError = newReceipt.Error, newReceipt.CleanupError
			run.setWorkloadIdentities(nil, newReceipt.Load)
			run.Metrics = append(run.Metrics, oneSidedTestMetricDelta("duration_ms", "ms", float64(newReceipt.DurationMS), false))
			run.Steps = compareTestReceiptPhases(nil, newReceipt.Phases)
			run.Steps = append(run.Steps, compareTestHTTPRequestSteps(nil, newReceipt.Requests)...)
			run.setLoadStatuses(nil, newReceipt.Load)
			loadMetrics, loadSteps := compareTestLoad(nil, newReceipt.Load)
			run.Metrics = append(run.Metrics, loadMetrics...)
			run.Steps = append(run.Steps, loadSteps...)
			if newReceipt.Baseline != nil {
				run.AfterBaselineStatus = newReceipt.Baseline.Status
			}
			if newReceipt.Load != nil {
				run.AfterLoadStatus = newReceipt.Load.Status
			}
			comparison.Summary.Added++
		default:
			run.Change = "removed"
			run.BeforeStatus = oldReceipt.Status
			run.BeforeError, run.BeforeCleanupError = oldReceipt.Error, oldReceipt.CleanupError
			run.setWorkloadIdentities(oldReceipt.Load, nil)
			run.Metrics = append(run.Metrics, oneSidedTestMetricDelta("duration_ms", "ms", float64(oldReceipt.DurationMS), true))
			run.Steps = compareTestReceiptPhases(oldReceipt.Phases, nil)
			run.Steps = append(run.Steps, compareTestHTTPRequestSteps(oldReceipt.Requests, nil)...)
			run.setLoadStatuses(oldReceipt.Load, nil)
			loadMetrics, loadSteps := compareTestLoad(oldReceipt.Load, nil)
			run.Metrics = append(run.Metrics, loadMetrics...)
			run.Steps = append(run.Steps, loadSteps...)
			if oldReceipt.Baseline != nil {
				run.BeforeBaselineStatus = oldReceipt.Baseline.Status
			}
			if oldReceipt.Load != nil {
				run.BeforeLoadStatus = oldReceipt.Load.Status
			}
			comparison.Summary.Removed++
		}
		comparison.Runs = append(comparison.Runs, run)
	}
	return comparison, nil
}
func (run *testRunComparison) setLoadStatuses(before, after *testLoadEvidence) {
	if before != nil {
		run.BeforeLoadStatus = before.Status
	}
	if after != nil {
		run.AfterLoadStatus = after.Status
	}
}

func (run *testRunComparison) setWorkloadIdentities(before, after *testLoadEvidence) {
	if before != nil && before.Workload != nil {
		workload := *before.Workload
		run.BeforeWorkload = &workload
	}
	if after != nil && after.Workload != nil {
		workload := *after.Workload
		run.AfterWorkload = &workload
	}
	if run.BeforeWorkload != nil && run.AfterWorkload != nil {
		run.WorkloadChanged = run.BeforeWorkload.SHA256 != run.AfterWorkload.SHA256 || run.BeforeWorkload.Label != run.AfterWorkload.Label
	}
}

func indexTestComparisonReceipts(receipts []testRunReceipt, label string) (map[testComparisonKey]testRunReceipt, error) {
	if len(receipts) == 0 {
		return nil, fmt.Errorf("%s report contains no runs", label)
	}
	index := make(map[testComparisonKey]testRunReceipt, len(receipts))
	for _, receipt := range receipts {
		if strings.TrimSpace(receipt.Scenario) == "" || strings.TrimSpace(receipt.Engine) == "" || strings.TrimSpace(receipt.Profile) == "" || receipt.Attempt < 1 {
			return nil, fmt.Errorf("%s report contains a run without scenario, engine, profile, or valid attempt identity", label)
		}
		key := testComparisonKey{receipt.Scenario, receipt.Engine, receipt.Profile, receipt.Case, receipt.Attempt}
		if _, exists := index[key]; exists {
			return nil, fmt.Errorf("%s report contains duplicate run %s", label, testComparisonKeyString(key))
		}
		index[key] = receipt
	}
	return index, nil
}

func testComparisonKeyString(key testComparisonKey) string {
	name := fmt.Sprintf("%s/%s/%s", key.Scenario, key.Engine, key.Profile)
	if key.Case != "" {
		name += "/" + key.Case
	}
	return fmt.Sprintf("%s#%d", name, key.Attempt)
}

func makeTestMetricDelta(name, unit string, before, after float64) testMetricDelta {
	delta := after - before
	metric := testMetricDelta{Name: name, Unit: unit, Before: &before, After: &after, Delta: &delta}
	if before != 0 {
		percent := ((after - before) / before) * 100
		metric.DeltaPercent = &percent
	}
	return metric
}

func oneSidedTestMetricDelta(name, unit string, value float64, before bool) testMetricDelta {
	metric := testMetricDelta{Name: name, Unit: unit}
	if before {
		metric.Before = &value
	} else {
		metric.After = &value
	}
	return metric
}

func compareTestReceiptPhases(before, after []testPhaseEvidence) []testStepComparison {
	return compareNamedTestSteps("phase", before, after,
		func(value testPhaseEvidence) string { return value.Name },
		func(value testPhaseEvidence) string { return value.Status },
		func(value testPhaseEvidence) []testMetricDelta {
			return []testMetricDelta{oneSidedTestMetricDelta("duration_ms", "ms", float64(value.DurationMS), true)}
		},
		func(value testPhaseEvidence) []testMetricDelta {
			return []testMetricDelta{oneSidedTestMetricDelta("duration_ms", "ms", float64(value.DurationMS), false)}
		},
		func(old, current testPhaseEvidence) []testMetricDelta {
			return []testMetricDelta{makeTestMetricDelta("duration_ms", "ms", float64(old.DurationMS), float64(current.DurationMS))}
		})
}

func compareTestHTTPRequestSteps(before, after []testHTTPRequestEvidence) []testStepComparison {
	return compareNamedTestSteps("http", before, after,
		func(value testHTTPRequestEvidence) string { return value.Name },
		func(value testHTTPRequestEvidence) string {
			if value.Passed {
				return "passed"
			}
			return "failed"
		},
		func(value testHTTPRequestEvidence) []testMetricDelta {
			return []testMetricDelta{oneSidedTestMetricDelta("duration_ms", "ms", float64(value.DurationMS), true)}
		},
		func(value testHTTPRequestEvidence) []testMetricDelta {
			return []testMetricDelta{oneSidedTestMetricDelta("duration_ms", "ms", float64(value.DurationMS), false)}
		},
		func(old, current testHTTPRequestEvidence) []testMetricDelta {
			return []testMetricDelta{makeTestMetricDelta("duration_ms", "ms", float64(old.DurationMS), float64(current.DurationMS))}
		})
}

func compareNamedTestSteps[T any](kind string, before, after []T, name func(T) string, status func(T) string,
	beforeOnly, afterOnly func(T) []testMetricDelta, matched func(T, T) []testMetricDelta) []testStepComparison {
	oldByName := make(map[string]T, len(before))
	newByName := make(map[string]T, len(after))
	for _, item := range before {
		oldByName[name(item)] = item
	}
	for _, item := range after {
		newByName[name(item)] = item
	}
	names := make([]string, 0, len(oldByName)+len(newByName))
	seen := make(map[string]bool, len(oldByName)+len(newByName))
	for key := range oldByName {
		names, seen[key] = append(names, key), true
	}
	for key := range newByName {
		if !seen[key] {
			names = append(names, key)
		}
	}
	sort.Strings(names)
	steps := make([]testStepComparison, 0, len(names))
	for _, stepName := range names {
		old, hasBefore := oldByName[stepName]
		current, hasAfter := newByName[stepName]
		step := testStepComparison{Kind: kind, Name: stepName}
		switch {
		case hasBefore && hasAfter:
			step.BeforeStatus, step.AfterStatus = status(old), status(current)
			step.Metrics = matched(old, current)
		case hasBefore:
			step.BeforeStatus, step.Metrics = status(old), beforeOnly(old)
		default:
			step.AfterStatus, step.Metrics = status(current), afterOnly(current)
		}
		steps = append(steps, step)
	}
	return steps
}

func compareTestLoad(before, after *testLoadEvidence) ([]testMetricDelta, []testStepComparison) {
	var metrics []testMetricDelta
	if before != nil && after != nil {
		metrics = append(metrics,
			makeTestMetricDelta("iterations_started", "journeys", float64(before.IterationsStarted), float64(after.IterationsStarted)),
			makeTestMetricDelta("iterations_completed", "journeys", float64(before.IterationsCompleted), float64(after.IterationsCompleted)),
			makeTestMetricDelta("iterations_failed", "journeys", float64(before.IterationsFailed), float64(after.IterationsFailed)),
			makeTestMetricDelta("requests", "requests", float64(before.Requests), float64(after.Requests)),
			makeTestMetricDelta("failures", "requests", float64(before.Failures), float64(after.Failures)),
			makeTestMetricDelta("error_rate", "%", before.ErrorRate*100, after.ErrorRate*100),
			makeTestMetricDelta("requests_per_second", "req/s", before.RequestsPerSecond, after.RequestsPerSecond),
			makeTestMetricDelta("p50_latency", "ms", before.Latency.P50MS, after.Latency.P50MS),
			makeTestMetricDelta("p95_latency", "ms", before.Latency.P95MS, after.Latency.P95MS),
			makeTestMetricDelta("p99_latency", "ms", before.Latency.P99MS, after.Latency.P99MS),
		)
		if before.Arrival != nil && after.Arrival != nil {
			metrics = append(metrics,
				makeTestMetricDelta("arrival_target", "journeys/s", float64(before.Arrival.TargetRate), float64(after.Arrival.TargetRate)),
				makeTestMetricDelta("arrivals_scheduled", "journeys", float64(before.Arrival.Scheduled), float64(after.Arrival.Scheduled)),
				makeTestMetricDelta("arrivals_dropped", "journeys", float64(before.Arrival.Dropped), float64(after.Arrival.Dropped)),
			)
		}
	} else if before != nil {
		metrics = oneSidedTestLoadMetrics(before, true)
	} else if after != nil {
		metrics = oneSidedTestLoadMetrics(after, false)
	}
	steps := compareNamedTestSteps("load", testLoadSteps(before), testLoadSteps(after),
		func(value testLoadStepEvidence) string { return value.Name },
		func(value testLoadStepEvidence) string { return "recorded" },
		func(value testLoadStepEvidence) []testMetricDelta { return testLoadStepMetrics(value, false) },
		func(value testLoadStepEvidence) []testMetricDelta { return testLoadStepMetrics(value, true) },
		func(old, current testLoadStepEvidence) []testMetricDelta {
			return []testMetricDelta{
				makeTestMetricDelta("requests", "requests", float64(old.Requests), float64(current.Requests)),
				makeTestMetricDelta("failures", "requests", float64(old.Failures), float64(current.Failures)),
				makeTestMetricDelta("error_rate", "%", old.ErrorRate*100, current.ErrorRate*100),
				makeTestMetricDelta("p50_latency", "ms", old.Latency.P50MS, current.Latency.P50MS),
				makeTestMetricDelta("p95_latency", "ms", old.Latency.P95MS, current.Latency.P95MS),
				makeTestMetricDelta("p99_latency", "ms", old.Latency.P99MS, current.Latency.P99MS),
			}
		})
	return metrics, steps
}

type testNamedMetricValue struct {
	name  string
	unit  string
	value float64
}

func oneSidedTestLoadMetrics(load *testLoadEvidence, before bool) []testMetricDelta {
	values := []testNamedMetricValue{
		{"iterations_started", "journeys", float64(load.IterationsStarted)},
		{"iterations_completed", "journeys", float64(load.IterationsCompleted)},
		{"iterations_failed", "journeys", float64(load.IterationsFailed)},
		{"requests", "requests", float64(load.Requests)},
		{"failures", "requests", float64(load.Failures)},
		{"error_rate", "%", load.ErrorRate * 100},
		{"requests_per_second", "req/s", load.RequestsPerSecond},
		{"p50_latency", "ms", load.Latency.P50MS},
		{"p95_latency", "ms", load.Latency.P95MS},
		{"p99_latency", "ms", load.Latency.P99MS},
	}
	if load.Arrival != nil {
		values = append(values,
			testNamedMetricValue{"arrival_target", "journeys/s", float64(load.Arrival.TargetRate)},
			testNamedMetricValue{"arrivals_scheduled", "journeys", float64(load.Arrival.Scheduled)},
			testNamedMetricValue{"arrivals_dropped", "journeys", float64(load.Arrival.Dropped)},
		)
	}
	metrics := make([]testMetricDelta, 0, len(values))
	for _, value := range values {
		metrics = append(metrics, oneSidedTestMetricDelta(value.name, value.unit, value.value, before))
	}
	return metrics
}

func testLoadSteps(load *testLoadEvidence) []testLoadStepEvidence {
	if load == nil {
		return nil
	}
	return load.Steps
}

func testLoadStepMetrics(step testLoadStepEvidence, after bool) []testMetricDelta {
	return []testMetricDelta{
		oneSidedTestMetricDelta("requests", "requests", float64(step.Requests), !after),
		oneSidedTestMetricDelta("failures", "requests", float64(step.Failures), !after),
		oneSidedTestMetricDelta("error_rate", "%", step.ErrorRate*100, !after),
		oneSidedTestMetricDelta("p50_latency", "ms", step.Latency.P50MS, !after),
		oneSidedTestMetricDelta("p95_latency", "ms", step.Latency.P95MS, !after),
		oneSidedTestMetricDelta("p99_latency", "ms", step.Latency.P99MS, !after),
	}
}

func printTestReportComparison(out io.Writer, comparison testReportComparison) {
	_, _ = fmt.Fprintf(out, "Compare %s → %s\n", comparison.Before, comparison.After)
	_, _ = fmt.Fprintf(out, "%d matched · %d added · %d removed · %d status changes\n",
		comparison.Summary.Matched, comparison.Summary.Added, comparison.Summary.Removed, comparison.Summary.StatusChanged)
	_, _ = fmt.Fprintln(out, "This is a report diff; it does not apply performance budgets or fail on metric changes.")
	for _, run := range comparison.Runs {
		beforeStatus, afterStatus := run.BeforeStatus, run.AfterStatus
		if beforeStatus == "" {
			beforeStatus = "—"
		}
		if afterStatus == "" {
			afterStatus = "—"
		}
		_, _ = fmt.Fprintf(out, "\n%s  %s → %s\n", run.Key, beforeStatus, afterStatus)
		if run.WorkloadChanged {
			_, _ = fmt.Fprintln(out, "  load workload identity changed; compare the load metrics cautiously")
		}
		if run.BeforeError != "" {
			_, _ = fmt.Fprintf(out, "  earlier error: %s\n", run.BeforeError)
		}
		if run.AfterError != "" {
			_, _ = fmt.Fprintf(out, "  later error: %s\n", run.AfterError)
		}
		if run.BeforeCleanupError != "" {
			_, _ = fmt.Fprintf(out, "  earlier cleanup error: %s\n", run.BeforeCleanupError)
		}
		if run.AfterCleanupError != "" {
			_, _ = fmt.Fprintf(out, "  later cleanup error: %s\n", run.AfterCleanupError)
		}
		for _, metric := range run.Metrics {
			_, _ = fmt.Fprintf(out, "  %-24s %s\n", metric.Name, formatTestMetricDelta(metric))
		}
		for _, step := range run.Steps {
			for _, metric := range step.Metrics {
				_, _ = fmt.Fprintf(out, "  %-24s %s\n", step.Kind+"/"+step.Name+"/"+metric.Name, formatTestMetricDelta(metric))
			}
		}
	}
}

func formatTestMetricDelta(metric testMetricDelta) string {
	format := func(value float64) string {
		return fmt.Sprintf("%.3f", value)
	}
	if metric.Before == nil {
		return fmt.Sprintf("— → %s %s", format(*metric.After), metric.Unit)
	}
	if metric.After == nil {
		return fmt.Sprintf("%s %s → —", format(*metric.Before), metric.Unit)
	}
	change := fmt.Sprintf("%+.3f %s", *metric.Delta, metric.Unit)
	if metric.DeltaPercent != nil {
		change += fmt.Sprintf(" (%+.1f%%)", *metric.DeltaPercent)
	}
	return fmt.Sprintf("%s → %s %s (%s)", format(*metric.Before), format(*metric.After), metric.Unit, change)
}

const testHTMLComparisonTemplate = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Gregale test comparison</title><style>{{styles}}</style></head>
<body><main><h1>Gregale test comparison</h1>
<div class="muted">{{.Before}} → {{.After}} · generated {{.Generated}}</div>
<p class="panel">Offline report diff. Metric changes are informational; this command does not apply regression budgets or fail on performance changes.</p>
<section class="summary"><div class="stat"><strong>{{.Summary.Matched}}</strong><span class="label">Matched runs</span></div><div class="stat"><strong>{{.Summary.Added}}</strong><span class="label">Added</span></div><div class="stat"><strong>{{.Summary.Removed}}</strong><span class="label">Removed</span></div><div class="stat"><strong>{{.Summary.StatusChanged}}</strong><span class="label">Status changes</span></div></section>
<h2>Run changes</h2><table><thead><tr><th>Run</th><th>Change</th><th>Earlier</th><th>Later</th><th>Duration</th></tr></thead><tbody>
{{range .Runs}}<tr><td>{{.Key}}</td><td>{{.Change}}</td><td>{{if .BeforeStatus}}{{.BeforeStatus}}{{else}}—{{end}}{{if .BeforeBaselineStatus}} · baseline {{.BeforeBaselineStatus}}{{end}}</td><td>{{if .AfterStatus}}{{.AfterStatus}}{{else}}—{{end}}{{if .AfterBaselineStatus}} · baseline {{.AfterBaselineStatus}}{{end}}</td><td>{{range .Metrics}}{{if eq .Name "duration_ms"}}{{delta .}}{{end}}{{end}}</td></tr>{{end}}
</tbody></table>
{{range .Runs}}{{if or .Metrics .Steps .BeforeError .AfterError .BeforeCleanupError .AfterCleanupError .WorkloadChanged}}<details class="run"><summary><strong>{{.Key}}</strong> · {{.Change}}</summary><div class="run-body">
{{if .BeforeError}}<div class="error">Earlier error: {{.BeforeError}}</div>{{end}}{{if .AfterError}}<div class="error">Later error: {{.AfterError}}</div>{{end}}{{if .BeforeCleanupError}}<div class="error">Earlier cleanup error: {{.BeforeCleanupError}}</div>{{end}}{{if .AfterCleanupError}}<div class="error">Later cleanup error: {{.AfterCleanupError}}</div>{{end}}
{{if .WorkloadChanged}}<div class="error">The load workload identity changed. Review the report labels and SHA-256 values before interpreting metric deltas.</div>{{end}}
{{if .Metrics}}<h3>Run and load metrics</h3><table><thead><tr><th>Metric</th><th>Earlier</th><th>Later</th><th>Change</th></tr></thead><tbody>{{range .Metrics}}<tr><td>{{.Name}}</td><td>{{metricValue . true}}</td><td>{{metricValue . false}}</td><td>{{delta .}}</td></tr>{{end}}</tbody></table>{{end}}
{{range .Steps}}<h3>{{.Kind}} · {{.Name}}{{if .BeforeStatus}} · {{.BeforeStatus}}{{end}}{{if .AfterStatus}} → {{.AfterStatus}}{{end}}</h3>{{if .Metrics}}<table><thead><tr><th>Metric</th><th>Earlier</th><th>Later</th><th>Change</th></tr></thead><tbody>{{range .Metrics}}<tr><td>{{.Name}}</td><td>{{metricValue . true}}</td><td>{{metricValue . false}}</td><td>{{delta .}}</td></tr>{{end}}</tbody></table>{{end}}{{end}}
</div></details>{{end}}{{end}}
<details class="nested"><summary>Comparison data (JSON)</summary><pre>{{json .}}</pre></details>
</main></body></html>`

func writeTestComparisonHTML(path string, comparison testReportComparison) error {
	return renderTestHTML(path, testHTMLComparisonTemplate, struct {
		testReportComparison
		Generated string
	}{comparison, testReportTimestamp()})
}
