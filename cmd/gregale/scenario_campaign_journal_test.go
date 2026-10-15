package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func campaignJournalTestFixture(t *testing.T) (string, testCampaignPolicyPlan, testCampaignCheckpoint, testCampaignCostBatch, testScenario, string) {
	t.Helper()
	plan, scenarios, _ := campaignRunnerTestPlan(t, 1)
	batch := plan.Batches[0]
	cp := testCampaignCheckpoint{SchemaVersion: 1, LineageVersion: 1, PlanSHA256: testCampaignJSONHash(plan),
		Side: batch.Side, Revision: strings.Repeat("a", 40), Batches: map[string]testCampaignCheckpointBatch{batch.ID: {Status: "running"}}}
	directory := t.TempDir()
	if err := writeTestCampaignJSON(filepath.Join(directory, "checkpoint.json"), cp); err != nil {
		t.Fatal(err)
	}
	path, err := prepareTestCampaignJournal(directory, batch)
	if err != nil {
		t.Fatal(err)
	}
	return directory, plan, cp, batch, scenarios[batch.Scenario], path
}

func TestCampaignJournalRecoversIdentityWithoutFinalReport(t *testing.T) {
	// adr: 984 — provisioning identity survives without granting assertion evidence.
	directory, plan, cp, batch, scenario, path := campaignJournalTestFixture(t)
	runID := strings.Repeat("b", 32)
	writer, err := newTestCampaignRunJournal(path, batch.Scenario, batch.Profile, runID, scenario)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.update(scenario.Project, "", "creating"); err != nil {
		t.Fatal(err)
	}
	if err := writer.update(scenario.Project, "app-returned", "provisioned"); err != nil {
		t.Fatal(err)
	}
	targets, hash, err := readTestCampaignJournalTargets(directory, testCampaignJSONHash(plan), cp, batch, scenario)
	if err != nil || hash == "" || len(targets) != 1 {
		t.Fatalf("identity lost: %v", err)
	}
	target := targets[scenario.Project+"/"+runID]
	if target.AppID != "app-returned" {
		t.Fatal("returned identity was not persisted")
	}
	result, recovered := inspectTestCampaignRecoveryReport(context.Background(), filepath.Join(directory, "batch-"+batch.ID+".json"),
		batch, scenario, cp, cp.Batches[batch.ID], nil)
	if result.Status != "blocked" || result.JournalSHA256 != hash || len(recovered) != 0 || result.AdoptedRuns != 0 {
		t.Fatal("journal was promoted to execution evidence")
	}
	if err := recordTestCampaignJournalCleanup(directory, batch.ID, []testCampaignCleanupAction{
		{Project: scenario.Project, RunID: runID, AppID: "app-returned", Status: "deleted"},
	}); err != nil {
		t.Fatal(err)
	}
	var persisted testCampaignRunJournal
	if err := readTestCampaignJSON(filepath.Join(path, runID+".json"), &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.Workloads[scenario.Project].State != "deleted" {
		t.Fatal("cleanup progress was lost")
	}
}

func TestCampaignJournalUnknownProvisioningOutcomeFailsClosed(t *testing.T) {
	// adr: 984 — an intent without a returned app ID never authorizes guessed deletion.
	directory, plan, cp, batch, scenario, path := campaignJournalTestFixture(t)
	writer, err := newTestCampaignRunJournal(path, batch.Scenario, batch.Profile, strings.Repeat("b", 32), scenario)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.update(scenario.Project, "", "creating"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readTestCampaignJournalTargets(directory, testCampaignJSONHash(plan), cp, batch, scenario); err == nil {
		t.Fatal("unknown provisioning outcome authorized recovery")
	}
	if err := writer.update(scenario.Project, "known", "provisioned"); err != nil {
		t.Fatal(err)
	}
	cp.Revision = strings.Repeat("c", 40)
	if _, _, err := readTestCampaignJournalTargets(directory, testCampaignJSONHash(plan), cp, batch, scenario); err == nil {
		t.Fatal("journal accepted another revision")
	}
}

func TestCampaignJournalRejectsDuplicateRunAndMalformedArtifacts(t *testing.T) {
	// adr: 984 — journals never overwrite prior run identities or accept partial artifacts.
	directory, plan, cp, batch, scenario, path := campaignJournalTestFixture(t)
	runID := strings.Repeat("b", 32)
	if _, err := newTestCampaignRunJournal(path, batch.Scenario, batch.Profile, runID, scenario); err != nil {
		t.Fatal(err)
	}
	if _, err := newTestCampaignRunJournal(path, batch.Scenario, batch.Profile, runID, scenario); err == nil {
		t.Fatal("duplicate identity overwrote journal")
	}
	if err := os.WriteFile(filepath.Join(path, "truncated.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readTestCampaignJournalTargets(directory, testCampaignJSONHash(plan), cp, batch, scenario); err == nil {
		t.Fatal("partial journal accepted")
	}
}
