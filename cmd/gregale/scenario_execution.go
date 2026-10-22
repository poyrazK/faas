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
	CampaignSeed           *testCampaignSeedSetting
	SweepBaseline          *testRunReceipt
	SweepBaselineSHA256    string
	ReplaySweep            *testFaultSweepSetting
	ReplayManifest         *testReplayManifest
	ReplayCase             string
	ReplayOf               string
	RecoveryBaseline       *testRunReceipt
	RecoveryBaselineSHA256 string
	Prepared               *testPreparedScenario
	Profile                string
	Case                   testDataCase
	Attempt                int
	Workload               *testLoadWorkload
	Baseline               *testRunReceipt
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

func executePreparedTests(client *Client, suite string, scenarios []testPreparedScenario, repeat int, failFast bool, reportPath, junitPath, htmlPath, baselinePath string, recoveryBaselinePaths ...string) int {
	for _, s := range scenarios {
		if s.Scenario.CommandExecution != nil && s.Load != nil {
			return printErr("Invalid contained command recipe", fmt.Errorf("contained commands do not support load overrides"))
		}
		profiles := s.Profiles
		if len(profiles) == 0 {
			profiles = []string{s.Engine}
		}
		for _, p := range profiles {
			if err := validateTestCommandExecution(s.Scenario, s.Engine, p); err != nil {
				return printErr("Invalid contained command recipe", err)
			}
		}
		if err := validateTestCampaignGuidanceOptions(s.Scenario, s.Name, s.Profiles, []string{reportPath, junitPath, htmlPath}); err != nil {
			return printErr("Invalid campaign guidance", err)
		}
	}
	plans := prepareTestRunPlans(scenarios, repeat)
	digest, err := prepareTestBaselines(baselinePath, plans, reportPath, junitPath, htmlPath)
	if err != nil {
		return printErr("Invalid performance baseline", &exitErr{msg: err.Error(), code: 1})
	}
	if len(recoveryBaselinePaths) > 0 {
		if err := prepareTestRecoveryBaselines(recoveryBaselinePaths[0], plans, reportPath, junitPath, htmlPath); err != nil {
			return printErr("Invalid recovery baseline", &exitErr{msg: err.Error(), code: 1})
		}
	}
	if len(recoveryBaselinePaths) > 1 {
		if err := prepareTestSweepBaselines(recoveryBaselinePaths[1], plans, reportPath, junitPath, htmlPath); err != nil {
			return printErr("Invalid sweep baseline", &exitErr{msg: err.Error(), code: 1})
		}
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
				Behavior: newTestBehaviorEvidence(plan.Prepared.Scenario.Behavior),
				Scenario: plan.Prepared.Name, Engine: plan.Prepared.Engine, Profile: plan.Profile,
				Case: plan.Case.Name, Status: "skipped", SkipReason: skipReason, StartedAt: now, FinishedAt: now,
			}
			if plan.Prepared.Load != nil {
				receipt.Load = newTestLoadEvidence(plan.Prepared.Load)
				receipt.Load.Workload = plan.Workload
			}
			if plan.SweepBaseline != nil {
				receipt.SweepRegression = &testSweepRegressionEvidence{Status: "not_compared", ReportSHA256: plan.SweepBaselineSHA256, Error: skipReason}
			}
			if plan.RecoveryBaseline != nil {
				receipt.RecoveryRegression = &testRecoveryRegressionEvidence{Status: "not_compared", ReportSHA256: plan.RecoveryBaselineSHA256, Error: skipReason}
			}
			if plan.Baseline != nil {
				receipt.Baseline = &testBaselineEvidence{Status: "not_compared", ReportSHA256: baselineDigest, Error: skipReason}
			}
		} else {
			printTestRunStart(plan, repeat)
			receipt = executeTestRunPlan(ctx, client, plan)
			receipt.Attempt = plan.Attempt
			// Runners return only after fixtures and resources have been cleaned up.
			applyTestBaseline(plan, &receipt, baselineDigest)
			applyTestRecoveryBaseline(plan, &receipt)
			applyTestSweepBaseline(plan, &receipt)
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
	if p.Scenario.Resilience != nil && (len(p.Scenario.Resilience.FaultMatrix) > 0 || p.Scenario.Resilience.FaultSweep != nil || p.Scenario.Resilience.CampaignCoverage != nil) {
		return runTestFaultMatrix(ctx, plan, func(caseCtx context.Context, casePlan testRunPlan) testRunReceipt {
			return executeTestRunPlan(caseCtx, client, casePlan)
		})
	}
	if p.Scenario.Resilience != nil {
		return runTestResilience(ctx, plan, func(runCtx context.Context, runPlan testRunPlan) testRunReceipt {
			return executeTestRunPlan(runCtx, client, runPlan)
		})
	}
	switch p.Engine {
	case "local":
		return runLocalTestWithLoad(ctx, p.Name, p.Scenario, p.ManifestDir, p.BaseURL, plan.Case, p.Load)
	case "simulated":
		return runSimulatedTest(ctx, p.Name, p.Scenario, p.ManifestDir)
	default:
		receipt := runTestProfile(ctx, client, p.Name, p.Scenario, p.ManifestDir, plan.Profile)
		receipt.CampaignSeed = plan.CampaignSeed
		receipt.ReplayOf = plan.ReplayOf
		if receipt.Status == "failed" || (receipt.Status == "passed" && (plan.CampaignSeed != nil || plan.ReplaySweep != nil || (plan.ReplayManifest != nil && (len(plan.ReplayManifest.DurationOverrides) > 0 || len(plan.ReplayManifest.RecoveryOverrides) > 0 || plan.ReplayManifest.Sweep != nil)))) {
			receipt.Replay = plan.ReplayManifest
			if receipt.Replay == nil {
				receipt.Replay = newTestReplayManifest(p.Name, plan.Profile, plan.ReplayCase, p.Scenario)
				receipt.Replay.Sweep = plan.ReplaySweep
				receipt.Replay.Version = testFaultReplayVersion(receipt.Replay)
			}
		}
		return receipt
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
	if len(cfg.Streams) > 0 {
		budget += fmt.Sprintf(", %d independent streams, max %d VUs", len(cfg.Streams), cfg.maxVUs())
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
	if receipt.Replay != nil {
		_, _ = fmt.Fprintf(osStderr, "Replay available: gregale test replay --from REPORT.json --run-id %s --manifest MANIFEST.yaml\n", receipt.RunID)
	}
	if receipt.ReplayOf != "" {
		_, _ = fmt.Fprintf(osStderr, "Replay of run %s\n", receipt.ReplayOf)
	}
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
	if receipt.Actions != nil && receipt.Actions.Evidence != nil && receipt.Actions.Evidence.Snapshot != nil {
		if resume := receipt.Actions.Evidence.Snapshot.SuspendResume; resume != nil {
			for _, p := range resume.Receipts {
				_, _ = fmt.Fprintf(osStdout, "  suspend/resume %s: %s; VM %s; pause %d ms; readiness %d/%d ms\n", p.Workload, p.State, p.InstanceID, p.PauseMS, p.ResumeMS, resume.Spec.WithinMS)
			}
		}
		if cold := receipt.Actions.Evidence.Snapshot.ColdStart; cold != nil {
			for _, p := range cold.Preparations {
				_, _ = fmt.Fprintf(osStdout, "  cold start %s: %s; VM %s → %s; startup %d/%d ms\n", p.Workload, p.State, p.InstanceID, p.BootInstanceID, p.StartupMS, cold.Spec.WithinMS)
			}
		}
		if work := receipt.Actions.Evidence.Snapshot.ManagedWork; work != nil && work.WorkerFault != nil {
			f := work.WorkerFault
			_, _ = fmt.Fprintf(osStdout, "  worker termination %s: %s; VM %s → %s; job %s\n", f.Workload, f.State, f.InstanceID, f.RecoveryInstanceID, f.InvocationID)
		}
	}
	if receipt.Minimization != nil {
		e := receipt.Minimization
		if e.Duration != nil {
			_, _ = fmt.Fprintf(osStdout, "  confirmed failing fault duration %s/%s: %d → %d ms (resolution %d ms)\n", e.Duration.Step, e.Duration.Phase, e.Duration.BeforeMS, e.Duration.BestMS, e.Duration.ResolutionMS)
		}
		if e.Stability != nil {
			_, _ = fmt.Fprintf(osStdout, "  confirmed stability relapse interval: %d → %d ms (resolution %d ms)\n", e.Stability.BeforeMS, e.Stability.BestMS, e.Stability.ResolutionMS)
			if e.Stability.PassedBelowMS != nil {
				_, _ = fmt.Fprintf(osStdout, "    tested passing interval: %d ms\n", *e.Stability.PassedBelowMS)
			}
		}
		if e.Recovery != nil {
			if e.Recovery.BestWithinMS != nil {
				_, _ = fmt.Fprintf(osStdout, "  confirmed passing recovery deadline: %d ms; tested failing deadline: %d ms (ceiling %d, resolution %d ms)\n", *e.Recovery.BestWithinMS, e.Recovery.FailedWithinMS, e.Recovery.MaxWithinMS, e.Recovery.ResolutionMS)
			} else {
				_, _ = fmt.Fprintf(osStdout, "  no passing recovery deadline confirmed up to %d ms\n", e.Recovery.MaxWithinMS)
			}
		}
		if e.Percent != nil {
			_, _ = fmt.Fprintf(osStdout, "  confirmed selection percentage: %d → %d%% (resolution %d percentage points)\n", e.Percent.BeforePercent, e.Percent.BestPercent, e.Percent.ResolutionPercentagePoints)
			if e.Percent.PassedBelowPercent != nil {
				_, _ = fmt.Fprintf(osStdout, "    tested passing percentage: %d%%\n", *e.Percent.PassedBelowPercent)
			}
		}
		if e.Bandwidth != nil {
			_, _ = fmt.Fprintf(osStdout, "  confirmed bandwidth: %d → %d KiB/s (ceiling %d, resolution %d KiB/s)\n", e.Bandwidth.BeforeKiBPerSecond, e.Bandwidth.BestKiBPerSecond, e.Bandwidth.MaxKiBPerSecond, e.Bandwidth.ResolutionKiBPerSecond)
			if e.Bandwidth.PassedAboveKiBPerSecond != nil {
				_, _ = fmt.Fprintf(osStdout, "    tested passing rate: %d KiB/s\n", *e.Bandwidth.PassedAboveKiBPerSecond)
			}
		}
		if e.Latency != nil {
			_, _ = fmt.Fprintf(osStdout, "  confirmed latency: %d → %d ms (resolution %d ms)\n", e.Latency.BeforeMS, e.Latency.BestMS, e.Latency.ResolutionMS)
		}
		_, _ = fmt.Fprintf(osStdout, "  fault minimization: %s; %d → %d rules; %d/%d runs, %d/%d estimated workload-minutes\n", e.Status, e.OriginalRules, e.RemainingRules, e.RunsUsed, e.MaxRuns, e.EstimatedWorkloadMinutes, e.MaxWorkloadMinutes)
		if e.Reason != "" {
			_, _ = fmt.Fprintf(osStdout, "    %s\n", e.Reason)
		}
		if e.BestRunID != "" {
			_, _ = fmt.Fprintf(osStdout, "    best confirmed replay run: %s\n", e.BestRunID)
		}
	}
	if receipt.Sweep != nil {
		if receipt.Sweep.Mode == "duration" {
			_, _ = fmt.Fprintf(osStdout, "  sweep duration=%s (phase %s/%s)\n", receipt.Sweep.Value, receipt.Sweep.Rule.Step, receipt.Sweep.Rule.Phase)
		} else {
			_, _ = fmt.Fprintf(osStdout, "  sweep %s=%s (rule %s/%s/%d)\n", receipt.Sweep.Mode, receipt.Sweep.Value, receipt.Sweep.Rule.Step, receipt.Sweep.Rule.Phase, receipt.Sweep.Rule.Index)
		}
	}
	if receipt.FaultMatrix != nil {
		e := receipt.FaultMatrix
		if e.Sweep != nil {
			_, _ = fmt.Fprintf(osStdout, "  resilience sweep limits: %d runs, %d estimated workload-minutes per sweep\n", e.Sweep.MaxRuns, e.Sweep.MaxWorkloadMinutes)
		}
		_, _ = fmt.Fprintf(osStdout, "  fault matrix: %d passed, %d failed, %d skipped cases\n", e.Passed, e.Failed, e.Skipped)
		if e.CampaignCoverage != nil {
			printTestCampaignCoverage(osStdout, e.CampaignCoverage)
		}
		for _, caseReceipt := range e.Cases {
			printTestRunResult(caseReceipt, repeat)
		}
	}
	if receipt.Resilience != nil {
		e := receipt.Resilience
		_, _ = fmt.Fprintf(osStdout, "  resilience: %d/%d passed (%.2f%%), requires %.2f%%; %d failed, %d skipped\n", e.Passed, e.Runs, e.PassRate*100, e.MinPassRate*100, e.Failed, e.Skipped)
		for _, r := range e.Recovery {
			_, _ = fmt.Fprintf(osStdout, "    recovery %s/%s: %d confirmed samples, p50 %.3fms, p95 %.3fms\n", r.Step, testRecoveryDisplayPhase(r.Phase, r.Request), r.Samples, r.P50MS, r.P95MS)
		}
		for _, run := range e.History {
			printTestRunResult(run, e.Runs)
		}
	}
	if receipt.FailureDiagnostics != nil {
		_, _ = fmt.Fprintf(osStdout, "  recovery failure diagnostics: %s (%d assertions, %d workloads, truncated=%t)\n", receipt.FailureDiagnostics.ID, len(receipt.FailureDiagnostics.Assertions), len(receipt.FailureDiagnostics.Workloads), receipt.FailureDiagnostics.Truncated)
		for _, name := range testEndpointRecoveryNames(receipt.FailureDiagnostics.Workloads) {
			logs := receipt.FailureDiagnostics.Workloads[name]
			_, _ = fmt.Fprintf(osStdout, "    %s: %d log excerpts; %s\n", name, len(logs.Logs), logs.CollectionError)
		}
	}
	if receipt.Load != nil {
		printTestLoadSummary(osStdout, receipt.Load)
	}
	for _, step := range receipt.Steps {
		if r := step.Restart; r != nil {
			_, _ = fmt.Fprintf(osStdout, "  staged restart %q: %s (workload %s, wake %s, processing %s, %d readiness attempts, storage %d before/%d after, %.3fs)\n", step.Name, r.Status, r.Workload, r.WakeID, r.ProcessingStatus, r.ReadinessAttempts, len(r.Before), len(r.After), float64(r.DurationMS)/1000)
			if f := r.InFlight; f != nil {
				_, _ = fmt.Fprintf(osStdout, "    in-flight %q: pending at submission %t, restart verified %t, joined %t, outcome %s (%d checkpoint attempts)\n", f.Name, f.PendingAtRestart, f.RestartCompleted, f.Joined, f.Outcome, f.CheckpointAttempts)
			}
		}
		if step.Load != nil {
			_, _ = fmt.Fprintf(osStdout, "  staged step %q SLO:\n", step.Name)
			printTestLoadSummary(osStdout, step.Load)
		}
		if step.Relative != nil {
			streak := ""
			if step.Relative.RequiredConsecutivePasses > 1 {
				streak = fmt.Sprintf(", passing streak %d/%d", step.Relative.ConsecutivePasses, step.Relative.RequiredConsecutivePasses)
			}
			_, _ = fmt.Fprintf(osStdout, "  relative SLO vs %q: %s (%d attempts, %.3fs%s)\n", step.Relative.BaselineStep, step.Relative.Status, step.Relative.Attempts, float64(step.Relative.ElapsedMS)/1000, streak)
			for _, attempt := range step.Relative.History {
				attemptStreak := ""
				if step.Relative.RequiredConsecutivePasses > 1 {
					attemptStreak = fmt.Sprintf(", passing streak %d/%d", attempt.ConsecutivePasses, step.Relative.RequiredConsecutivePasses)
				}
				_, _ = fmt.Fprintf(osStdout, "    attempt %d: load %s, comparison %s (%.3fs%s)\n", attempt.Number, attempt.LoadStatus, attempt.ComparisonStatus, float64(attempt.DurationMS)/1000, attemptStreak)
				for _, check := range attempt.Checks {
					_, _ = fmt.Fprintf(osStdout, "      %s\n", formatTestStagedLoadComparisonCheck(check))
				}
				if attempt.Error != "" {
					_, _ = fmt.Fprintf(osStdout, "      %s\n", attempt.Error)
				}
			}
			if len(step.Relative.History) == 0 {
				for _, check := range step.Relative.Checks {
					_, _ = fmt.Fprintf(osStdout, "    %s\n", formatTestStagedLoadComparisonCheck(check))
				}
			}
		}
	}
	if receipt.SweepRegression != nil {
		e := receipt.SweepRegression
		_, _ = fmt.Fprintf(osStdout, "  sweep regression: %s (baseline %s) %s\n", e.Status, e.ReportSHA256, e.Error)
		for _, c := range e.Checks {
			_, _ = fmt.Fprintf(osStdout, "    %s=%s: %d/%d → %d/%d passed; drop %.2f percentage points (limit %.2f); passing setting lost=%t, passed=%t\n", c.Setting.Mode, c.Setting.Value, c.BaselinePassed, c.Runs, c.CurrentPassed, c.Runs, 100*c.Drop, 100*c.MaxDrop, c.LostPassingSetting, c.Passed)
		}
		for _, c := range e.Recovery {
			_, _ = fmt.Fprintf(osStdout, "    %s recovery %s/%s: p95 %.3f → %.3fms (limit %.3fms), passed=%t\n", c.Case, c.Step, testRecoveryDisplayPhase(c.Phase, c.Request), c.BaselineP95MS, c.CurrentP95MS, c.LimitMS, c.Passed)
		}
	}
	if receipt.RecoveryRegression != nil {
		e := receipt.RecoveryRegression
		_, _ = fmt.Fprintf(osStdout, "  recovery regression: %s\n", e.Status)
		for _, c := range e.Checks {
			_, _ = fmt.Fprintf(osStdout, "    %s %s/%s: previous p95 %.3fms (%d samples), current %.3fms (%d samples), change %+.2f%%, limit %.3fms (passed=%t)\n", c.Case, c.Step, testRecoveryDisplayPhase(c.Phase, c.Request), c.BaselineP95MS, c.BaselineSamples, c.CurrentP95MS, c.CurrentSamples, c.IncreasePercent, c.LimitMS, c.Passed)
		}
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

func formatTestStagedLoadComparisonCheck(check testStagedLoadComparisonCheck) string {
	label := check.Metric
	if check.Step != "" {
		label = fmt.Sprintf("%s (%s → %s): %s", check.Step, check.BaselineHTTPStep, check.CurrentHTTPStep, check.Metric)
	}
	if check.Error != "" {
		return label + ": " + check.Error
	}
	status := "failed"
	if check.Passed {
		status = "passed"
	}
	return fmt.Sprintf("%s %.4g %s %.4g (baseline %.4g, delta %+.4g; %s)", label, check.Current, check.Operator, check.Limit, check.Baseline, check.Delta, status)
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

func executeTestReplayPlans(client *Client, plans []testRunPlan, report, junit, html string) int {
	ctx, stop := signal.NotifyContext(context.Background(), testTerminationSignals()...)
	defer stop()
	results := runPreparedTestPlans(ctx, client, "", plans, 1, false, "")
	return finishTestRunReports("", results, report, junit, html)
}
