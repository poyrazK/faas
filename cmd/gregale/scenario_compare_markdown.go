package main

import (
	"fmt"
	"html"
	"io"
	"os"
	"strings"
)

const testComparisonMarkdownRowLimit = 100

func writeTestComparisonMarkdown(path string, comparison testReportComparison) error {
	if err := os.WriteFile(path, []byte(renderTestComparisonMarkdown(comparison)), 0o600); err != nil {
		return fmt.Errorf("write Markdown comparison: %w", err)
	}
	return nil
}

func appendTestComparisonGitHubSummary(path string, comparison testReportComparison) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open GitHub Actions job summary: %w", err)
	}
	if _, err := io.WriteString(file, "\n"); err != nil {
		_ = file.Close()
		return fmt.Errorf("write GitHub Actions job summary: %w", err)
	}
	if _, err := io.WriteString(file, renderTestComparisonMarkdown(comparison)); err != nil {
		_ = file.Close()
		return fmt.Errorf("write GitHub Actions job summary: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close GitHub Actions job summary: %w", err)
	}
	return nil
}

func renderTestComparisonMarkdown(comparison testReportComparison) string {
	var out strings.Builder
	fmt.Fprintf(&out, "# Gregale test comparison\n\n**Earlier:** %s  \n**Later:** %s\n\n",
		markdownComparisonText(comparison.Before), markdownComparisonText(comparison.After))
	fmt.Fprintf(&out, "Matched **%d** · Added **%d** · Removed **%d** · Status changes **%d**\n\n",
		comparison.Summary.Matched, comparison.Summary.Added, comparison.Summary.Removed, comparison.Summary.StatusChanged)
	if comparison.Gate == nil {
		out.WriteString("No budget was supplied; metric changes are informational.\n\n")
	} else {
		fmt.Fprintf(&out, "## Regression budget: %s\n\n%d passed · %d failed · %d inconclusive\n\n",
			strings.ToUpper(comparison.Gate.Status), comparison.Gate.Passed, comparison.Gate.Failed, comparison.Gate.Inconclusive)
		out.WriteString("| Group | Metric | Status | Earlier | Later | Limit | Repeat spread | Reason |\n")
		out.WriteString("|---|---|---|---:|---:|---:|---|---|\n")
		for index, check := range comparison.Gate.Checks {
			if index == testComparisonMarkdownRowLimit {
				break
			}
			fmt.Fprintf(&out, "| %s | %s | %s | %s | %s | %s | %s | %s |\n",
				markdownComparisonText(check.Run), markdownComparisonText(check.Metric), markdownComparisonText(check.Status),
				markdownComparisonText(comparisonMarkdownValue(check.Before, check.Unit)),
				markdownComparisonText(comparisonMarkdownValue(check.After, check.Unit)),
				markdownComparisonText(comparisonMarkdownValue(check.Limit, check.Unit)),
				markdownComparisonText(comparisonMarkdownRepeatSpread(check)), markdownComparisonText(check.Reason))
		}
		if len(comparison.Gate.Checks) > testComparisonMarkdownRowLimit {
			fmt.Fprintf(&out, "\nShowing the first %d of %d budget checks. Full details remain in the JSON or HTML report.\n",
				testComparisonMarkdownRowLimit, len(comparison.Gate.Checks))
		}
		out.WriteString("\n")
	}

	out.WriteString("## Run changes\n\n| Run | Change | Earlier | Later | Duration | Key metrics | Workload |\n|---|---|---|---|---:|---|---|\n")
	for index, run := range comparison.Runs {
		if index == testComparisonMarkdownRowLimit {
			break
		}
		beforeStatus, afterStatus := run.BeforeStatus, run.AfterStatus
		if beforeStatus == "" {
			beforeStatus = "—"
		}
		if afterStatus == "" {
			afterStatus = "—"
		}
		workload := "—"
		if run.WorkloadChanged {
			workload = "changed"
		}
		fmt.Fprintf(&out, "| %s | %s | %s | %s | %s | %s | %s |\n",
			markdownComparisonText(run.Key), markdownComparisonText(run.Change),
			markdownComparisonText(beforeStatus), markdownComparisonText(afterStatus),
			markdownComparisonText(testComparisonRunDuration(run)), markdownComparisonText(testComparisonRunKeyMetrics(run)), workload)
	}
	if len(comparison.Runs) > testComparisonMarkdownRowLimit {
		fmt.Fprintf(&out, "\nShowing the first %d of %d runs. Full details remain in the JSON or HTML report.\n",
			testComparisonMarkdownRowLimit, len(comparison.Runs))
	}
	return out.String()
}

func comparisonMarkdownValue(value *float64, unit string) string {
	if value == nil {
		return "—"
	}
	return formatTestMetricValue(*value, unit)
}

func comparisonMarkdownRepeatSpread(check testComparisonGateCheck) string {
	if check.BeforeRuns == 0 && check.AfterRuns == 0 {
		return "—"
	}
	format := func(runs int, spread *float64) string {
		if runs == 0 {
			return "—"
		}
		if spread == nil {
			return fmt.Sprintf("%d runs", runs)
		}
		return fmt.Sprintf("%d runs; %.2f %s", runs, *spread, check.SpreadUnit)
	}
	return format(check.BeforeRuns, check.BeforeSpread) + " → " + format(check.AfterRuns, check.AfterSpread)
}

func testComparisonRunDuration(run testRunComparison) string {
	for _, metric := range run.Metrics {
		if metric.Name == "duration_ms" {
			return formatTestMetricDelta(metric)
		}
	}
	return "—"
}

func testComparisonRunKeyMetrics(run testRunComparison) string {
	var values []string
	omitted := false
	appendMetric := func(name string, metric testMetricDelta) {
		if len(values) < 6 {
			values = append(values, name+" "+formatTestMetricDelta(metric))
		} else {
			omitted = true
		}
	}
	for _, metric := range run.Metrics {
		switch metric.Name {
		case "p95_latency", "p99_latency", "error_rate", "requests_per_second":
			appendMetric(metric.Name, metric)
		}
	}
	for _, step := range run.Steps {
		for _, metric := range step.Metrics {
			if step.Kind == "http" && metric.Name == "duration_ms" {
				appendMetric("http/"+step.Name+"/duration_ms", metric)
			}
			if step.Kind == "load" && (metric.Name == "p95_latency" || metric.Name == "p99_latency" || metric.Name == "error_rate") {
				appendMetric("load/"+step.Name+"/"+metric.Name, metric)
			}
		}
	}
	if len(values) == 0 {
		return "—"
	}
	if omitted {
		values = append(values, "additional metrics omitted")
	}
	return strings.Join(values, "; ")
}

func markdownComparisonText(value string) string {
	value = strings.ToValidUTF8(value, "�")
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) > 256 {
		value = string(runes[:256]) + "…"
	}
	value = html.EscapeString(value)
	value = strings.NewReplacer(
		"\\", "\\\\", "|", "\\|", "`", "\\`", "*", "\\*", "_", "\\_",
		"[", "\\[", "]", "\\]", "(", "\\(", ")", "\\)",
	).Replace(value)
	return value
}
