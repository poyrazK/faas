package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type campaignCleanupFake struct {
	app     string
	deleted bool
	deletes int
}

func (f *campaignCleanupFake) GetDevSession(_ context.Context, _, _ string) (api.DevSessionResponse, error) {
	var response api.DevSessionResponse
	response.App.ID = f.app
	return response, nil
}

type campaignCleanupIdentityFake struct{ campaignCleanupFake }

func (f *campaignCleanupIdentityFake) DestroyDevSession(_ context.Context, _ string, _ ...string) error {
	f.deletes++
	return errors.New("simulated deletion failure")
}

func TestCampaignCleanupRequiresExactLiveIdentity(t *testing.T) {
	// adr: 984 — cleanup cannot delete a replacement workload in a known namespace.
	scenario := testScenario{Project: "checkout"}
	leaf := testRunReceipt{RunID: strings.Repeat("a", 32), WorkloadAppIDs: map[string]string{"checkout": "recorded"}}
	fake := &campaignCleanupIdentityFake{campaignCleanupFake: campaignCleanupFake{app: "replacement"}}
	if _, err := testCampaignRetryCleanup(context.Background(), fake, scenario, []testRunReceipt{leaf}, true); err == nil || fake.deletes != 0 {
		t.Fatal("mismatched live identity permitted deletion")
	}
	fake.app = "recorded"
	actions, err := testCampaignRetryCleanup(context.Background(), fake, scenario, []testRunReceipt{leaf}, false)
	if err != nil || fake.deletes != 0 || len(actions) != 1 || actions[0].Status != "present" {
		t.Fatal("inspection mutated sessions or omitted evidence")
	}
	leaf.RunID = ""
	if _, err := testCampaignRetryCleanup(context.Background(), fake, scenario, []testRunReceipt{leaf}, true); err == nil || fake.deletes != 0 {
		t.Fatal("default workspace permitted cleanup")
	}
}

func TestCampaignCleanupPreflightsAllIdentities(t *testing.T) {
	// adr: 984 — incomplete service identity must stop the entire cleanup preflight.
	scenario := testScenario{Project: "checkout", Services: map[string]testService{"database": {}}}
	fake := &campaignCleanupIdentityFake{campaignCleanupFake: campaignCleanupFake{app: "recorded"}}
	leaf := testRunReceipt{RunID: strings.Repeat("a", 32), WorkloadAppIDs: map[string]string{"checkout": "recorded"}}
	if _, err := testCampaignRetryCleanup(context.Background(), fake, scenario, []testRunReceipt{leaf}, true); err == nil || fake.deletes != 0 {
		t.Fatal("incomplete workload identities allowed partial destructive cleanup")
	}
}

func TestCampaignCancellationDrainsAndDoesNotScheduleNextBatch(t *testing.T) {
	// adr: 984 — saved cancellation stops scheduling and preserves active batch receipts.
	plan, scenarios, _ := campaignRunnerTestPlan(t, 2)
	directory := t.TempDir()
	cp := testCampaignCheckpoint{SchemaVersion: 1, LineageVersion: 1, PlanSHA256: testCampaignJSONHash(plan),
		Side: "current", Revision: strings.Repeat("a", 40), Batches: map[string]testCampaignCheckpointBatch{}}
	launched := 0
	err := runTestCampaignBatches(context.Background(), plan, scenarios, directory, &cp, 1,
		func(testCampaignCostBatch) error { return nil },
		func(_ context.Context, b testCampaignCostBatch, _ string) error {
			launched++
			if err := writeTestCampaignJSON(filepath.Join(directory, testCampaignCancelName),
				testCampaignCancellation{SchemaVersion: 1, PlanSHA256: cp.PlanSHA256, RequestedAt: time.Now().UTC()}); err != nil {
				return err
			}
			receipt := campaignRecoveryTestReceipt(scenarios[b.Scenario], "completed")
			return writeTestCampaignJSON(filepath.Join(directory, "batch-"+b.ID+".json"), []testRunReceipt{receipt})
		})
	if err == nil || launched != 1 || !cp.Halted || cp.Batches[plan.Batches[0].ID].Status != "done" {
		t.Fatalf("cancellation lost completed evidence or scheduled more work: %+v, %v", cp, err)
	}
	if _, err := os.Stat(filepath.Join(directory, "checkpoint.json")); err != nil {
		t.Fatal(err)
	}
}

func TestCampaignCancellationBeforeLaunchAndMalformedRequest(t *testing.T) {
	// adr: 984 — malformed requests fail closed without launching work.
	plan, scenarios, _ := campaignRunnerTestPlan(t, 1)
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, testCampaignCancelName), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	cp := testCampaignCheckpoint{Side: "current", Batches: map[string]testCampaignCheckpointBatch{}}
	called := false
	err := runTestCampaignBatches(context.Background(), plan, scenarios, directory, &cp, 1,
		func(testCampaignCostBatch) error { return nil },
		func(context.Context, testCampaignCostBatch, string) error { called = true; return nil })
	if err == nil || called || !cp.Halted {
		t.Fatal("invalid cancellation did not stop execution")
	}
}
