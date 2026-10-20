package main_test

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"gopkg.in/yaml.v3"
)

func resilienceTestPlan(runs int, rate float64) main.TestRunPlan {
	return main.TestRunPlan{Profile: "warm", Prepared: &main.TestPreparedScenario{Name: "resilience", Engine: "real-vm", Scenario: main.TestScenario{Resilience: &main.TestResilienceSpec{Runs: runs, MinPassRate: rate}}}}
}

func TestResiliencePassRateAndRecoveryEvidence(t *testing.T) {
	plan := resilienceTestPlan(10, .9)
	calls := 0
	receipt := main.RunTestResilience(context.Background(), plan, func(_ context.Context, p main.TestRunPlan) main.TestRunReceipt {
		if p.Prepared.Scenario.Resilience != nil {
			t.Fatal("recursive resilience spec")
		}
		calls++
		value := float64(calls * 100)
		run := main.TestRunReceipt{Scenario: "resilience", Profile: "warm", Engine: "real-vm", RunID: string(rune('a' + calls)), Status: "passed", Steps: []main.TestScenarioStepEvidence{{Name: "load", Load: &main.TestLoadEvidence{ChaosPhases: []main.TestLoadChaosPhaseEvidence{{Name: "recovery", Recovery: &main.TestLoadRecoveryEvidence{Passed: true, RecoveryMS: &value}}}}}}}
		if calls == 10 {
			run.Status = "failed"
			run.Error = "later check failed"
		}
		return run
	})
	e := receipt.Resilience
	if calls != 10 || receipt.Status != "passed" || e.Passed != 9 || e.Failed != 1 || e.PassRate != .9 || e.RequiredPasses != 9 || len(e.History) != 10 {
		t.Fatalf("unexpected group: %+v", receipt)
	}
	if len(e.Recovery) != 1 || e.Recovery[0].Samples != 10 || e.Recovery[0].P50MS != 500 || e.Recovery[0].P95MS != 1000 {
		t.Fatalf("unexpected recovery summary: %+v", e.Recovery)
	}
	if plan.Prepared.Scenario.Resilience == nil {
		t.Fatal("original scenario mutated")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "results.xml")
	if err := main.WriteTestJUnit(path, []main.TestRunReceipt{receipt}); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(path)
	var suite main.TestJUnitSuite
	if err := xml.Unmarshal(body, &suite); err != nil || suite.Tests != 1 || suite.Failures != 0 {
		t.Fatalf("allowed failure failed JUnit: %+v %v", suite, err)
	}
	var decoded main.TestRunReceipt
	if err := json.Unmarshal([]byte(suite.Cases[0].SystemOut), &decoded); err != nil || len(decoded.Resilience.History) != 10 || decoded.Resilience.History[9].Error != "later check failed" {
		t.Fatalf("individual evidence lost: %v", err)
	}
	path = filepath.Join(dir, "results.html")
	if err := main.WriteTestHTMLReport(path, "", []main.TestRunReceipt{receipt}); err != nil {
		t.Fatal(err)
	}
	body, _ = os.ReadFile(path)
	for _, text := range []string{"9/10 passed", "Recovery p95", "1000.000 ms", "Run 10: failed", "later check failed"} {
		if !strings.Contains(string(body), text) {
			t.Fatalf("HTML missing %q", text)
		}
	}
}

func TestResilienceCleanupAndCancellationStopRuns(t *testing.T) {
	for _, kind := range []string{"cleanup", "cancel", "budget"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			receipt := main.RunTestResilience(ctx, resilienceTestPlan(10, .8), func(_ context.Context, _ main.TestRunPlan) main.TestRunReceipt {
				calls++
				run := main.TestRunReceipt{Status: "passed"}
				if kind == "cleanup" && calls == 9 {
					run.CleanupError = "namespace deletion failed"
				}
				if kind == "cancel" && calls == 9 {
					cancel()
				}
				if kind == "budget" && calls >= 8 {
					run.Status = "failed"
				}
				return run
			})
			if receipt.Status != "failed" {
				t.Fatal("group passed despite stop or failed budget")
			}
			if kind != "budget" && (calls != 9 || receipt.Resilience.Skipped != 1) {
				t.Fatalf("execution continued: %+v", receipt.Resilience)
			}
			if kind == "cleanup" && receipt.CleanupError == "" {
				t.Fatal("cleanup evidence lost")
			}
			if kind == "budget" && (calls != 10 || receipt.Resilience.Passed != 7) {
				t.Fatal("ordinary failures stopped group")
			}
		})
	}
}

func TestResilienceConfigurationAndResourceEstimates(t *testing.T) {
	var scenario main.TestScenario
	if err := yaml.Unmarshal([]byte("resilience:\n  runs: 10\n  min_pass_rate: 0.9\n"), &scenario); err != nil {
		t.Fatal(err)
	}
	scenario.Timeout = "1m"
	scenario.Steps = []main.TestScenarioStep{{Requests: []main.TestHTTPRequest{main.LoadTestStep()}}, {Load: &main.TestLoadSpec{ChaosSchedule: []main.TestLoadChaosPhaseSpec{{Recovery: &main.TestLoadRecoverySpec{}}}}}}
	if err := main.ValidateTestResilience(scenario); err != nil {
		t.Fatal(err)
	}
	if got := main.EstimateTestWorkloadMinutes(scenario, 2); got != 20 {
		t.Fatalf("estimates omitted repetitions: %d", got)
	}
	for _, spec := range []main.TestResilienceSpec{{Runs: 1, MinPassRate: .9}, {Runs: 21, MinPassRate: .9}, {Runs: 10, MinPassRate: 0}, {Runs: 10, MinPassRate: 1.01}, {Runs: 10, MinPassRate: math.NaN()}} {
		scenario.Resilience = &spec
		if err := main.ValidateTestResilience(scenario); err == nil {
			t.Fatalf("invalid spec accepted: %+v", spec)
		}
	}
	scenario.Resilience = &main.TestResilienceSpec{Runs: 10, MinPassRate: .9}
	scenario.Steps = nil
	if err := main.ValidateTestResilience(scenario); err == nil {
		t.Fatal("unstaged resilience accepted")
	}
}

func TestResilienceRecoverySummaryExcludesUnconfirmedSamples(t *testing.T) {
	value := 100.0
	history := []main.TestRunReceipt{{Steps: []main.TestScenarioStepEvidence{{Name: "load", Load: &main.TestLoadEvidence{ChaosPhases: []main.TestLoadChaosPhaseEvidence{{Name: "recovery", Recovery: &main.TestLoadRecoveryEvidence{Passed: false, RecoveryMS: &value}}}}}}}}
	if len(main.SummarizeTestResilienceRecovery(history)) != 0 {
		t.Fatal("unconfirmed recovery counted")
	}
	plan := resilienceTestPlan(2, 1e-15)
	receipt := main.RunTestResilience(context.Background(), plan, func(context.Context, main.TestRunPlan) main.TestRunReceipt {
		return main.TestRunReceipt{Status: "failed", StartedAt: time.Now()}
	})
	if receipt.Status != "failed" || receipt.Resilience.RequiredPasses != 1 {
		t.Fatal("tiny nonzero budget passed without successful runs")
	}
}
