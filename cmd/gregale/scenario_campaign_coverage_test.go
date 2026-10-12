package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func campaignCoverageScenario() testScenario {
	s := faultCampaignScenario()
	s.Resilience = &testResilienceSpec{Runs: 1, MinPassRate: 1, CampaignCoverage: &testCampaignCoverageSpec{Step: "load", Seeds: []uint64{42, 43, 44}, MaxRuns: 3, MaxWorkloadMinutes: 12}}
	return s
}

func campaignCoverageReceipt(t *testing.T, plan testRunPlan, id string, failed bool) testRunReceipt {
	t.Helper()
	cfg := outageCyclesConfig(t, plan.Prepared.Scenario)
	load := outageCyclesReceipt(t, cfg)
	for i, phase := range cfg.ChaosSchedule {
		load.Requests += 10
		if len(phase.Rules) == 0 {
			continue
		}
		p, err := (&testChaosSpec{Duration: phase.Duration.String(), Rules: phase.Rules}).plan()
		if err != nil {
			t.Fatal(err)
		}
		load.ChaosPhases[i].Chaos.Rules = p.Rules
		load.ChaosPhases[i].Chaos.Matches[0].Rule = p.Rules[0]
	}
	status := "passed"
	if failed {
		status = "failed"
		load.Status = status
		r := load.ChaosPhases[4].Recovery
		r.Passed, r.WindowsChecked, r.Error = false, 5, "no sustained healthy interval within the recovery deadline"
	}
	_ = applyTestOutageCycles(load, cfg, nil)
	return testRunReceipt{Scenario: "checkout", Engine: "real-vm", Profile: "cold", RunID: id, Status: status, CampaignSeed: plan.CampaignSeed, Replay: newTestReplayManifest("checkout", "cold", plan.ReplayCase, plan.Prepared.Scenario), Steps: []testScenarioStepEvidence{{Name: "load", Load: load}}}
}

func TestCampaignCoverageExpansionLimitsAndIsolation(t *testing.T) {
	s := campaignCoverageScenario()
	before, _ := json.Marshal(s)
	x, err := expandTestFaultSweep(s)
	if err != nil {
		t.Fatal(err)
	}
	if testResilienceRuns(s) != 3 || estimateTestWorkloadMinutes(x, 1) != 12 || len(x.Resilience.FaultMatrix) != 3 {
		t.Fatal("wrong seed resource estimate")
	}
	if err := validateTestResilience(x); err != nil {
		t.Fatal(err)
	}
	again, err := expandTestFaultSweep(x)
	if err != nil || !reflect.DeepEqual(x, again) {
		t.Fatal("coverage expansion is not idempotent", err)
	}
	for _, c := range x.Resilience.FaultMatrix {
		variant, err := applyTestFaultMatrixCase(x, c)
		if err != nil {
			t.Fatal(err)
		}
		if variant.Resilience.CampaignCoverage != nil || len(variant.Resilience.FaultMatrix) != 0 {
			t.Fatal("recursive coverage")
		}
		one := faultCampaignScenario()
		*one.Steps[1].Load.FaultCampaign.Seed = c.Campaign.Seed
		expected, err := expandTestOutageCycles(one)
		if err != nil || !reflect.DeepEqual(variant.Steps[1].Load.ChaosSchedule, expected.Steps[1].Load.ChaosSchedule) {
			t.Fatal("seed variant differs from standalone campaign", err)
		}
		variant.Steps[1].Load.ChaosSchedule[1].Rules[0].Percent = 1
	}
	after, _ := json.Marshal(s)
	if !bytes.Equal(before, after) {
		t.Fatal("campaign source mutated")
	}
	if err := validateTestCampaignCoverageBatch(x, 2); err == nil {
		t.Fatal("combined profiles/repeats exceed max_runs")
	}
	x.Resilience.CampaignCoverage.MaxRuns = 6
	x.Resilience.CampaignCoverage.MaxWorkloadMinutes = 20
	if err := validateTestCampaignCoverageBatch(x, 2); err == nil {
		t.Fatal("combined workload cap ignored")
	}
	x.Resilience.CampaignCoverage.MaxWorkloadMinutes = 24
	if err := validateTestCampaignCoverageBatch(x, 2); err != nil {
		t.Fatal("exact cap rejected", err)
	}
}

func TestCampaignCoverageRejectsInvalidInputs(t *testing.T) {
	for _, kind := range []string{"empty", "duplicate", "too-many", "runs", "run-cap", "minutes", "cost", "step", "campaign", "matrix", "sweep", "timeout", "seed-duration"} {
		t.Run(kind, func(t *testing.T) {
			s := campaignCoverageScenario()
			c := s.Resilience.CampaignCoverage
			switch kind {
			case "empty":
				c.Seeds = nil
			case "duplicate":
				c.Seeds[1] = c.Seeds[0]
			case "too-many":
				c.Seeds = make([]uint64, 11)
			case "runs":
				s.Resilience.Runs = 0
			case "run-cap":
				c.MaxRuns = 2
			case "minutes":
				c.MaxWorkloadMinutes = 0
			case "cost":
				c.MaxWorkloadMinutes = 11
			case "step":
				c.Step = "missing"
			case "campaign":
				s.Steps[1].Load.FaultCampaign = nil
			case "matrix":
				s.Resilience.FaultMatrix = []testFaultMatrixCaseSpec{{Name: "extra"}}
			case "sweep":
				s.Resilience.FaultSweep = &testFaultSweepSpec{}
			case "timeout":
				s.Timeout = "invalid"
			case "seed-duration":
				s.Steps[1].Load.FaultCampaign.MaxDuration = "7s"
			}
			if _, err := expandTestFaultSweep(s); err == nil {
				t.Fatal("invalid coverage accepted")
			}
		})
	}
	s := campaignCoverageScenario()
	s.Resilience.CampaignCoverage.Seeds[0] = 0
	if _, err := expandTestFaultSweep(s); err != nil {
		t.Fatal("explicit zero seed rejected", err)
	}
}

func TestCampaignCoverageRunsReportsReplayAndMinimization(t *testing.T) {
	s := campaignCoverageScenario()
	calls := 0
	result := runSweepTest(context.Background(), s, func(ctx context.Context, plan testRunPlan) testRunReceipt {
		calls++
		return campaignCoverageReceipt(t, plan, fmt.Sprintf("coverage-%d", calls), plan.CampaignSeed.Seed == 43)
	})
	if calls != 3 || result.Status != "failed" || result.FaultMatrix.Passed != 2 || result.FaultMatrix.Failed != 1 {
		t.Fatal("wrong seed results", calls, result.Status)
	}
	e := result.FaultMatrix.CampaignCoverage
	if e == nil || len(e.Seeds) != 3 || e.Seeds[1].Seed != 43 || e.Seeds[1].Runs[0].FirstFailedCycle != 2 || len(e.Seeds[1].Runs[0].RecoveryFailures) != 1 {
		t.Fatal("missing seed failures", e)
	}
	var planned, installed, exercised int
	for _, choice := range e.Choices {
		planned += choice.Planned
		installed += choice.Installed
		exercised += choice.Exercised
	}
	if planned != 9 || installed != 9 || exercised != 9 {
		t.Fatal("incorrect choice coverage", e.Choices)
	}
	original := result.FaultMatrix.Cases[1].Resilience.History[0]
	selected, err := selectTestReplay([]testRunReceipt{result}, original.RunID)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := prepareTestReplay(selected, map[string]testScenario{"checkout": s}, ".")
	if err != nil || *prepared.Scenario.Steps[1].Load.FaultCampaign.Seed != 43 || prepared.Scenario.Resilience != nil {
		t.Fatal("wrong seed replay", err)
	}
	changed := campaignCoverageScenario()
	changed.Resilience.CampaignCoverage.Seeds[1] = 45
	if _, err := prepareTestReplay(selected, map[string]testScenario{"checkout": changed}, "."); err == nil {
		t.Fatal("removed seed replay accepted")
	}
	opts := minimizationTestOptions()
	opts.Target = testMinimizationTarget{Step: "load", Phase: "recovery-cycle-2"}
	minimized := runTestFaultMinimization(context.Background(), prepared, selected, opts, func(ctx context.Context, plan testRunPlan) testRunReceipt {
		return campaignCoverageReceipt(t, plan, fmt.Sprintf("coverage-min-%d", plan.Attempt), true)
	})
	if minimized.Status != "passed" || minimized.Minimization.RemainingRules != 1 || minimized.Minimization.Replay.MatrixCase != "campaign-seed-43" {
		t.Fatalf("seed identity lost in minimization: %+v", minimized.Minimization)
	}
	best, err := selectTestReplay([]testRunReceipt{minimized}, minimized.Minimization.BestRunID)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := prepareTestReplay(best, map[string]testScenario{"checkout": s}, ".")
	if err != nil || len(replayed.Scenario.Steps[1].Load.ChaosSchedule[1].Rules) != 0 || *replayed.Scenario.Steps[1].Load.FaultCampaign.Seed != 43 {
		t.Fatal("minimized seed replay redrawn", err)
	}
	dir := t.TempDir()
	html := filepath.Join(dir, "coverage.html")
	if err := writeTestHTMLReport(html, "", []testRunReceipt{result}); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(html)
	if !strings.Contains(string(body), "Campaign coverage") || !strings.Contains(string(body), "coverage-2") || !strings.Contains(string(body), "recovery-cycle-2") {
		t.Fatal("HTML coverage missing")
	}
	xml := filepath.Join(dir, "coverage.xml")
	if err := writeTestJUnit(xml, []testRunReceipt{result}); err != nil {
		t.Fatal(err)
	}
	body, _ = os.ReadFile(xml)
	if !strings.Contains(string(body), "campaign_coverage") || !strings.Contains(string(body), "campaign-seed-43") {
		t.Fatal("JUnit coverage missing")
	}
	var out bytes.Buffer
	printTestCampaignCoverage(&out, e)
	if !strings.Contains(out.String(), "seed 43: failed") || !strings.Contains(out.String(), "coverage-2") {
		t.Fatal("terminal seed failure missing")
	}
}

func TestCampaignCoverageDoesNotCountUnmatchedOrInterruptedFaults(t *testing.T) {
	for _, kind := range []string{"no-matches", "wrong-match-seed", "wrong-installed-seed", "missing-window", "cleanup", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			s := campaignCoverageScenario()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			result := runSweepTest(ctx, s, func(ctx context.Context, plan testRunPlan) testRunReceipt {
				calls++
				r := campaignCoverageReceipt(t, plan, fmt.Sprintf("coverage-partial-%d", calls), false)
				p := &r.Steps[0].Load.ChaosPhases[1]
				switch kind {
				case "no-matches":
					p.Chaos.Matches[0].Matches = 0
				case "wrong-match-seed":
					p.Chaos.Matches[0].Rule.Seed++
				case "wrong-installed-seed":
					p.Chaos.Rules[0].Seed++
				case "missing-window":
					p.FinishedAt = p.StartedAt
				case "cleanup":
					r.CleanupError = "cleanup failed"
				case "cancel":
					cancel()
				}
				return r
			})
			e := result.FaultMatrix.CampaignCoverage
			var planned, installed, exercised int
			for _, c := range e.Choices {
				planned += c.Planned
				installed += c.Installed
				exercised += c.Exercised
			}
			if planned != 9 {
				t.Fatal("skips lost planned coverage")
			}
			if kind == "cleanup" || kind == "cancel" {
				if calls != 1 || result.Status != "failed" || e.Seeds[1].Skipped != 1 || installed != 3 || exercised != 3 {
					t.Fatal("skipped seeds counted as executed", calls, e)
				}
			} else if exercised != 6 || ((kind == "wrong-installed-seed" || kind == "missing-window") && installed != 6) {
				t.Fatal("invalid coverage evidence counted", e.Choices)
			}
		})
	}
}

func TestCampaignCoverageRepeatsRequireEachSeedsPassPolicy(t *testing.T) {
	for _, tolerance := range []bool{false, true} {
		t.Run(fmt.Sprint(tolerance), func(t *testing.T) {
			s := campaignCoverageScenario()
			s.Resilience.Runs = 2
			s.Resilience.CampaignCoverage.MaxRuns = 6
			s.Resilience.CampaignCoverage.MaxWorkloadMinutes = 24
			if tolerance {
				s.Resilience.MinPassRate = 0.5
			}
			calls := 0
			result := runSweepTest(context.Background(), s, func(ctx context.Context, plan testRunPlan) testRunReceipt {
				calls++
				return campaignCoverageReceipt(t, plan, fmt.Sprintf("coverage-repeat-%d", calls), plan.CampaignSeed.Seed == 43 && plan.Attempt == 1)
			})
			e := result.FaultMatrix.CampaignCoverage
			if calls != 6 || e.Seeds[1].Failed != 1 || e.Seeds[1].Passed != 1 || len(e.Seeds[1].Runs[0].RecoveryFailures) != 1 {
				t.Fatal("repeated seed failure lost", e)
			}
			if (result.Status == "passed") != tolerance {
				t.Fatal("other seeds compensated for failing seed", result.Status)
			}
			var planned, exercised int
			for _, choice := range e.Choices {
				planned += choice.Planned
				exercised += choice.Exercised
			}
			if planned != 18 || exercised != 18 {
				t.Fatal("repeat coverage was not accumulated")
			}
		})
	}
}

func TestCampaignCoverageOfflineCLI(t *testing.T) {
	s := campaignCoverageScenario()
	dir := t.TempDir()
	for name, body := range map[string]string{"package.json": `{"scripts":{"start":"node server.js"}}`, "server.js": "console.log('fixture');"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	body, err := yaml.Marshal(testManifest{Version: 1, Scenarios: map[string]testScenario{"checkout": s}})
	if err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(dir, "gregale-test.yaml")
	if err := os.WriteFile(manifest, body, 0600); err != nil {
		t.Fatal(err)
	}
	scenarios, _, err := readTestManifestForScenario(manifest, "checkout", nil)
	if err != nil {
		t.Fatal(err)
	}
	normalized := scenarios["checkout"]
	variant, err := applyTestFaultMatrixCase(normalized, normalized.Resilience.FaultMatrix[1])
	if err != nil {
		t.Fatal(err)
	}
	row := testRunReceipt{Scenario: "checkout", Engine: "real-vm", Profile: "cold", RunID: "coverage-cli", Status: "failed", Replay: newTestReplayManifest("checkout", "cold", "campaign-seed-43", variant)}
	report := filepath.Join(dir, "failed.json")
	if err := writeTestReport(report, []testRunReceipt{row}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAAS_TOKEN", "")
	if cmdTest([]string{"--manifest", manifest, "--scenario", "checkout", "--validate"}) != 0 || cmdTestReplay([]string{"--manifest", manifest, "--from", report, "--run-id", row.RunID, "--validate"}) != 0 {
		t.Fatal("offline coverage validation/replay failed")
	}
	var stderr bytes.Buffer
	previousErr := osStderr
	osStderr = &stderr
	t.Cleanup(func() { osStderr = previousErr })
	if cmdTest([]string{"--manifest", manifest, "--scenario", "checkout", "--profile", "all"}) != 1 || !strings.Contains(stderr.String(), "Campaign resource guard exceeded") {
		t.Fatal("combined CLI run cap was not checked before login", stderr.String())
	}
	prepared := []testPreparedScenario{{Name: "checkout", Engine: "real-vm", Scenario: normalized, Profiles: []string{"cold", "warm"}}}
	if err := validateTestSuiteOptions(prepared, testSuiteOptions{Repeat: 1}); err == nil || !strings.Contains(err.Error(), "max_runs") {
		t.Fatal("suite ignored combined campaign cap", err)
	}
}
