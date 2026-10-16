package main

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type campaignFaultAPI struct {
	consumer         api.APIConsumerResponse
	members          map[string]string
	revokes, deletes int
	fail             bool
	expiry           *time.Time
	expiryState      string
}

func (f *campaignFaultAPI) GetAPIConsumer(context.Context, string, string) (api.APIConsumerResponse, error) {
	return f.consumer, nil
}
func (f *campaignFaultAPI) RevokeAPIConsumer(context.Context, string, string) (api.APIConsumerResponse, error) {
	f.revokes++
	f.consumer.Status = "revoked"
	return f.consumer, nil
}
func (f *campaignFaultAPI) GetScenarioTestRegistration(context.Context, string) (api.ScenarioTestRegistration, error) {
	if f.fail {
		return api.ScenarioTestRegistration{}, errors.New("lookup failed")
	}
	if f.members == nil {
		return api.ScenarioTestRegistration{}, errors.New("unsupported registration API")
	}
	return api.ScenarioTestRegistration{SchemaVersion: 1, Members: f.members, ExpiresAt: f.expiry, ExpiryState: f.expiryState}, nil
}
func (f *campaignFaultAPI) DeleteScenarioTestRegistration(_ context.Context, _ string, digest string) error {
	if digest != api.ScenarioTestRegistrationSHA256(f.members) {
		return errors.New("precondition")
	}
	f.deletes++
	f.fail = true
	return nil
}
func TestCampaignFaultCleanupIdentityAndRevocation(t *testing.T) {
	// adr: 984 — only exact consumer identities may be revoked, without replaying execution.
	scenario := testScenario{Project: "checkout", Consumers: []testConsumer{{Name: "customer"}}}
	j := testCampaignRunJournal{RunID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Workloads: map[string]testCampaignJournalWorkload{"checkout": {Project: "checkout", AppID: "owned"}}, Faults: &testCampaignFaultJournal{Registration: "registered", Consumers: map[string]testCampaignFaultConsumer{"customer": {ID: "11111111-1111-4111-8111-111111111111", AppID: "owned", AppSlug: "preview", ExternalRef: "gregale-test-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-customer", ExpiresAt: time.Now().Add(time.Hour)}}}}
	body, err := json.Marshal(j)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), `"key"`) || checkTestCampaignBundleSensitive(body, nil) != nil {
		t.Fatal("fault inventory is not portable")
	}
	if err := validateTestCampaignFaultJournal(j, scenario); err != nil {
		t.Fatal(err)
	}
	f := &campaignFaultAPI{consumer: api.APIConsumerResponse{ID: "11111111-1111-4111-8111-111111111111", AppID: "owned", ExternalRef: j.Faults.Consumers["customer"].ExternalRef, Status: "active"}, members: map[string]string{"checkout": "owned"}}
	ctx := context.Background()
	if _, err := inspectTestCampaignFaults(ctx, f, []testCampaignRunJournal{j}, false, false); err != nil || f.revokes != 0 || f.deletes != 0 {
		t.Fatal("inspection mutated resources", err)
	}
	f.consumer.AppID = "replacement"
	if _, err := inspectTestCampaignFaults(ctx, f, []testCampaignRunJournal{j}, true, false); err == nil || f.revokes != 0 {
		t.Fatal("replacement consumer revoked")
	}
	f.consumer.AppID = "owned"
	for i := 0; i < 2; i++ {
		if _, err := inspectTestCampaignFaults(ctx, f, []testCampaignRunJournal{j}, true, false); err != nil {
			t.Fatal(err)
		}
	}
	if f.revokes != 1 || f.deletes != 0 {
		t.Fatal("consumer revocation was not idempotent or namespace deleted before sessions")
	}
	f.members["checkout"] = "replacement"
	if _, err := inspectTestCampaignFaults(ctx, f, []testCampaignRunJournal{j}, true, true); err == nil || f.deletes != 0 {
		t.Fatal("replacement registration deleted")
	}
	f.members["checkout"] = "owned"
	if _, err := inspectTestCampaignFaults(ctx, f, []testCampaignRunJournal{j}, true, true); err == nil {
		t.Fatal("failed absence lookup certified deletion")
	}
	c := j.Faults.Consumers["customer"]
	c.ID = ""
	j.Faults.Consumers["customer"] = c
	if err := validateTestCampaignFaultJournal(j, scenario); err == nil {
		t.Fatal("uncertain creation gained cleanup authority")
	}
}

func (f *campaignFaultAPI) GetDevSession(context.Context, string, string) (api.DevSessionResponse, error) {
	return api.DevSessionResponse{}, errors.New("session read outside fault check")
}

func TestCampaignFaultCompletionDoesNotForgeExecution(t *testing.T) {
	// adr: 984 — registration absence cannot replace a missing test result or ancestor evidence.
	plan, scenarios, _ := campaignRunnerTestPlan(t, 1)
	directory := t.TempDir()
	batch := plan.Batches[0]
	cp := testCampaignCheckpoint{SchemaVersion: 1, LineageVersion: 1, PlanSHA256: testCampaignJSONHash(plan), Side: batch.Side, Revision: strings.Repeat("a", 40), Batches: map[string]testCampaignCheckpointBatch{batch.ID: {Status: "running"}}}
	if err := writeTestCampaignJSON(filepath.Join(directory, "checkpoint.json"), cp); err != nil {
		t.Fatal(err)
	}
	journal, err := prepareTestCampaignJournal(directory, batch)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := newTestCampaignRunJournal(journal, batch.Scenario, batch.Profile, strings.Repeat("a", 32), scenarios[batch.Scenario])
	if err != nil {
		t.Fatal(err)
	}
	project := scenarios[batch.Scenario].Project
	if err := writer.update(project, "owned", "provisioned"); err != nil {
		t.Fatal(err)
	}
	if err := writer.registration("registered"); err != nil {
		t.Fatal(err)
	}
	cp.Halted = true
	cp.Batches[batch.ID] = testCampaignCheckpointBatch{Status: "uncertain"}
	if err := writeTestCampaignJSON(filepath.Join(directory, "checkpoint.json"), cp); err != nil {
		t.Fatal(err)
	}
	past := time.Now().UTC().Add(-time.Hour)
	client := &campaignFaultAPI{members: map[string]string{project: "owned"}, expiry: &past, expiryState: "expired"}
	report := testCampaignRecoveryCompletion{RecoveryComplete: true, ResourceEvidenceComplete: true}
	applyTestCampaignFaultCompletion(context.Background(), &report, client, plan, cp, scenarios, directory, false)
	if !report.FaultResourcesVerified || report.FaultResourcesAbsent || report.RecoveryComplete {
		t.Fatal("live namespace certified absent")
	}
	if len(report.FaultResources) != 1 || report.FaultResources[0].RegistrationExpiryState != "expired" || report.FaultResources[0].RegistrationExpiresAt == nil {
		t.Fatal("completion lost expired registration status")
	}
	client.expiry, client.expiryState = nil, ""
	report = testCampaignRecoveryCompletion{ResourceEvidenceComplete: true}
	applyTestCampaignFaultCompletion(context.Background(), &report, client, plan, cp, scenarios, directory, false)
	if !report.FaultResourcesVerified || report.FaultResourcesAbsent || len(report.FaultResources) != 1 || report.FaultResources[0].RegistrationExpiryState != "unknown" {
		t.Fatal("older API expiry capability was guessed")
	}
	client.expiryState = "expired"
	report = testCampaignRecoveryCompletion{ResourceEvidenceComplete: true}
	applyTestCampaignFaultCompletion(context.Background(), &report, client, plan, cp, scenarios, directory, false)
	if report.FaultResourcesVerified || report.FaultResourcesAbsent {
		t.Fatal("malformed expiry metadata was accepted")
	}
	client.members = map[string]string{}
	client.expiry, client.expiryState = nil, "absent"
	report = testCampaignRecoveryCompletion{ResourceEvidenceComplete: true}
	applyTestCampaignFaultCompletion(context.Background(), &report, client, plan, cp, scenarios, directory, false)
	if !report.FaultResourcesAbsent || report.RecoveryComplete || report.TestExecutionComplete {
		t.Fatal("resource absence forged test completion")
	}
	applyTestCampaignFaultCompletion(context.Background(), &report, client, plan, cp, scenarios, directory, true)
	if report.FaultResourcesAbsent {
		t.Fatal("current work waived ancestor inventory")
	}
}
