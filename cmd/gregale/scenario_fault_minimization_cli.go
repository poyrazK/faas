package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os/signal"
	"time"
)

func cmdTestMinimize(args []string) int {
	fs := newFlagSet("test minimize", flag.ContinueOnError)
	from := fs.String("from", "", "JSON report containing the original failed run")
	id := fs.String("run-id", "", "original isolated run ID")
	manifest := fs.String("manifest", "gregale-test.yaml", "original scenario manifest")
	assertion := fs.String("assertion", "", "recovery assertion: STEP/PHASE or STEP/PHASE/REQUEST")
	mode := fs.String("mode", "rules", "minimize rules, latency, bandwidth, percent, recovery, stability, or duration")
	ruleIndex := fs.Int("rule-index", -1, "original zero-based rule index in severity modes")
	resolution := fs.Duration("resolution", 25*time.Millisecond, "latency search resolution")
	maxRate := fs.Int64("max-rate-kib-per-second", 0, "required bandwidth search ceiling (1..1000000 KiB/s)")
	rateResolution := fs.Int64("rate-resolution-kib-per-second", 1, "bandwidth search resolution (KiB/s)")
	percentResolution := fs.Int("percent-resolution", 1, "percentage search resolution (1..100 percentage points)")
	maxRecovery := fs.Duration("max-recovery-within", 0, "required recovery deadline ceiling")
	recoveryResolution := fs.Duration("recovery-resolution", 25*time.Millisecond, "recovery deadline search resolution")
	durationResolution := fs.Duration("duration-resolution", 100*time.Millisecond, "fault duration search resolution")
	stabilityResolution := fs.Duration("stability-resolution", 25*time.Millisecond, "stability observation search resolution")
	maxRuns := fs.Int("max-runs", 10, "maximum fresh runs including all confirmations (2..20)")
	maxMinutes := fs.Int("max-workload-minutes", 0, "required maximum estimated VM workload-minutes")
	validate := fs.Bool("validate", false, "validate selection and budgets without provisioning")
	report := fs.String("report", "", "required JSON output for execution")
	junit := fs.String("junit", "", "write minimization JUnit report")
	html := fs.String("html", "", "write minimization HTML report")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) || fs.NArg() != 0 {
		return printErr("Invalid minimization", errors.New("unexpected positional arguments"))
	}
	invalidFlags := false
	fs.Visit(func(f *flag.Flag) {
		if (f.Name == "duration-resolution" && *mode != "duration") || (f.Name == "stability-resolution" && *mode != "stability") || (f.Name == "rule-index" && !isTestSeverityMinimization(*mode)) || (f.Name == "resolution" && *mode != "latency") || ((f.Name == "max-rate-kib-per-second" || f.Name == "rate-resolution-kib-per-second") && *mode != "bandwidth") || (f.Name == "percent-resolution" && *mode != "percent") || ((f.Name == "max-recovery-within" || f.Name == "recovery-resolution") && *mode != "recovery") {
			invalidFlags = true
		}
	})
	if invalidFlags {
		return printErr("Invalid minimization", errors.New("rule-index applies to severity modes; resolution to latency; rate flags to bandwidth; percent-resolution to percent; recovery flags to recovery; stability-resolution to stability; duration-resolution to duration"))
	}
	if isTestSeverityMinimization(*mode) && *ruleIndex < 0 {
		return printErr("Invalid minimization", errors.New("severity modes require --rule-index"))
	}
	if *mode == "latency" && (*resolution < time.Millisecond || *resolution > 30*time.Second || *resolution%time.Millisecond != 0) {
		return printErr("Invalid minimization", errors.New("latency resolution must be whole milliseconds from 1ms to 30s"))
	}
	if *mode == "bandwidth" && (*maxRate < 1 || *maxRate > 1000000 || *rateResolution < 1 || *rateResolution > 1000000) {
		return printErr("Invalid minimization", errors.New("bandwidth mode requires a maximum rate and resolution from 1 to 1000000 KiB/s"))
	}

	if *mode == "percent" && (*percentResolution < 1 || *percentResolution > 100) {
		return printErr("Invalid minimization", errors.New("percentage resolution must be between 1 and 100 percentage points"))
	}
	if *mode == "recovery" && (*maxRecovery < 100*time.Millisecond || *maxRecovery > testLoadMaxDuration || *maxRecovery%time.Millisecond != 0 || *recoveryResolution < time.Millisecond || *recoveryResolution > testLoadMaxDuration || *recoveryResolution%time.Millisecond != 0) {
		return printErr("Invalid minimization", errors.New("recovery mode requires a whole-millisecond maximum deadline from 100ms to 5m and resolution from 1ms to 5m"))
	}
	if *mode == "duration" && (*durationResolution < time.Millisecond || *durationResolution > testLoadMaxDuration || *durationResolution%time.Millisecond != 0) {
		return printErr("Invalid minimization", errors.New("duration resolution must be whole milliseconds from 1ms to 5m"))
	}
	if *mode == "stability" && (*stabilityResolution < time.Millisecond || *stabilityResolution > testLoadMaxDuration || *stabilityResolution%time.Millisecond != 0) {
		return printErr("Invalid minimization", errors.New("stability resolution must be whole milliseconds from 1ms to 5m"))
	}
	if *from == "" || *id == "" {
		return printErr("Invalid minimization", errors.New("--from and --run-id are required"))
	}
	if *validate && (*report != "" || *junit != "" || *html != "") {
		return printErr("Invalid minimization", errors.New("validation does not write reports"))
	}
	if !*validate && *report == "" {
		return printErr("Invalid minimization", errors.New("--report is required to save the comparison and replay configuration"))
	}
	target, err := parseTestMinimizationTarget(*assertion)
	if err != nil {
		return printErr("Invalid minimization assertion", err)
	}
	if err := validateDistinctTestReportPaths(*from, *manifest, *report, *junit, *html); err != nil {
		return printErr("Invalid minimization paths", err)
	}
	rows, _, err := readTestBaselineReport(*from, []string{*report, *junit, *html})
	if err != nil {
		return printErr("Invalid minimization report", err)
	}
	original, err := selectTestReplay(rows, *id)
	if err != nil {
		return printErr("Invalid minimization selection", err)
	}
	scenarios, dir, err := readTestManifestForScenario(*manifest, original.Scenario, nil)
	if err != nil {
		return printErr("Invalid minimization manifest", err)
	}
	prepared, err := prepareTestReplay(original, scenarios, dir)
	if err != nil {
		return printErr("Invalid minimization configuration", err)
	}
	options := testFaultMinimizationOptions{DurationResolution: *durationResolution, Mode: *mode, StabilityResolution: *stabilityResolution, MaxRecoveryWithin: *maxRecovery, RecoveryResolution: *recoveryResolution, PercentResolution: *percentResolution, MaxRateKiBPerSecond: *maxRate, RateResolutionKiBPerSecond: *rateResolution, RuleIndex: *ruleIndex, Resolution: *resolution, Target: target, MaxRuns: *maxRuns, MaxWorkloadMinutes: *maxMinutes}
	if _, err := validateTestMinimization(prepared, original, options); err != nil {
		return printErr("Invalid minimization", err)
	}
	if _, err := collectTestValidation(map[string]testScenario{prepared.Name: prepared.Scenario}, dir, prepared.Name); err != nil {
		return printErr("Invalid minimization source", err)
	}
	if *validate {
		fmt.Fprintln(osStdout, "Fault minimization configuration and budgets validated")
		return 0
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), testTerminationSignals()...)
	defer stop()
	result := runTestFaultMinimization(ctx, prepared, original, options, func(ctx context.Context, plan testRunPlan) testRunReceipt {
		fmt.Fprintf(osStderr, "Fault minimization: isolated run %d/%d\n", plan.Attempt, options.MaxRuns)
		return executeTestRunPlan(ctx, client, plan)
	})
	printTestRunResult(result, 1)
	return finishTestRunReports("", []testRunReceipt{result}, *report, *junit, *html)
}
