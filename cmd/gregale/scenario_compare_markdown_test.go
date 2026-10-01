package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderTestComparisonMarkdownIncludesBudgetStatsAndEscapesCells(t *testing.T) {
	beforeValue, afterValue, limit, delta := 100.0, 112.0, 110.0, 12.0
	spreadBefore, spreadAfter := 4.0, 28.0
	comparison := testReportComparison{
		Before: "before|<base>\nreport.json", After: "after.json",
		Summary: testReportComparisonStats{Matched: 1, Added: 1, StatusChanged: 1},
		Runs: []testRunComparison{{
			Key: "health/local/local#1|<script>", Change: "matched", BeforeStatus: "passed", AfterStatus: "failed",
			Metrics: []testMetricDelta{{Name: "duration_ms", Unit: "ms", Before: &beforeValue, After: &afterValue, Delta: &delta}},
		}},
		Gate: &testComparisonGate{Status: "failed", Passed: 1, Failed: 1, Inconclusive: 1, Checks: []testComparisonGateCheck{{
			Run: "health/local/local", Metric: "load.p95_latency", Status: "inconclusive", Unit: "ms",
			Before: &beforeValue, After: &afterValue, Limit: &limit,
			BeforeRuns: 3, AfterRuns: 3, BeforeSpread: &spreadBefore, AfterSpread: &spreadAfter, SpreadUnit: "%",
			Reason: "spread | <script>\nexceeded [click](https://evil.test) *noise*",
		}}},
	}

	markdown := renderTestComparisonMarkdown(comparison)
	for _, expected := range []string{
		"# Gregale test comparison",
		"before\\|&lt;base&gt; report.json",
		"## Regression budget: FAILED",
		"3 runs; 4.00 % → 3 runs; 28.00 %",
		"spread \\| &lt;script&gt; exceeded \\[click\\]\\(https://evil.test\\) \\*noise\\*",
		"health/local/local#1\\|&lt;script&gt;",
	} {
		if !strings.Contains(markdown, expected) {
			t.Errorf("Markdown summary omitted %q:\n%s", expected, markdown)
		}
	}
	if strings.Contains(markdown, "<script>") {
		t.Fatalf("Markdown summary left HTML markup unescaped:\n%s", markdown)
	}
}

func TestRenderTestComparisonMarkdownShowsKeyMetricsWithoutBudget(t *testing.T) {
	before, after, delta, deltaPercent := 100.0, 120.0, 20.0, 20.0
	comparison := testReportComparison{
		Before: "baseline.json", After: "current.json",
		Summary: testReportComparisonStats{Matched: 1},
		Runs: []testRunComparison{{
			Key: "api/local/local#1", Change: "matched", BeforeStatus: "passed", AfterStatus: "passed",
			Metrics: []testMetricDelta{{Name: "p95_latency", Unit: "ms", Before: &before, After: &after, Delta: &delta, DeltaPercent: &deltaPercent}},
		}},
	}
	markdown := renderTestComparisonMarkdown(comparison)
	if !strings.Contains(markdown, "No budget was supplied") || !strings.Contains(markdown, "p95\\_latency 100.000 → 120.000 ms") {
		t.Fatalf("informational Markdown summary omitted metric deltas:\n%s", markdown)
	}
}

func TestCmdTestCompareWritesMarkdownAndAppendsGitHubSummaryOnGateFailure(t *testing.T) {
	dir := t.TempDir()
	beforePath := filepath.Join(dir, "before.json")
	afterPath := filepath.Join(dir, "after.json")
	budgetPath := filepath.Join(dir, "budget.yaml")
	markdownPath := filepath.Join(dir, "comparison.md")
	summaryPath := filepath.Join(dir, "step-summary.md")
	before := []testRunReceipt{comparisonBudgetReceipt(100, 150, 0, 100, 1000, 50, "passed")}
	after := []testRunReceipt{comparisonBudgetReceipt(100, 150, 0, 100, 1000, 50, "failed")}
	for path, receipts := range map[string][]testRunReceipt{beforePath: before, afterPath: after} {
		body, err := json.Marshal(receipts)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(budgetPath, []byte("version: 1\nruns:\n  require_passing: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(summaryPath, []byte("existing job summary\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GITHUB_STEP_SUMMARY", summaryPath)
	oldOut, oldErr, oldJSON := osStdout, osStderr, jsonOutput
	var stdout, stderr strings.Builder
	osStdout, osStderr, jsonOutput = &stdout, &stderr, false
	t.Cleanup(func() { osStdout, osStderr, jsonOutput = oldOut, oldErr, oldJSON })
	code := cmdTestCompare([]string{beforePath, afterPath, "--budget", budgetPath, "--markdown", markdownPath, "--github-summary"})
	if code == 0 {
		t.Fatal("failed budget returned success")
	}
	markdown, err := os.ReadFile(markdownPath)
	if err != nil {
		t.Fatalf("Markdown summary was not written on gate failure: %v", err)
	}
	if !strings.Contains(string(markdown), "## Regression budget: FAILED") || !strings.Contains(string(markdown), "run.status") {
		t.Fatalf("Markdown summary omitted the gate result: %s", markdown)
	}
	summary, err := os.ReadFile(summaryPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(summary), "existing job summary\n\n# Gregale test comparison") || !strings.Contains(string(summary), "## Regression budget: FAILED") {
		t.Fatalf("GitHub job summary was not appended: %s", summary)
	}
	if !strings.Contains(stdout.String(), "Markdown summary:") || !strings.Contains(stdout.String(), "GitHub Actions job summary updated") {
		t.Fatalf("CLI output omitted summary destinations: %s", stdout.String())
	}
}

func TestCmdTestCompareGitHubSummaryRequiresEnvironmentPath(t *testing.T) {
	t.Setenv("GITHUB_STEP_SUMMARY", "")
	oldErr := osStderr
	var stderr strings.Builder
	osStderr = &stderr
	t.Cleanup(func() { osStderr = oldErr })
	if code := cmdTestCompare([]string{"--github-summary"}); code == 0 {
		t.Fatal("missing GitHub summary path returned success")
	}
	if !strings.Contains(stderr.String(), "GITHUB_STEP_SUMMARY to be set") {
		t.Fatalf("missing summary path error was unclear: %s", stderr.String())
	}
}

func TestCmdTestCompareMarkdownCannotOverwriteAnInputReport(t *testing.T) {
	dir := t.TempDir()
	beforePath := filepath.Join(dir, "before.json")
	afterPath := filepath.Join(dir, "after.json")
	if err := os.WriteFile(beforePath, []byte("earlier report"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(afterPath, []byte("later report"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldErr := osStderr
	var stderr strings.Builder
	osStderr = &stderr
	t.Cleanup(func() { osStderr = oldErr })
	if code := cmdTestCompare([]string{beforePath, afterPath, "--markdown", beforePath}); code == 0 {
		t.Fatal("Markdown output overwrote an input report")
	}
	body, err := os.ReadFile(beforePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "earlier report" {
		t.Fatalf("input report changed: %q", body)
	}
}
