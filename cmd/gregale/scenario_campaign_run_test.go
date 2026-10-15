package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func campaignRunnerTestPlan(t *testing.T, batches int) (testCampaignPolicyPlan, map[string]testScenario, testCampaignPolicy) {
	t.Helper()
	scenario := testScenario{Project: "checkout", Command: []string{"true"}}
	scenarios := map[string]testScenario{"checkout": scenario}
	policy := requiredCoverageTestPolicy(digestComparisonReceipt("seed", true, 50))
	plan := testCampaignPolicyPlan{SchemaVersion: 1, Execution: &testCampaignExecutionBinding{
		ManifestSHA256: testCampaignJSONHash(scenarios), PolicySHA256: testCampaignJSONHash(policy)},
		Costs: &testCampaignPlanCosts{}}
	unit, err := testCampaignCostUnit(scenario)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < batches; i++ {
		id := string(rune('a' + i))
		plan.Batches = append(plan.Batches, testCampaignCostBatch{ID: id, Scenario: "checkout", Profile: "warm", Side: "current", Repetitions: 1, Estimated: unit,
			Argv: []string{"do-not-execute-saved-argv"}})
		plan.Costs.Estimated.Runs += unit.Runs
		plan.Costs.Estimated.Deployments += unit.Deployments
		plan.Costs.Estimated.WorkloadMinutes += unit.WorkloadMinutes
	}
	return plan, scenarios, policy
}

func campaignRunnerTestLaunch(scenarios map[string]testScenario, cleanup bool) testCampaignBatchLauncher {
	return func(_ context.Context, b testCampaignCostBatch, dir string) error {
		r := sourceRevisionTestReceipt("run-"+b.ID, true, 50)
		r.Scenario, r.Profile, r.Engine = b.Scenario, b.Profile, "real-vm"
		r.ConfigurationSHA256 = newTestReplayManifest("", "", "", scenarios[b.Scenario]).ConfigurationSHA256
		if cleanup {
			r.CleanupError = "fixture cleanup failed"
			r.Status = "failed"
		}
		return writeTestCampaignJSON(filepath.Join(dir, "batch-"+b.ID+".json"), []testRunReceipt{r})
	}
}

func TestCampaignRunnerResumesWithoutRepeatingCompletedBatches(t *testing.T) {
	// adr: 984 — checkpoints bind receipts and never spend the same batch twice.
	plan, scenarios, policy := campaignRunnerTestPlan(t, 2)
	if err := validateTestCampaignRunnable(plan, scenarios, policy); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	checkpoint := testCampaignCheckpoint{Side: "current", Revision: strings.Repeat("a", 40), Batches: map[string]testCampaignCheckpointBatch{}}
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	launcher := campaignRunnerTestLaunch(scenarios, false)
	err := runTestCampaignBatches(ctx, plan, scenarios, dir, &checkpoint, 1, func(testCampaignCostBatch) error { return nil },
		func(ctx context.Context, b testCampaignCostBatch, dir string) error {
			calls++
			err := launcher(ctx, b, dir)
			cancel()
			return err
		})
	if err == nil || calls != 1 || checkpoint.Batches["a"].Status != "done" {
		t.Fatal("interruption did not checkpoint completed work")
	}
	err = runTestCampaignBatches(context.Background(), plan, scenarios, dir, &checkpoint, 1, func(testCampaignCostBatch) error { return nil },
		func(ctx context.Context, b testCampaignCostBatch, dir string) error {
			calls++
			return launcher(ctx, b, dir)
		})
	if err != nil || calls != 2 || len(checkpoint.Batches) != 2 {
		t.Fatalf("resume repeated or lost work: %v %+v", err, checkpoint)
	}
}

func TestCampaignRunnerStopsForCleanupAndUncertainWork(t *testing.T) {
	// adr: 984 — uncertain execution and cleanup are never automatically retried.
	plan, scenarios, _ := campaignRunnerTestPlan(t, 2)
	dir := t.TempDir()
	checkpoint := testCampaignCheckpoint{Side: "current", Revision: strings.Repeat("a", 40), Batches: map[string]testCampaignCheckpointBatch{}}
	calls := 0
	launch := campaignRunnerTestLaunch(scenarios, true)
	err := runTestCampaignBatches(context.Background(), plan, scenarios, dir, &checkpoint, 1, func(testCampaignCostBatch) error { return nil },
		func(ctx context.Context, b testCampaignCostBatch, dir string) error {
			calls++
			return launch(ctx, b, dir)
		})
	if err == nil || calls != 1 || !checkpoint.Halted {
		t.Fatal("cleanup failure did not halt scheduling")
	}
	checkpoint = testCampaignCheckpoint{Side: "current", Revision: strings.Repeat("a", 40), Batches: map[string]testCampaignCheckpointBatch{"a": {Status: "running"}}}
	err = runTestCampaignBatches(context.Background(), plan, scenarios, t.TempDir(), &checkpoint, 1, func(testCampaignCostBatch) error { return nil },
		func(context.Context, testCampaignCostBatch, string) error {
			t.Fatal("uncertain batch repeated")
			return nil
		})
	if err == nil {
		t.Fatal("uncertain checkpoint accepted")
	}
}

func TestCampaignRunnerBoundsConcurrencyAndSeparatesSides(t *testing.T) {
	// adr: 984 — only selected-side batches execute, within the explicit worker limit.
	plan, scenarios, _ := campaignRunnerTestPlan(t, 4)
	plan.Batches[3].Side = "baseline"
	checkpoint := testCampaignCheckpoint{Side: "current", Revision: strings.Repeat("a", 40), Batches: map[string]testCampaignCheckpointBatch{}}
	var active, maximum, calls atomic.Int32
	barrier := make(chan struct{})
	launch := campaignRunnerTestLaunch(scenarios, false)
	err := runTestCampaignBatches(context.Background(), plan, scenarios, t.TempDir(), &checkpoint, 2, func(testCampaignCostBatch) error { return nil },
		func(ctx context.Context, b testCampaignCostBatch, dir string) error {
			n := active.Add(1)
			for old := maximum.Load(); n > old && !maximum.CompareAndSwap(old, n); old = maximum.Load() {
			}
			if calls.Add(1) == 2 {
				close(barrier)
			}
			<-barrier
			defer active.Add(-1)
			return launch(ctx, b, dir)
		})
	if err != nil || maximum.Load() != 2 || calls.Load() != 3 || len(checkpoint.Batches) != 3 {
		t.Fatalf("worker or side bound violated: %v max=%d calls=%d", err, maximum.Load(), calls.Load())
	}
}

func TestCampaignRunnerRejectsDriftBudgetAndTamperedEvidence(t *testing.T) {
	// adr: 984 — saved estimates and receipt hashes cannot silently change on resume.
	plan, scenarios, policy := campaignRunnerTestPlan(t, 1)
	plan.Costs.Budget.Runs = 1
	plan.Batches[0].Repetitions = 2
	if err := validateTestCampaignRunnable(plan, scenarios, policy); err == nil {
		t.Fatal("changed batch estimate accepted")
	}
	plan, scenarios, policy = campaignRunnerTestPlan(t, 2)
	plan.Costs.Budget.Runs = 1
	if err := validateTestCampaignRunnable(plan, scenarios, policy); err == nil {
		t.Fatal("over-budget plan accepted")
	}
	plan, scenarios, policy = campaignRunnerTestPlan(t, 1)
	scenarios["checkout"] = testScenario{Project: "changed"}
	if err := validateTestCampaignRunnable(plan, scenarios, policy); err == nil {
		t.Fatal("manifest drift accepted")
	}
	plan, scenarios, _ = campaignRunnerTestPlan(t, 1)
	dir := t.TempDir()
	checkpoint := testCampaignCheckpoint{Side: "current", Revision: strings.Repeat("a", 40), Batches: map[string]testCampaignCheckpointBatch{}}
	guard := func(testCampaignCostBatch) error { return nil }
	if err := runTestCampaignBatches(context.Background(), plan, scenarios, dir, &checkpoint, 1, guard, campaignRunnerTestLaunch(scenarios, false)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "batch-a.json"), []byte("[]"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runTestCampaignBatches(context.Background(), plan, scenarios, dir, &checkpoint, 1, guard,
		func(context.Context, testCampaignCostBatch, string) error { return errors.New("must not execute") }); err == nil {
		t.Fatal("tampered evidence accepted")
	}
}

func TestCampaignRunnerRejectsWrongRevisionAndGuardsBeforeSpending(t *testing.T) {
	// adr: 984 — revision evidence and runtime guards precede further campaign spending.
	plan, scenarios, _ := campaignRunnerTestPlan(t, 1)
	dir := t.TempDir()
	launch := campaignRunnerTestLaunch(scenarios, false)
	if err := launch(context.Background(), plan.Batches[0], dir); err != nil {
		t.Fatal(err)
	}
	if _, _, err := validateTestCampaignBatchReport(filepath.Join(dir, "batch-a.json"), plan.Batches[0], scenarios["checkout"], strings.Repeat("c", 40)); err == nil {
		t.Fatal("another source revision accepted")
	}
	checkpoint := testCampaignCheckpoint{Side: "current", Revision: strings.Repeat("a", 40), Batches: map[string]testCampaignCheckpointBatch{}}
	err := runTestCampaignBatches(context.Background(), plan, scenarios, t.TempDir(), &checkpoint, 1,
		func(testCampaignCostBatch) error { return errors.New("manifest changed") },
		func(context.Context, testCampaignCostBatch, string) error {
			t.Fatal("guard failure spent a batch")
			return nil
		})
	if err == nil || len(checkpoint.Batches) != 0 {
		t.Fatal("guard did not prevent execution")
	}
}
