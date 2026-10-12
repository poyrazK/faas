package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func minimizationTestScenario() testScenario {
	scenario := faultMatrixTestScenario()
	scenario.Resilience = nil
	scenario.Timeout = "1m"
	scenario.Source = "."
	scenario.Services = map[string]testService{"cache": {Source: ".", TCPPorts: []int{6379}}, "database": {Source: ".", TCPPorts: []int{5432}}, "search": {Source: ".", TCPPorts: []int{9200}}}
	scenario.Steps[1].Load.ChaosSchedule[1].Rules = []testChaosRule{
		{To: "cache", Kind: "tcp_latency", Port: 6379, Percent: 100, Latency: "100ms"},
		{To: "database", Kind: "tcp_latency", Port: 5432, Percent: 100, Latency: "100ms"},
		{To: "search", Kind: "tcp_latency", Port: 9200, Percent: 100, Latency: "100ms"},
	}
	return scenario
}

func minimizationTestReceipt(scenario testScenario, id string, failed bool) testRunReceipt {
	now := time.Now().UTC()
	rules := len(scenario.Steps[1].Load.ChaosSchedule[1].Rules)
	fault := &testChaosEvidence{RulesInstalled: rules, Matches: make([]testChaosRuleEvidence, rules)}
	for i := range fault.Matches {
		fault.Matches[i].Matches = 1
	}
	recovery := &testLoadRecoveryEvidence{Passed: !failed, WindowsChecked: 5, WithinMS: 500, SustainMS: 200, WindowMS: 200}
	status := "passed"
	if failed {
		status = "failed"
		recovery.Error = "no sustained healthy interval within the recovery deadline"
	}
	return testRunReceipt{Scenario: "checkout", Engine: "real-vm", Profile: "cold", RunID: id, Status: status, Error: recovery.Error,
		Replay: newTestReplayManifest("checkout", "cold", "", scenario), Steps: []testScenarioStepEvidence{{Name: "load", Status: status, Load: &testLoadEvidence{testLoadMetrics: testLoadMetrics{Requests: 30}, Status: status, StopReason: "duration", ChaosPhases: []testLoadChaosPhaseEvidence{
			{Name: "healthy", StartedAt: now, FinishedAt: now.Add(time.Second)},
			{Name: "fault", Chaos: fault, StartedAt: now.Add(time.Second), FinishedAt: now.Add(2 * time.Second)},
			{Name: "recovery", Recovery: recovery, StartedAt: now.Add(2 * time.Second), FinishedAt: now.Add(3 * time.Second)},
		}}}}}
}

func minimizationTestOptions() testFaultMinimizationOptions {
	return testFaultMinimizationOptions{Target: testMinimizationTarget{Step: "load", Phase: "recovery"}, MaxRuns: 20, MaxWorkloadMinutes: 200}
}

func TestFaultMinimizationFindsReproducerAndRestarts(t *testing.T) {
	for _, kind := range []string{"one-dependency", "restart"} {
		t.Run(kind, func(t *testing.T) {
			scenario := minimizationTestScenario()
			if err := validateTestScenarioSteps(scenario, nil); err != nil {
				t.Fatal(err)
			}
			original := minimizationTestReceipt(scenario, "original", true)
			prepared := testPreparedScenario{Name: "checkout", Engine: "real-vm", Scenario: scenario}
			fingerprint := newTestReplayManifest("checkout", "cold", "", scenario).ConfigurationSHA256
			calls := 0
			result := runTestFaultMinimization(context.Background(), prepared, &original, minimizationTestOptions(), func(ctx context.Context, plan testRunPlan) testRunReceipt {
				calls++
				if plan.Attempt != calls || plan.ReplayOf != "original" || plan.Prepared.Scenario.Resilience != nil {
					t.Fatal("run not isolated or not linked")
				}
				present := map[string]bool{}
				for _, r := range plan.Prepared.Scenario.Steps[1].Load.ChaosSchedule[1].Rules {
					present[r.To] = true
				}
				failed := present["database"]
				if kind == "restart" {
					failed = present["search"] && (present["cache"] || !present["database"])
				}
				return minimizationTestReceipt(plan.Prepared.Scenario, fmt.Sprintf("run-%d", calls), failed)
			})
			e := result.Minimization
			if result.Status != "passed" || e.Status != "minimized" || !e.OriginalConfirmed || e.OriginalRules != 3 || e.RemainingRules != 1 || calls != 8 {
				t.Fatalf("result=%+v calls=%d", e, calls)
			}
			if newTestReplayManifest("checkout", "cold", "", scenario).ConfigurationSHA256 != fingerprint {
				t.Fatal("source scenario mutated")
			}
			selected, err := selectTestReplay([]testRunReceipt{result}, e.BestRunID)
			if err != nil {
				t.Fatal(err)
			}
			replay, err := prepareTestReplay(selected, map[string]testScenario{"checkout": scenario}, "")
			if err != nil {
				t.Fatal(err)
			}
			want := "database"
			if kind == "restart" {
				want = "search"
			}
			remaining := replay.Scenario.Steps[1].Load.ChaosSchedule[1].Rules
			if len(remaining) != 1 || remaining[0].To != want {
				t.Fatalf("wrong replay: %+v", remaining)
			}
			// Removed rules are also protected by the original fingerprint.
			changed := minimizationTestScenario()
			changed.Steps[1].Load.ChaosSchedule[1].Rules[0].Latency = "200ms"
			if _, err := prepareTestReplay(selected, map[string]testScenario{"checkout": changed}, ""); err == nil {
				t.Fatal("changed removed rule accepted")
			}
		})
	}
}

func TestFaultMinimizationInconclusiveAndBounds(t *testing.T) {
	for _, kind := range []string{"original-pass", "original-flaky", "candidate-flaky", "cleanup", "candidate-cleanup", "cancel", "run-budget", "minute-budget", "partial-best", "different-failure", "missing-evidence", "no-traffic", "unfinished-fault"} {
		t.Run(kind, func(t *testing.T) {
			scenario := minimizationTestScenario()
			original := minimizationTestReceipt(scenario, "original", true)
			options := minimizationTestOptions()
			if kind == "run-budget" {
				options.MaxRuns = 2
			}
			if kind == "minute-budget" {
				options.MaxWorkloadMinutes = 8
			}
			if kind == "partial-best" {
				options.MaxRuns = 5
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			result := runTestFaultMinimization(ctx, testPreparedScenario{Name: "checkout", Engine: "real-vm", Scenario: scenario}, &original, options, func(ctx context.Context, plan testRunPlan) testRunReceipt {
				calls++
				failed := true
				if kind == "original-pass" || (kind == "original-flaky" && calls == 2) || (kind == "candidate-flaky" && calls == 4) {
					failed = false
				}
				run := minimizationTestReceipt(plan.Prepared.Scenario, fmt.Sprintf("run-%d", calls), failed)
				if (kind == "cleanup" && calls == 1) || (kind == "candidate-cleanup" && calls == 3) {
					run.CleanupError = "destroy failed"
				}
				if kind == "cancel" {
					cancel()
				}
				if kind == "different-failure" {
					run.Steps[0].Load.ChaosPhases[2].Recovery.Passed = true
					run.Error = "unrelated assertion failed"
				}
				if kind == "no-traffic" {
					run.Steps[0].Load.Requests = 0
				}
				if kind == "unfinished-fault" {
					run.Steps[0].Load.ChaosPhases[1].FinishedAt = time.Time{}
				}
				if kind == "missing-evidence" {
					run.Steps[0].Load.ChaosPhases[1].Chaos = nil
				}
				return run
			})
			e := result.Minimization
			if result.Status != "failed" || e.Status != "inconclusive" || e.Reason == "" || e.RunsUsed != calls || calls > options.MaxRuns || e.EstimatedWorkloadMinutes > options.MaxWorkloadMinutes {
				t.Fatalf("unbounded or conclusive: %+v calls=%d", e, calls)
			}
			maxCalls := 2
			switch kind {
			case "cleanup", "cancel", "different-failure", "missing-evidence", "no-traffic", "unfinished-fault":
				maxCalls = 1
			case "candidate-cleanup":
				maxCalls = 3
			case "candidate-flaky", "partial-best":
				maxCalls = 4
			}
			if calls != maxCalls {
				t.Fatalf("calls=%d want %d", calls, maxCalls)
			}
			if kind == "partial-best" && (e.RemainingRules != 2 || len(e.Replay.RemovedRules) != 1 || e.BestRunID != "run-4") {
				t.Fatal("last confirmed best was lost")
			}
			if strings.Contains(kind, "cleanup") && result.CleanupError == "" {
				t.Fatal("cleanup failure lost")
			}
		})
	}
}

func TestFaultMinimizationUnchangedAndEndpointSelection(t *testing.T) {
	scenario := minimizationTestScenario()
	spec := scenario.Steps[1].Load.ChaosSchedule[2].Recovery
	scenario.Steps[1].Load.ChaosSchedule[2].Recovery = nil
	scenario.Steps[1].Load.ChaosSchedule[2].StepRecovery = map[string]*testLoadRecoverySpec{"load-probe": spec}
	options := minimizationTestOptions()
	options.Target.Request = "load-probe"
	endpoint := func(s testScenario, id string, failed bool) testRunReceipt {
		r := minimizationTestReceipt(s, id, failed)
		phase := &r.Steps[0].Load.ChaosPhases[2]
		phase.StepRecovery = map[string]*testLoadRecoveryEvidence{"load-probe": phase.Recovery}
		phase.Recovery = nil
		return r
	}
	original := endpoint(scenario, "original", true)
	calls := 0
	result := runTestFaultMinimization(context.Background(), testPreparedScenario{Name: "checkout", Engine: "real-vm", Scenario: scenario}, &original, options, func(ctx context.Context, plan testRunPlan) testRunReceipt {
		calls++
		return endpoint(plan.Prepared.Scenario, fmt.Sprintf("run-%d", calls), len(plan.Prepared.Scenario.Steps[1].Load.ChaosSchedule[1].Rules) == 3)
	})
	if result.Status != "passed" || result.Minimization.Status != "unchanged" || calls != 8 || result.Minimization.RemainingRules != 3 {
		t.Fatalf("%+v calls=%d", result.Minimization, calls)
	}
	options.Target.Request = "missing"
	if _, err := validateTestMinimization(testPreparedScenario{Engine: "real-vm", Scenario: scenario}, &original, options); err == nil {
		t.Fatal("unknown endpoint accepted")
	}
}

func TestFaultMinimizationReportsAndCLIValidation(t *testing.T) {
	dir := t.TempDir()
	scenario := minimizationTestScenario()
	for name, body := range map[string]string{"package.json": `{"name":"minimize-test","version":"1.0.0","scripts":{"start":"node server.js"}}`, "server.js": `console.log("ready")`} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	body, err := yaml.Marshal(testManifest{Version: 1, Scenarios: map[string]testScenario{"checkout": scenario}})
	if err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(dir, "gregale-test.yaml")
	if err := os.WriteFile(manifest, body, 0600); err != nil {
		t.Fatal(err)
	}
	// Parse the same normalization that the original CLI uses before hashing.
	scenarios, _, err := readTestManifestForScenario(manifest, "checkout", nil)
	if err != nil {
		t.Fatal(err)
	}
	scenario = scenarios["checkout"]
	original := minimizationTestReceipt(scenario, "original", true)
	from := filepath.Join(dir, "failed.json")
	if err := writeTestReport(from, []testRunReceipt{original}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAAS_TOKEN", "")
	args := []string{"minimize", "--from", from, "--run-id", "original", "--manifest", manifest, "--assertion", "load/recovery", "--max-runs", "10", "--max-workload-minutes", "40", "--validate"}
	if code := cmdTest(args); code != 0 {
		t.Fatalf("offline minimization validation=%d", code)
	}
	args[len(args)-2] = "0"
	if code := cmdTest(args); code == 0 {
		t.Fatal("unbounded command accepted")
	}
	alias := filepath.Join(dir, "alias.json")
	if err := os.Link(from, alias); err != nil {
		t.Fatal(err)
	}
	if code := cmdTestMinimize([]string{"--from", from, "--run-id", "original", "--assertion", "load/recovery", "--report", alias, "--max-workload-minutes", "40"}); code == 0 {
		t.Fatal("input overwrite accepted")
	}
	calls := 0
	result := runTestFaultMinimization(context.Background(), testPreparedScenario{Name: "checkout", Engine: "real-vm", Scenario: scenario}, &original, minimizationTestOptions(), func(ctx context.Context, plan testRunPlan) testRunReceipt {
		calls++
		return minimizationTestReceipt(plan.Prepared.Scenario, fmt.Sprintf("run-%d", calls), true)
	})
	output := filepath.Join(dir, "minimized.json")
	if err := writeTestReport(output, []testRunReceipt{result}); err != nil {
		t.Fatal(err)
	}
	rows, _, err := readTestBaselineReport(output, nil)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := selectTestReplay(rows, result.Minimization.BestRunID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prepareTestReplay(selected, scenarios, dir); err != nil {
		t.Fatal(err)
	}
	if selected.Replay.Version != 2 {
		t.Fatal("reduced manifest did not use version 2")
	}
	if code := cmdTestReplay([]string{"--from", output, "--run-id", result.Minimization.BestRunID, "--manifest", manifest, "--validate"}); code != 0 {
		t.Fatalf("offline reduced replay validation=%d", code)
	}

	html := filepath.Join(dir, "minimized.html")
	if err := writeTestHTMLReport(html, "", []testRunReceipt{result}); err != nil {
		t.Fatal(err)
	}
	rendered, err := os.ReadFile(html)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rendered), "Fault minimization: minimized") || !strings.Contains(string(rendered), result.Minimization.BestRunID) {
		t.Fatal("HTML missing comparison or replay")
	}
	junit := filepath.Join(dir, "minimized.xml")
	if err := writeTestJUnit(junit, []testRunReceipt{result}); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(result.Minimization.Replay)
	if err != nil || strings.Contains(string(encoded), "Secrets") {
		t.Fatal("replay manifest contains configuration")
	}
}

func TestFaultMinimizationRemovalValidation(t *testing.T) {
	scenario := minimizationTestScenario()
	for _, refs := range [][]testFaultRuleRef{
		{{"load", "fault", -1}}, {{"load", "fault", 30}}, {{"missing", "fault", 0}}, {{"load", "fault", 0}, {"load", "fault", 0}}, {{"load", "fault", 0}, {"load", "fault", 1}, {"load", "fault", 2}},
	} {
		if _, err := removeTestFaultRules(scenario, refs); err == nil {
			t.Fatalf("invalid refs accepted: %+v", refs)
		}
	}
	for _, value := range []string{"", "load", "load/recovery/", "load/recovery/checkout/extra"} {
		if _, err := parseTestMinimizationTarget(value); err == nil {
			t.Fatalf("invalid target accepted: %q", value)
		}
	}
	if target, err := parseTestMinimizationTarget("load/recovery/checkout"); err != nil || target.Request != "checkout" {
		t.Fatal("endpoint target parse failed")
	}
}

func TestFaultMinimizationReducedMatrixReplay(t *testing.T) {
	template := minimizationTestScenario()
	rules := append([]testChaosRule(nil), template.Steps[1].Load.ChaosSchedule[1].Rules...)
	template.Resilience = &testResilienceSpec{Runs: 2, MinPassRate: 1, FaultMatrix: []testFaultMatrixCaseSpec{
		{Name: "composite", Step: "load", Phase: "fault", Rules: rules},
		{Name: "single", Step: "load", Phase: "fault", Rules: rules[:1]},
	}}
	variant, err := applyTestFaultMatrixCase(template, template.Resilience.FaultMatrix[0])
	if err != nil {
		t.Fatal(err)
	}
	original := minimizationTestReceipt(variant, "matrix-original", true)
	original.Replay = newTestReplayManifest("checkout", "cold", "composite", variant)
	prepared, err := prepareTestReplay(&original, map[string]testScenario{"checkout": template}, "")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	result := runTestFaultMinimization(context.Background(), prepared, &original, minimizationTestOptions(), func(ctx context.Context, plan testRunPlan) testRunReceipt {
		calls++
		if plan.ReplayCase != "composite" || plan.ReplayManifest.MatrixCase != "composite" {
			t.Fatal("matrix case lost")
		}
		return minimizationTestReceipt(plan.Prepared.Scenario, fmt.Sprintf("matrix-%d", calls), true)
	})
	selected, err := selectTestReplay([]testRunReceipt{result}, result.Minimization.BestRunID)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := prepareTestReplay(selected, map[string]testScenario{"checkout": template}, "")
	if err != nil {
		t.Fatal(err)
	}
	if replay.Scenario.Resilience != nil || len(replay.Scenario.Steps[1].Load.ChaosSchedule[1].Rules) != 1 {
		t.Fatal("reduced replay expanded matrix or restored removed rules")
	}
	for _, kind := range []string{"old-version", "bad-hash", "unsupported-version"} {
		changed := *selected
		manifest := *selected.Replay
		changed.Replay = &manifest
		switch kind {
		case "old-version":
			manifest.Version = 1
		case "bad-hash":
			manifest.ReducedConfigurationSHA256 = "invalid"
		case "unsupported-version":
			manifest.Version = 8
		}
		if _, err := prepareTestReplay(&changed, map[string]testScenario{"checkout": template}, ""); err == nil {
			t.Fatalf("accepted %s", kind)
		}
	}
	if len(template.Resilience.FaultMatrix[0].Rules) != 3 || len(template.Steps[1].Load.ChaosSchedule[1].Rules) != 3 {
		t.Fatal("matrix template changed")
	}
}

func TestFaultMinimizationReservesRoundedRunEstimates(t *testing.T) {
	scenario := minimizationTestScenario()
	scenario.Timeout = "80s"
	prepared := testPreparedScenario{Name: "checkout", Engine: "real-vm", Scenario: scenario}
	original := minimizationTestReceipt(scenario, "original", true)
	options := minimizationTestOptions()
	options.MaxWorkloadMinutes = 11
	if _, err := validateTestMinimization(prepared, &original, options); err == nil {
		t.Fatal("pair accepted with room only for the unrounded combined estimate")
	}
	options.MaxWorkloadMinutes = 12
	if _, err := validateTestMinimization(prepared, &original, options); err != nil {
		t.Fatal(err)
	}
	calls := 0
	result := runTestFaultMinimization(context.Background(), prepared, &original, options, func(ctx context.Context, plan testRunPlan) testRunReceipt {
		calls++
		return minimizationTestReceipt(plan.Prepared.Scenario, fmt.Sprintf("run-%d", calls), true)
	})
	if calls != 2 || result.Minimization.EstimatedWorkloadMinutes != 12 || result.Minimization.Status != "inconclusive" {
		t.Fatal("per-run rounded reservation was not enforced")
	}
}

func TestFaultMinimizationPreservesRampedLoad(t *testing.T) {
	scenario := minimizationTestScenario()
	target := 2
	scenario.Steps[1].Load.Duration = ""
	scenario.Steps[1].Load.Stages = []testLoadStageSpec{{Duration: "3s", Target: &target}}
	receiptFor := func(s testScenario, id string) testRunReceipt {
		r := minimizationTestReceipt(s, id, true)
		r.Steps[0].Load.StopReason = "stages"
		return r
	}
	original := receiptFor(scenario, "original")
	calls := 0
	result := runTestFaultMinimization(context.Background(), testPreparedScenario{Name: "checkout", Engine: "real-vm", Scenario: scenario}, &original, minimizationTestOptions(), func(ctx context.Context, plan testRunPlan) testRunReceipt {
		calls++
		load := plan.Prepared.Scenario.Steps[1].Load
		if load.Duration != "" || len(load.Stages) != 1 || *load.Stages[0].Target != 2 {
			t.Fatal("ramp configuration changed")
		}
		return receiptFor(plan.Prepared.Scenario, fmt.Sprintf("ramp-%d", calls))
	})
	if result.Minimization.Status != "minimized" || calls != 6 {
		t.Fatalf("ramped load result=%+v calls=%d", result.Minimization, calls)
	}
}
