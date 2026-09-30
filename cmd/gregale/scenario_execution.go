package main

import (
	"context"
	"fmt"
	"os/signal"
	"time"
)

type testPreparedScenario struct {
	Name        string
	Scenario    testScenario
	ManifestDir string
	Engine      string
	Profiles    []string
	Cases       []testDataCase
	BaseURL     string
	Load        *testLoadConfig
}

type testRunPlan struct {
	Prepared *testPreparedScenario
	Profile  string
	Case     testDataCase
	Attempt  int
	Workload *testLoadWorkload
	Baseline *testRunReceipt
}

func prepareTestRunPlans(scenarios []testPreparedScenario, repeat int) []testRunPlan {
	plans := make([]testRunPlan, 0)
	for i := range scenarios {
		prepared := &scenarios[i]
		for attempt := 1; attempt <= repeat; attempt++ {
			switch prepared.Engine {
			case "local":
				for _, data := range prepared.Cases {
					plans = append(plans, testRunPlan{Prepared: prepared, Profile: "local", Case: data, Attempt: attempt})
				}
			case "simulated":
				plans = append(plans, testRunPlan{Prepared: prepared, Profile: "simulated", Attempt: attempt})
			default:
				for _, profile := range prepared.Profiles {
					plans = append(plans, testRunPlan{Prepared: prepared, Profile: profile, Attempt: attempt})
				}
			}
		}
	}
	return plans
}

func executePreparedTests(client *Client, suite string, scenarios []testPreparedScenario, repeat int, failFast bool, reportPath, junitPath, htmlPath, baselinePath string) int {
	plans := prepareTestRunPlans(scenarios, repeat)
	digest, err := prepareTestBaselines(baselinePath, plans, reportPath, junitPath, htmlPath)
	if err != nil {
		return printErr("Invalid performance baseline", &exitErr{msg: err.Error(), code: 1})
	}
	ctx, stop := signal.NotifyContext(context.Background(), testTerminationSignals()...)
	defer stop()
	results := runPreparedTestPlans(ctx, client, suite, plans, repeat, failFast, digest)
	return finishTestRunReports(suite, results, reportPath, junitPath, htmlPath)
}

func runPreparedTestPlans(ctx context.Context, client *Client, suite string, plans []testRunPlan, repeat int, failFast bool, baselineDigest string) []testRunReceipt {
	results := make([]testRunReceipt, 0, len(plans))
	skipReason := ""
	for _, plan := range plans {
		if ctx.Err() != nil {
			skipReason = "test execution interrupted: " + ctx.Err().Error()
		}
		var receipt testRunReceipt
		if skipReason != "" {
			now := time.Now().UTC()
			receipt = testRunReceipt{
				Scenario: plan.Prepared.Name, Engine: plan.Prepared.Engine, Profile: plan.Profile,
				Case: plan.Case.Name, Status: "skipped", SkipReason: skipReason, StartedAt: now, FinishedAt: now,
			}
			if plan.Prepared.Load != nil {
				receipt.Load = newTestLoadEvidence(plan.Prepared.Load)
				receipt.Load.Workload = plan.Workload
			}
			if plan.Baseline != nil {
				receipt.Baseline = &testBaselineEvidence{Status: "not_compared", ReportSHA256: baselineDigest, Error: skipReason}
			}
		} else {
			printTestRunStart(plan, repeat)
			receipt = executeTestRunPlan(ctx, client, plan)
			// Runners return only after fixtures and resources have been cleaned up.
			applyTestBaseline(plan, &receipt, baselineDigest)
			if failFast && receipt.Status != "passed" {
				skipReason = "not run after an earlier failure (--fail-fast)"
			}
		}
		receipt.Suite, receipt.Attempt = suite, plan.Attempt
		results = append(results, receipt)
		printTestRunResult(receipt, repeat)
	}
	return results
}

func executeTestRunPlan(ctx context.Context, client *Client, plan testRunPlan) testRunReceipt {
	p := plan.Prepared
	switch p.Engine {
	case "local":
		return runLocalTestWithLoad(ctx, p.Name, p.Scenario, p.ManifestDir, p.BaseURL, plan.Case, p.Load)
	case "simulated":
		return runSimulatedTest(ctx, p.Name, p.Scenario, p.ManifestDir)
	default:
		return runTestProfile(ctx, client, p.Name, p.Scenario, p.ManifestDir, plan.Profile)
	}
}

func printTestRunStart(plan testRunPlan, repeat int) {
	cfg := plan.Prepared.Load
	if cfg == nil || jsonOutput {
		return
	}
	budget := fmt.Sprintf("%d total journeys", cfg.Iterations)
	if cfg.Duration > 0 {
		budget = cfg.Duration.String()
	}
	if cfg.Rate > 0 {
		budget += fmt.Sprintf(", %d journeys/s, up to %d active", cfg.Rate, cfg.maxVUs())
	}
	if len(cfg.Stages) > 0 {
		budget += fmt.Sprintf(", %d stages, max %d VUs", len(cfg.Stages), cfg.maxVUs())
	}
	label := plan.Prepared.Name
	if plan.Case.Name != "" {
		label += "/" + plan.Case.Name
	}
	_, _ = fmt.Fprintf(osStdout, "%s: starting local load (%d VUs, %s, attempt %d/%d)\n", label, cfg.VUs, budget, plan.Attempt, repeat)
}

func printTestRunResult(receipt testRunReceipt, repeat int) {
	if jsonOutput {
		return
	}
	label := receipt.Profile
	if receipt.Load != nil {
		label += ", load"
	}
	if receipt.Case != "" {
		label += ", " + receipt.Case
	}
	label += fmt.Sprintf(", attempt %d/%d", receipt.Attempt, repeat)
	if receipt.AppSlug != "" {
		label += ", app " + receipt.AppSlug
	}
	_, _ = fmt.Fprintf(osStdout, "%s: %s (%s)\n", receipt.Scenario, receipt.Status, label)
	if receipt.Load != nil {
		printTestLoadSummary(osStdout, receipt.Load)
	}
	if receipt.Baseline != nil {
		_, _ = fmt.Fprintf(osStdout, "  baseline: %s (%d checks)\n", receipt.Baseline.Status, len(receipt.Baseline.Checks))
	}
	for _, message := range []string{receipt.Error, receipt.CleanupError, receipt.SkipReason} {
		if message != "" {
			_, _ = fmt.Fprintln(osStderr, message)
		}
	}
}

func finishTestRunReports(suite string, results []testRunReceipt, reportPath, junitPath string, htmlPaths ...string) int {
	htmlPath := ""
	if len(htmlPaths) > 0 {
		htmlPath = htmlPaths[0]
	}
	if reportPath != "" {
		if err := writeTestReport(reportPath, results); err != nil {
			return printErr("Could not save test report", err)
		}
	}
	if junitPath != "" {
		if err := writeTestJUnit(junitPath, results); err != nil {
			return printErr("Could not save JUnit report", err)
		}
	}
	if htmlPath != "" {
		if err := writeTestHTMLReport(htmlPath, suite, results); err != nil {
			return printErr("Could not save HTML report", err)
		}
	}
	if jsonOutput {
		if err := writeJSON(results); err != nil {
			return printErr("Could not print test report", err)
		}
	}
	passed, failed, skipped := 0, 0, 0
	for _, receipt := range results {
		switch receipt.Status {
		case "passed":
			passed++
		case "skipped":
			skipped++
		default:
			failed++
		}
	}
	if suite != "" && !jsonOutput {
		_, _ = fmt.Fprintf(osStdout, "suite %s: %d passed, %d failed, %d skipped\n", suite, passed, failed, skipped)
	}
	if failed > 0 || skipped > 0 || len(results) == 0 {
		return 1
	}
	return 0
}
