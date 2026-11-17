package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestStoredWorkflowEnforcedPublication(t *testing.T) {
	state.WorkflowScheduleStores(t, func(t *testing.T, store state.Store) {
		app, _ := state.SeedWorkflowSchedule(t, store, "allow")
		author := store.(state.StoredWorkflowStore)
		checks := store.(state.StoredWorkflowPublishCheckStore)
		ctx := context.Background()
		draft := state.MutateForTest(t, author, app.ID, "checked", "save", 0, state.StoredWorkflowDraft("checked", "/v1", ""), false)
		var definition api.WorkflowSpec
		_ = json.Unmarshal(draft.Draft, &definition)
		step := definition.Steps[0].Name
		req := api.CheckStoredWorkflowPublicationRequest{ExpectedVersion: draft.Version, Scenarios: []api.StoredWorkflowPublishCheckScenario{{Name: "success", Simulation: api.SimulateStoredWorkflowRequest{Definition: definition, MockOutputs: map[string]json.RawMessage{step: json.RawMessage(`{"ok":true}`)}}, Expectations: []api.StoredWorkflowCheckExpectation{{Step: step, State: "mocked", Output: json.RawMessage(`{"ok":true}`)}}}}}
		p, err := checks.SetStoredWorkflowPublishPolicy(ctx, app.ID, api.SetStoredWorkflowPublishPolicyRequest{Mode: "scenarios"})
		if err != nil {
			t.Fatal(err)
		}
		publish := state.StoredWorkflowMutation{Action: "publish", ExpectedVersion: draft.Version, ActorAccountID: app.AccountID}
		if _, err := author.MutateStoredWorkflow(ctx, app.ID, "checked", publish); !errors.Is(err, state.ErrStoredWorkflowPublishCheckRequired) {
			t.Fatalf("plain bypass: %v", err)
		}
		bad := req
		bad.Scenarios = append([]api.StoredWorkflowPublishCheckScenario(nil), req.Scenarios...)
		bad.Scenarios[0].Expectations = []api.StoredWorkflowCheckExpectation{{Step: step, State: "skipped"}}
		if _, err := checks.CheckStoredWorkflowPublication(ctx, app.ID, "checked", app.AccountID, "", bad, api.PlanHobby); !errors.Is(err, state.ErrStoredWorkflowInvalid) {
			t.Fatalf("false assertion: %v", err)
		}
		if _, err := checks.SetStoredWorkflowPublishPolicy(ctx, app.ID, api.SetStoredWorkflowPublishPolicyRequest{Mode: "optional"}); !errors.Is(err, state.ErrStoredWorkflowVersionConflict) {
			t.Fatalf("policy CAS: %v", err)
		}
		receipt, err := checks.CheckStoredWorkflowPublication(ctx, app.ID, "checked", app.AccountID, "", req, api.PlanHobby)
		if err != nil {
			t.Fatal(err)
		}
		if receipt.Receipt == "" || receipt.Evidence.DefinitionHash == "" {
			t.Fatal("empty receipt")
		}
		publish.CheckReceipt = receipt.Receipt
		wrong := publish
		wrong.ActorAPIKeyID = "different-key"
		if _, err := author.MutateStoredWorkflow(ctx, app.ID, "checked", wrong); !errors.Is(err, state.ErrStoredWorkflowPublishCheckRequired) {
			t.Fatalf("identity bypass: %v", err)
		}
		// Policy changes invalidate all earlier receipts, including a change away and back.
		p, err = checks.SetStoredWorkflowPublishPolicy(ctx, app.ID, api.SetStoredWorkflowPublishPolicyRequest{Mode: "coverage", ExpectedVersion: p.Version})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := author.MutateStoredWorkflow(ctx, app.ID, "checked", publish); !errors.Is(err, state.ErrStoredWorkflowPublishCheckRequired) {
			t.Fatalf("policy stale receipt: %v", err)
		}
		receipt, err = checks.CheckStoredWorkflowPublication(ctx, app.ID, "checked", app.AccountID, "", req, api.PlanHobby)
		if err != nil {
			t.Fatal(err)
		}
		publish.CheckReceipt = receipt.Receipt
		// Another draft save invalidates a still-unexpired receipt.
		draft = state.MutateForTest(t, author, app.ID, "checked", "save", draft.Version, draft.Draft, false)
		publish.ExpectedVersion = draft.Version
		if _, err := author.MutateStoredWorkflow(ctx, app.ID, "checked", publish); !errors.Is(err, state.ErrStoredWorkflowPublishCheckRequired) {
			t.Fatalf("draft stale receipt: %v", err)
		}
		req.ExpectedVersion = draft.Version
		receipt, err = checks.CheckStoredWorkflowPublication(ctx, app.ID, "checked", app.AccountID, "", req, api.PlanHobby)
		if err != nil {
			t.Fatal(err)
		}
		publish.CheckReceipt = receipt.Receipt
		receipt.Evidence.Scenarios[0].Name = "mutated-response"
		published, err := author.MutateStoredWorkflow(ctx, app.ID, "checked", publish)
		if err != nil {
			t.Fatal(err)
		}
		revision, err := author.GetStoredWorkflowRevision(ctx, app.ID, "checked", published.PublishedVersion)
		if err != nil {
			t.Fatal(err)
		}
		var evidence api.StoredWorkflowCheckEvidence
		if json.Unmarshal(revision.CheckEvidence, &evidence) != nil || !evidence.ServerVerified || !evidence.CoverageRequired || evidence.Scenarios[0].Name != "success" {
			t.Fatalf("trusted evidence absent: %s", revision.CheckEvidence)
		}
		publish.ExpectedVersion = published.Version
		if _, err := author.MutateStoredWorkflow(ctx, app.ID, "checked", publish); !errors.Is(err, state.ErrStoredWorkflowPublishCheckRequired) {
			t.Fatalf("receipt replay: %v", err)
		}
		_, total, err := author.ListStoredWorkflowRevisions(ctx, app.ID, "checked", state.StoredWorkflowRevisionListOptions{Limit: 10})
		if err != nil || total != 1 {
			t.Fatalf("failed checks wrote history: %d %v", total, err)
		}
		// A coverage policy cannot be bypassed by require_coverage=false.
		guarded := definition
		guarded.Name = "guarded"
		guarded.Steps = append([]api.WorkflowStepSpec(nil), definition.Steps...)
		guarded.Steps[0].When = &api.WorkflowGuardSpec{Ref: "input.ok", Op: "exists", Value: json.RawMessage(`true`)}
		guardedRaw, _ := json.Marshal(guarded)
		guardedDraft := state.MutateForTest(t, author, app.ID, "guarded", "save", 0, guardedRaw, false)
		guardedReq := api.CheckStoredWorkflowPublicationRequest{ExpectedVersion: guardedDraft.Version, Scenarios: []api.StoredWorkflowPublishCheckScenario{{Name: "matched", Simulation: api.SimulateStoredWorkflowRequest{Definition: guarded, Input: json.RawMessage(`{"ok":true}`), MockOutputs: map[string]json.RawMessage{step: json.RawMessage(`null`)}}, Expectations: []api.StoredWorkflowCheckExpectation{{Step: step, State: "mocked"}}}}}
		if _, err := checks.CheckStoredWorkflowPublication(ctx, app.ID, "guarded", app.AccountID, "", guardedReq, api.PlanHobby); !errors.Is(err, state.ErrStoredWorkflowInvalid) {
			t.Fatalf("coverage bypass: %v", err)
		}
		guardedReq.Exclusions = []api.StoredWorkflowCheckExclusion{{Step: step, Code: "guard_skip_missing", Reason: "External contract guarantees ok"}}
		if _, err := checks.CheckStoredWorkflowPublication(ctx, app.ID, "guarded", app.AccountID, "", guardedReq, api.PlanHobby); err != nil {
			t.Fatal("reasoned exclusion", err)
		}

	})
}

func TestStoredWorkflowReceiptExpiry(t *testing.T) {
	def := api.WorkflowSpec{Name: "check", Steps: []api.WorkflowStepSpec{}}
	raw, _ := json.Marshal(def)
	evidence := api.StoredWorkflowCheckEvidence{DefinitionHash: state.StoredWorkflowReceiptHash(string(raw)), CheckedVersion: 1, CheckedAt: time.Now(), Scenarios: []api.StoredWorkflowCheckScenario{{Name: "ok", Passed: true, DefinitionValid: true, Complete: true}}, Exclusions: []api.StoredWorkflowCheckExclusion{}, CoveragePassed: true}
	receipt := state.StoredWorkflowPublishReceipt{AppID: "app", Name: "check", AccountID: "actor", TokenHash: state.StoredWorkflowReceiptHash("token"), ExpiresAt: time.Now().Add(-time.Second), Evidence: evidence}
	mutation := state.StoredWorkflowMutation{Action: "publish", ActorAccountID: "actor", CheckReceipt: "token"}
	if err := state.ValidatePublicationReceipt(api.StoredWorkflowPublishPolicy{Mode: "scenarios"}, receipt, "app", "check", &mutation, &state.StoredWorkflow{Version: 1, Draft: raw}); !errors.Is(err, state.ErrStoredWorkflowPublishCheckRequired) {
		t.Fatalf("expired receipt: %v", err)
	}
}
