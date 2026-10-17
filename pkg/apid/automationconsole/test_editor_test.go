// adr: 1290
package automationconsole

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/automationchecks"
	"github.com/onebox-faas/faas/pkg/automationtemplates"
	"github.com/onebox-faas/faas/pkg/dashboard"
)

func caseEditorFixture(t *testing.T) dashboard.AutomationConsoleData {
	t.Helper()
	bundle, err := automationtemplates.Load("scheduled-report")
	if err != nil {
		t.Fatal(err)
	}
	var data dashboard.AutomationConsoleData
	setBundle(&data, bundle)
	setupTestEditor(&data)
	return data
}

func caseEditorForm(data *dashboard.AutomationConsoleData) url.Values {
	data.TestEditor = nil
	setupTestEditor(data)
	v := data.TestEditor
	form := url.Values{"test_editor_mode": {"guided"}, "test_editor_source": {"guided"},
		"test_case_index": {strconv.Itoa(data.TestCaseIndex)}, "test_assertion_index": {strconv.Itoa(data.TestAssertionIndex)},
		"test_case_name": {v.Name}, "test_case_simulation": {v.Simulation}, "test_step": {v.Step}, "test_state": {v.State},
		"test_item_index": {v.ItemIndex}, "test_output": {v.Output}, "test_attempt_count": {v.AttemptCount}}
	if v.AssertOutput {
		form.Set("test_assert_output", "yes")
	}
	return form
}

func applyCaseEditor(data *dashboard.AutomationConsoleData, action string, form url.Values) string {
	r := reportRequest(form)
	r.SetPathValue("action", action)
	return prepareTestEditor(r, data)
}

func TestGuidedCasesPreserveAdvancedDataAndExactJSON(t *testing.T) {
	data := caseEditorFixture(t)
	cases, _ := testCases(data)
	cases[0].Simulation.Input = json.RawMessage(`{"id":9007199254740993}`)
	cases[0].Live = &api.AutomationLiveTest{Expectations: []api.AutomationLiveExpectation{{Step: "collect", State: "succeeded", Output: json.RawMessage(`9007199254740993`)}}}
	cases[0].Simulation.MockItemAttempts = map[string]map[string][]api.AutomationSimulationMockAttempt{"batch": {"0": {{Outcome: "success", Output: json.RawMessage(`null`)}}}}
	yes, reason := true, "PRIVATE-REASON"
	count := 0
	attempts := []api.AutomationCheckAttempt{}
	cases[0].Expectations[0].Reason, cases[0].Expectations[0].WhenMatched = &reason, &yes
	cases[0].Expectations[0].AttemptCount, cases[0].Expectations[0].Attempts = &count, &attempts
	cases[0].Expectations[0].Output = json.RawMessage(`9007199254740993`)
	data.Scenarios = prettyScenarios(cases)
	before, _ := editorTestSuite(data)
	form := caseEditorForm(&data)
	if message := applyCaseEditor(&data, "apply-test-case", form); message != "" {
		t.Fatal(message)
	}
	after, _ := editorTestSuite(data)
	if !automationchecks.EqualJSON([]byte(pretty(before)), []byte(pretty(after))) {
		t.Fatal("no-op form discarded advanced fields or rounded JSON")
	}
	if message := applyCaseEditor(&data, "duplicate-test-case", caseEditorForm(&data)); message != "" {
		t.Fatal(message)
	}
	copied, _ := testCases(data)
	if len(copied) != len(cases)+1 || copied[len(copied)-1].Name == cases[0].Name || !automationchecks.EqualJSON(copied[len(copied)-1].Simulation.Input, []byte(`{"id":9007199254740993}`)) {
		t.Fatal("duplicate lost data or name uniqueness")
	}
	form = caseEditorForm(&data)
	form.Set("test_case_name", "renamed")
	form.Set("test_output", "null")
	if message := applyCaseEditor(&data, "apply-test-case", form); message != "" {
		t.Fatal(message)
	}
	copied, _ = testCases(data)
	e := copied[data.TestCaseIndex].Expectations[0]
	if string(e.Output) != "null" || e.AttemptCount == nil || *e.AttemptCount != 0 || e.WhenMatched == nil || !*e.WhenMatched || *e.Reason != reason || e.Attempts == nil {
		t.Fatal("null/zero or advanced assertion fields were lost")
	}
	form = caseEditorForm(&data)
	form.Del("test_assert_output")
	form.Set("test_attempt_count", "")
	if message := applyCaseEditor(&data, "apply-test-case", form); message != "" {
		t.Fatal(message)
	}
	copied, _ = testCases(data)
	e = copied[data.TestCaseIndex].Expectations[0]
	if e.Output != nil || e.AttemptCount != nil || e.Attempts == nil {
		t.Fatal("optional controls did not preserve nil versus zero/null")
	}
}

func TestGuidedCaseManagementAndJSONSource(t *testing.T) {
	data := caseEditorFixture(t)
	initial, _ := testCases(data)
	for _, action := range []string{"add-test-case", "add-test-assertion"} {
		if message := applyCaseEditor(&data, action, caseEditorForm(&data)); message != "" {
			t.Fatal(message)
		}
	}
	cases, _ := testCases(data)
	if len(cases) != len(initial)+1 || len(cases[data.TestCaseIndex].Expectations) != 1 || cases[data.TestCaseIndex].Expectations[0].State != "" {
		t.Fatal("new assertions must require an explicit check")
	}
	form := caseEditorForm(&data)
	form.Set("test_state", "mocked")
	if message := applyCaseEditor(&data, "add-test-assertion", form); message != "" {
		t.Fatal(message)
	}
	cases, _ = testCases(data)
	if cases[data.TestCaseIndex].Expectations[0].Step == cases[data.TestCaseIndex].Expectations[1].Step {
		t.Fatal("add created a duplicate assertion target")
	}
	for _, action := range []string{"remove-test-assertion", "remove-test-case"} {
		form := caseEditorForm(&data)
		form.Set("test_output", "invalid JSON in removed assertion")
		form.Set("test_assert_output", "yes")
		if message := applyCaseEditor(&data, action, form); message != "" {
			t.Fatal(message)
		}
	}
	if cases, _ := testCases(data); len(cases) != len(initial) {
		t.Fatal("case removal failed")
	}
	before := data.Scenarios
	form = caseEditorForm(&data)
	form.Set("test_editor_mode", "json")
	form.Set("test_case_name", "ignored stale field")
	if message := applyCaseEditor(&data, "test-editor", form); message != "" || data.Scenarios != before {
		t.Fatal("JSON mode applied guided fields")
	}
}

func TestGuidedCaseValidationRetainsSubmittedFields(t *testing.T) {
	for _, tc := range []struct{ field, value string }{
		{"test_case_name", ""}, {"test_case_simulation", "broken"}, {"test_output", "broken"},
		{"test_attempt_count", "-1"}, {"test_attempt_count", "26"}, {"test_step", "unknown"},
		{"test_state", "approved"}, {"test_case_index", "-1"}, {"test_assertion_index", "99999"},
	} {
		t.Run(tc.field+tc.value, func(t *testing.T) {
			data := caseEditorFixture(t)
			before := data.Scenarios
			form := caseEditorForm(&data)
			form.Set("test_assert_output", "yes")
			form.Set(tc.field, tc.value)
			if message := applyCaseEditor(&data, "apply-test-case", form); message == "" || data.Scenarios != before {
				t.Fatal("invalid input changed case JSON")
			}
			if tc.field == "test_case_simulation" && data.TestEditor.Simulation != tc.value {
				t.Fatal("invalid form lost submitted input")
			}
		})
	}
	data := caseEditorFixture(t)
	form := caseEditorForm(&data)
	form.Set("test_case_name", strings.Repeat("é", api.AutomationTestCaseNameMaxBytes))
	if applyCaseEditor(&data, "apply-test-case", form) == "" {
		t.Fatal("name byte limit ignored")
	}
}

func TestGuidedLoopSelectionAndLimits(t *testing.T) {
	data := caseEditorFixture(t)
	def, _ := definition(data)
	def.Steps = append(def.Steps, api.WorkflowStepSpec{Name: "batch", ForEach: &api.WorkflowForEachSpec{Items: "input.items", Action: api.WorkflowForEachActionSpec{Path: "/handle"}}})
	data.Definition = pretty(def)
	form := caseEditorForm(&data)
	form.Set("test_step", "batch/action")
	form.Set("test_item_index", "7")
	if message := applyCaseEditor(&data, "apply-test-case", form); message != "" {
		t.Fatal(message)
	}
	cases, _ := testCases(data)
	e := cases[0].Expectations[0]
	if e.Loop != "batch" || e.ItemIndex == nil || *e.ItemIndex != 7 {
		t.Fatal("item assertion target was not bound")
	}
	form = caseEditorForm(&data)
	form.Set("test_item_index", strconv.Itoa(api.WorkflowForEachMaxItems))
	if applyCaseEditor(&data, "apply-test-case", form) == "" {
		t.Fatal("out of range item accepted")
	}
	form = caseEditorForm(&data)
	form.Set("test_step", "collect")
	if message := applyCaseEditor(&data, "apply-test-case", form); message != "" {
		t.Fatal(message)
	}
	cases, _ = testCases(data)
	if cases[0].Expectations[0].Loop != "" || cases[0].Expectations[0].ItemIndex != nil {
		t.Fatal("root selection retained loop target")
	}
	for len(cases) < api.AutomationPublishCheckMaxScenarios {
		c := cases[0]
		c.Name = "Case " + strconv.Itoa(len(cases))
		cases = append(cases, c)
	}
	data.Scenarios = prettyScenarios(cases)
	if applyCaseEditor(&data, "add-test-case", caseEditorForm(&data)) == "" {
		t.Fatal("case limit ignored")
	}
	cases[0].Expectations = make([]api.AutomationCheckExpectation, api.AutomationPublishCheckMaxExpectations+1)
	data.Scenarios = prettyScenarios(cases[:1])
	data.TestEditor = nil
	setupTestEditor(&data)
	if data.TestEditor.Error == "" || data.TestEditor.HasAssertion {
		t.Fatal("oversized JSON expanded unbounded controls")
	}
}

func TestGuidedErrorKeepsSelectedAssertion(t *testing.T) {
	data := caseEditorFixture(t)
	cases, _ := testCases(data)
	if len(cases[0].Expectations) < 2 {
		t.Fatal("fixture requires two assertions")
	}
	data.TestAssertionIndex = 1
	form := caseEditorForm(&data)
	form.Set("test_case_name", "")
	// A request starts with only the posted editor JSON, not previous view state.
	data.TestAssertionIndex, data.TestEditor = 0, nil
	if applyCaseEditor(&data, "apply-test-case", form) == "" || data.TestAssertionIndex != 1 || data.TestEditor.Step != form.Get("test_step") {
		t.Fatal("validation error rebound posted fields to another assertion")
	}
}

func TestGuidedSizeRejectionKeepsOriginalSelection(t *testing.T) {
	data := caseEditorFixture(t)
	cases, _ := testCases(data)
	input := json.RawMessage(`"` + strings.Repeat("x", int(api.AutomationSimulationRequestMaxBytes)-(8<<10)) + `"`)
	cases = cases[:1]
	cases[0].Simulation.Input = input
	for len(cases) < int(api.AutomationPublishCheckMaxBytes/api.AutomationSimulationRequestMaxBytes) {
		c := cases[0]
		c.Name = "Case " + strconv.Itoa(len(cases))
		cases = append(cases, c)
	}
	data.Scenarios = prettyScenarios(cases)
	before := data.Scenarios
	data.TestCaseIndex = 1
	form := caseEditorForm(&data)
	if message := applyCaseEditor(&data, "duplicate-test-case", form); message == "" {
		t.Fatal("oversized duplication accepted")
	}
	if data.TestCaseIndex != 1 || data.Scenarios != before || data.TestEditor.Name != cases[1].Name {
		t.Fatal("size rejection moved the selection or discarded original data")
	}
}
