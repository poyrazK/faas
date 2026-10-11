package state

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestAutomationEnforcedPublication(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		app, _ := seedWorkflowSchedule(t, store, "allow")
		author := store.(AutomationStore)
		checks := store.(AutomationPublishCheckStore)
		ctx := context.Background()
		draft := mutateForTest(t, author, app.ID, "checked", "save", 0, automationDraft("checked", "/v1", ""), false)
		var definition api.WorkflowSpec
		_ = json.Unmarshal(draft.Draft, &definition)
		step := definition.Steps[0].Name
		req := api.CheckAutomationPublicationRequest{ExpectedVersion: draft.Version, Scenarios: []api.AutomationPublishCheckScenario{{Name: "success", Simulation: api.SimulateAutomationRequest{Definition: definition, MockOutputs: map[string]json.RawMessage{step: json.RawMessage(`{"ok":true}`)}}, Expectations: []api.AutomationCheckExpectation{{Step: step, State: "mocked", Output: json.RawMessage(`{"ok":true}`)}}}}}
		p, err := checks.SetAutomationPublishPolicy(ctx, app.ID, api.SetAutomationPublishPolicyRequest{Mode: "scenarios"})
		if err != nil {
			t.Fatal(err)
		}
		publish := AutomationMutation{Action: "publish", ExpectedVersion: draft.Version, ActorAccountID: app.AccountID}
		if _, err := author.MutateAutomation(ctx, app.ID, "checked", publish); !errors.Is(err, ErrAutomationPublishCheckRequired) {
			t.Fatalf("plain bypass: %v", err)
		}
		bad := req
		bad.Scenarios = append([]api.AutomationPublishCheckScenario(nil), req.Scenarios...)
		bad.Scenarios[0].Expectations = []api.AutomationCheckExpectation{{Step: step, State: "skipped"}}
		if _, err := checks.CheckAutomationPublication(ctx, app.ID, "checked", app.AccountID, "", bad, api.PlanHobby); !errors.Is(err, ErrAutomationInvalid) {
			t.Fatalf("false assertion: %v", err)
		}
		if _, err := checks.SetAutomationPublishPolicy(ctx, app.ID, api.SetAutomationPublishPolicyRequest{Mode: "optional"}); !errors.Is(err, ErrAutomationVersionConflict) {
			t.Fatalf("policy CAS: %v", err)
		}
		receipt, err := checks.CheckAutomationPublication(ctx, app.ID, "checked", app.AccountID, "", req, api.PlanHobby)
		if err != nil {
			t.Fatal(err)
		}
		if receipt.Receipt == "" || receipt.Evidence.DefinitionHash == "" {
			t.Fatal("empty receipt")
		}
		publish.CheckReceipt = receipt.Receipt
		wrong := publish
		wrong.ActorAPIKeyID = "different-key"
		if _, err := author.MutateAutomation(ctx, app.ID, "checked", wrong); !errors.Is(err, ErrAutomationPublishCheckRequired) {
			t.Fatalf("identity bypass: %v", err)
		}
		// Policy changes invalidate all earlier receipts, including a change away and back.
		p, err = checks.SetAutomationPublishPolicy(ctx, app.ID, api.SetAutomationPublishPolicyRequest{Mode: "coverage", ExpectedVersion: p.Version})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := author.MutateAutomation(ctx, app.ID, "checked", publish); !errors.Is(err, ErrAutomationPublishCheckRequired) {
			t.Fatalf("policy stale receipt: %v", err)
		}
		receipt, err = checks.CheckAutomationPublication(ctx, app.ID, "checked", app.AccountID, "", req, api.PlanHobby)
		if err != nil {
			t.Fatal(err)
		}
		publish.CheckReceipt = receipt.Receipt
		// Another draft save invalidates a still-unexpired receipt.
		draft = mutateForTest(t, author, app.ID, "checked", "save", draft.Version, draft.Draft, false)
		publish.ExpectedVersion = draft.Version
		if _, err := author.MutateAutomation(ctx, app.ID, "checked", publish); !errors.Is(err, ErrAutomationPublishCheckRequired) {
			t.Fatalf("draft stale receipt: %v", err)
		}
		req.ExpectedVersion = draft.Version
		receipt, err = checks.CheckAutomationPublication(ctx, app.ID, "checked", app.AccountID, "", req, api.PlanHobby)
		if err != nil {
			t.Fatal(err)
		}
		publish.CheckReceipt = receipt.Receipt
		receipt.Evidence.Scenarios[0].Name = "mutated-response"
		published, err := author.MutateAutomation(ctx, app.ID, "checked", publish)
		if err != nil {
			t.Fatal(err)
		}
		revision, err := author.GetAutomationRevision(ctx, app.ID, "checked", published.PublishedVersion)
		if err != nil {
			t.Fatal(err)
		}
		var evidence api.AutomationCheckEvidence
		if json.Unmarshal(revision.CheckEvidence, &evidence) != nil || !evidence.ServerVerified || !evidence.CoverageRequired || evidence.Scenarios[0].Name != "success" {
			t.Fatalf("trusted evidence absent: %s", revision.CheckEvidence)
		}
		publish.ExpectedVersion = published.Version
		if _, err := author.MutateAutomation(ctx, app.ID, "checked", publish); !errors.Is(err, ErrAutomationPublishCheckRequired) {
			t.Fatalf("receipt replay: %v", err)
		}
		_, total, err := author.ListAutomationRevisions(ctx, app.ID, "checked", AutomationRevisionListOptions{Limit: 10})
		if err != nil || total != 1 {
			t.Fatalf("failed checks wrote history: %d %v", total, err)
		}
		// A coverage policy cannot be bypassed by require_coverage=false.
		guarded := definition
		guarded.Name = "guarded"
		guarded.Steps = append([]api.WorkflowStepSpec(nil), definition.Steps...)
		guarded.Steps[0].When = &api.WorkflowGuardSpec{Ref: "input.ok", Op: "exists", Value: json.RawMessage(`true`)}
		guardedRaw, _ := json.Marshal(guarded)
		guardedDraft := mutateForTest(t, author, app.ID, "guarded", "save", 0, guardedRaw, false)
		guardedReq := api.CheckAutomationPublicationRequest{ExpectedVersion: guardedDraft.Version, Scenarios: []api.AutomationPublishCheckScenario{{Name: "matched", Simulation: api.SimulateAutomationRequest{Definition: guarded, Input: json.RawMessage(`{"ok":true}`), MockOutputs: map[string]json.RawMessage{step: json.RawMessage(`null`)}}, Expectations: []api.AutomationCheckExpectation{{Step: step, State: "mocked"}}}}}
		if _, err := checks.CheckAutomationPublication(ctx, app.ID, "guarded", app.AccountID, "", guardedReq, api.PlanHobby); !errors.Is(err, ErrAutomationInvalid) {
			t.Fatalf("coverage bypass: %v", err)
		}
		guardedReq.Exclusions = []api.AutomationCheckExclusion{{Step: step, Code: "guard_skip_missing", Reason: "External contract guarantees ok"}}
		if _, err := checks.CheckAutomationPublication(ctx, app.ID, "guarded", app.AccountID, "", guardedReq, api.PlanHobby); err != nil {
			t.Fatal("reasoned exclusion", err)
		}

	})
}

func TestAutomationReceiptExpiry(t *testing.T) {
	def := api.WorkflowSpec{Name: "check", Steps: []api.WorkflowStepSpec{}}
	raw, _ := json.Marshal(def)
	evidence := api.AutomationCheckEvidence{DefinitionHash: automationReceiptHash(string(raw)), CheckedVersion: 1, CheckedAt: time.Now(), Scenarios: []api.AutomationCheckScenario{{Name: "ok", Passed: true, DefinitionValid: true, Complete: true}}, Exclusions: []api.AutomationCheckExclusion{}, CoveragePassed: true}
	receipt := automationPublishReceipt{AppID: "app", Name: "check", AccountID: "actor", TokenHash: automationReceiptHash("token"), ExpiresAt: time.Now().Add(-time.Second), Evidence: evidence}
	mutation := AutomationMutation{Action: "publish", ActorAccountID: "actor", CheckReceipt: "token"}
	if err := validatePublicationReceipt(api.AutomationPublishPolicy{Mode: "scenarios"}, receipt, "app", "check", &mutation, &Automation{Version: 1, Draft: raw}); !errors.Is(err, ErrAutomationPublishCheckRequired) {
		t.Fatalf("expired receipt: %v", err)
	}
}
