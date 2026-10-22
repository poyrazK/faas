// adr: 1289
package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func automationSuiteFixture() api.AutomationTestSuite {
	inputs := api.AutomationTestInputs{Input: json.RawMessage(`{"private":"PRIVATE-SAMPLE"}`), MockOutputs: map[string]json.RawMessage{"main": json.RawMessage(`{"private":"PRIVATE-OUTPUT","count":9007199254740993}`)}}
	return api.AutomationTestSuite{Sample: inputs, Cases: []api.AutomationTestCase{
		{Name: "correct output", Simulation: inputs, Expectations: []api.AutomationCheckExpectation{{Step: "main", State: "mocked", Output: inputs.MockOutputs["main"]}}},
		{Name: "wrong output", Simulation: inputs, Expectations: []api.AutomationCheckExpectation{{Step: "main", Output: json.RawMessage(`{"private":"PRIVATE-EXPECTATION"}`)}}},
		{Name: "last case still runs", Simulation: inputs, Expectations: []api.AutomationCheckExpectation{{Step: "main", State: "mocked"}}},
	}}
}
func saveSuiteForTest(t *testing.T, store state.AutomationStore, appID string, draft state.Automation, suite *api.AutomationTestSuite) state.Automation {
	t.Helper()
	var raw json.RawMessage
	if suite != nil {
		raw, _ = json.Marshal(suite)
	}
	result, err := store.MutateAutomation(t.Context(), appID, draft.Name, state.AutomationMutation{Action: "tests", ExpectedVersion: draft.Version, TestSuite: raw})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func TestAutomationSavedTestsPersistenceAndFreshness(t *testing.T) {
	state.WorkflowScheduleStores(t, func(t *testing.T, store state.Store) {
		app, _ := state.SeedWorkflowSchedule(t, store, "allow")
		author := store.(state.AutomationStore)
		runner := store.(state.AutomationTestStore)
		raw := state.AutomationDraft("saved-cases", "/handler", "")
		draft := state.MutateForTest(t, author, app.ID, "saved-cases", "save", 0, raw, false)
		suite := automationSuiteFixture()
		saved := saveSuiteForTest(t, author, app.ID, draft, &suite)
		if saved.Version <= draft.Version || len(saved.Published) > 0 {
			t.Fatal("suite save did not version draft intent")
		}
		if _, err := runner.RunAutomationTests(t.Context(), app.ID, draft.Name, draft.Version, api.PlanHobby); !errors.Is(err, state.ErrAutomationVersionConflict) {
			t.Fatalf("stale run: %v", err)
		}
		report, err := runner.RunAutomationTests(t.Context(), app.ID, draft.Name, saved.Version, api.PlanHobby)
		if err != nil {
			t.Fatal(err)
		}
		if report.Passed || len(report.Cases) != 3 || !report.Cases[0].Passed || report.Cases[1].Passed || !report.Cases[2].Passed || report.Cases[1].Steps[0].Passed || report.Cases[1].Steps[0].State != "mocked" {
			t.Fatalf("case/step results: %+v", report)
		}
		if !reflect.DeepEqual(report.Cases[1].Steps[0].Mismatches, []string{"output"}) {
			t.Fatal("report did not identify the failing field")
		}
		rows, _ := author.ListAutomations(t.Context(), app.ID)
		current := rows[0]
		if current.Version != saved.Version || !current.UpdatedAt.Truncate(time.Microsecond).Equal(saved.UpdatedAt.Truncate(time.Microsecond)) {
			t.Fatal("observation changed intent version/time")
		}
		for _, secret := range []string{"PRIVATE-SAMPLE", "PRIVATE-OUTPUT", "PRIVATE-EXPECTATION"} {
			if strings.Contains(string(current.TestReport), secret) {
				t.Fatal("report retained payload")
			}
		}
		var restored api.AutomationTestSuite
		if json.Unmarshal(current.TestSuite, &restored) != nil || len(restored.Cases) != 3 || !strings.Contains(string(restored.Cases[0].Simulation.MockOutputs["main"]), "9007199254740993") {
			t.Fatal("suite did not survive storage")
		}
		var def api.WorkflowSpec
		_ = json.Unmarshal(current.Draft, &def)
		hash, _ := api.AutomationTestInputsHash(def, restored)
		if hash != report.InputsHash {
			t.Fatal("JSON storage changed result freshness")
		}
		published := state.MutateForTest(t, author, app.ID, draft.Name, "publish", current.Version, nil, false)
		if string(published.TestReport) != string(current.TestReport) || string(published.TestSuite) != string(current.TestSuite) {
			t.Fatal("publication dropped tests")
		}
		suite.Cases[1].Expectations = suite.Cases[0].Expectations
		edited := saveSuiteForTest(t, author, app.ID, published, &suite)
		hash, _ = api.AutomationTestInputsHash(def, suite)
		if hash == report.InputsHash || len(edited.TestReport) == 0 {
			t.Fatal("editing should retain outdated report")
		}
		passed, err := runner.RunAutomationTests(t.Context(), app.ID, draft.Name, edited.Version, api.PlanHobby)
		if err != nil || !passed.Passed {
			t.Fatalf("rerun: %+v %v", passed, err)
		}
		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := runner.RunAutomationTests(cancelled, app.ID, draft.Name, edited.Version, api.PlanHobby); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation: %v", err)
		}
		if err := store.UpdateAccountPlan(t.Context(), app.AccountID, api.PlanFree); err != nil {
			t.Fatal(err)
		}
		if _, err := author.MutateAutomation(t.Context(), app.ID, draft.Name, state.AutomationMutation{Action: "tests", ExpectedVersion: edited.Version, TestSuite: edited.TestSuite}); err == nil {
			t.Fatal("suite write ignored plan downgrade")
		}
		cleared := saveSuiteForTest(t, author, app.ID, edited, nil)
		if len(cleared.TestSuite) != 0 || len(cleared.TestReport) != 0 {
			t.Fatal("clear retained test data")
		}
		if _, err := runner.RunAutomationTests(t.Context(), app.ID, draft.Name, cleared.Version, api.PlanHobby); !errors.Is(err, state.ErrAutomationInvalid) {
			t.Fatalf("empty suite run: %v", err)
		}
		runs, _, _ := store.ListWorkflowRuns(t.Context(), app.ID, state.ListWorkflowRunsOpts{})
		if len(runs) != 0 {
			t.Fatal("saved simulations admitted real runs")
		}
	})
}
func TestAutomationSavedTestsInvalidatePublicationReceipts(t *testing.T) {
	state.WorkflowScheduleStores(t, func(t *testing.T, store state.Store) {
		app, _ := state.SeedWorkflowSchedule(t, store, "allow")
		author := store.(state.AutomationStore)
		draft := state.MutateForTest(t, author, app.ID, "checks", "save", 0, state.AutomationDraft("checks", "/handler", ""), false)
		suite := automationSuiteFixture()
		suite.Cases = suite.Cases[:1]
		draft = saveSuiteForTest(t, author, app.ID, draft, &suite)
		var def api.WorkflowSpec
		_ = json.Unmarshal(draft.Draft, &def)
		checker := store.(state.AutomationPublishCheckStore)
		receipt, err := checker.CheckAutomationPublication(t.Context(), app.ID, draft.Name, app.AccountID, "", api.CheckAutomationPublicationRequest{ExpectedVersion: draft.Version, Scenarios: []api.AutomationPublishCheckScenario{{Name: suite.Cases[0].Name, Simulation: suite.Cases[0].Simulation.Simulation(def), Expectations: suite.Cases[0].Expectations}}}, api.PlanHobby)
		if err != nil {
			t.Fatal(err)
		}
		draft = saveSuiteForTest(t, author, app.ID, draft, &suite)
		if _, err := author.MutateAutomation(t.Context(), app.ID, draft.Name, state.AutomationMutation{Action: "publish", ExpectedVersion: draft.Version, CheckReceipt: receipt.Receipt, ActorAccountID: app.AccountID}); !errors.Is(err, state.ErrAutomationPublishCheckRequired) {
			t.Fatalf("stale receipt accepted: %v", err)
		}
	})
}
func TestAutomationSavedTestsValidationAndInvalidAssertions(t *testing.T) {
	suite := automationSuiteFixture()
	for _, raw := range []string{`null`, `{} {}`, `{"cases":[{"name":"a","simulation":{"definition":{}}}]}`, `{"cases":[{"name":"a"},{"name":"a"}]}`, `{"cases":[{"name":" "}]}`} {
		if state.ValidateAutomationTestSuite(json.RawMessage(raw)) == nil {
			t.Fatalf("accepted invalid suite: %s", raw)
		}
	}
	var def api.WorkflowSpec
	_ = json.Unmarshal(state.AutomationDraft("tests", "/handler", ""), &def)
	suite.Cases[0].Expectations[0].Step = "removed"
	report, err := state.SimulateAutomationTestSuite(t.Context(), def, suite, 1, api.PlanHobby)
	if err != nil || report.Cases[0].Issue != "invalid_expectations" || !report.Cases[2].Passed {
		t.Fatalf("invalid assertion stopped suite: %+v %v", report, err)
	}
}

func TestAutomationSavedTestsPreserveScheduleCursor(t *testing.T) {
	state.WorkflowScheduleStores(t, func(t *testing.T, store state.Store) {
		app, dep := state.SeedWorkflowSchedule(t, store, "allow")
		author := store.(state.AutomationStore)
		draft := state.MutateForTest(t, author, app.ID, "nightly", "save", 0, state.AutomationDraft("nightly", "/report", `{"type":"schedule","schedule":"* * * * *"}`), false)
		draft = state.MutateForTest(t, author, app.ID, "nightly", "publish", draft.Version, nil, true)
		schedules := store.(state.WorkflowScheduleStore)
		at := time.Date(2026, 10, 15, 8, 0, 30, 0, time.UTC)
		if _, _, err := schedules.AdmitScheduledWorkflow(t.Context(), app.ID, dep.ID, draft.Name, at); err != nil {
			t.Fatal(err)
		}
		before, err := schedules.ListWorkflowScheduleCursors(t.Context(), app.ID)
		if err != nil || len(before) != 1 {
			t.Fatal("cursor", err)
		}
		suite := automationSuiteFixture()
		suite.Cases = suite.Cases[:1]
		draft = saveSuiteForTest(t, author, app.ID, draft, &suite)
		if _, err := store.(state.AutomationTestStore).RunAutomationTests(t.Context(), app.ID, draft.Name, draft.Version, api.PlanHobby); err != nil {
			t.Fatal(err)
		}
		after, err := schedules.ListWorkflowScheduleCursors(t.Context(), app.ID)
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatal("test edits changed schedule admission", err)
		}
	})
}

func TestAutomationSavedTestsLoopItemSummary(t *testing.T) {
	def := api.WorkflowSpec{Name: "loop-tests", Steps: []api.WorkflowStepSpec{{Name: "batch", ForEach: &api.WorkflowForEachSpec{Items: "input.items", Action: api.WorkflowForEachActionSpec{Run: "send"}}}}}
	index := 0
	c := api.AutomationTestCase{Name: "item succeeds", Simulation: api.AutomationTestInputs{Input: json.RawMessage(`{"items":[1]}`), MockItemOutputs: map[string][]json.RawMessage{"batch": {json.RawMessage(`{"ok":true}`)}}}, Expectations: []api.AutomationCheckExpectation{{Step: "batch/action", Loop: "batch", ItemIndex: &index, State: "mocked"}}}
	result := state.EvaluateAutomationTestCase(t.Context(), def, c, api.PlanPro)
	if !result.Passed || len(result.Steps) != 1 || result.Steps[0].State != "mocked" || !result.Steps[0].Passed {
		t.Fatalf("loop item summary: %+v", result)
	}
}
