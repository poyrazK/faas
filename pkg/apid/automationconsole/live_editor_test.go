// adr: 1292
package automationconsole

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/automationchecks"
	"github.com/onebox-faas/faas/pkg/dashboard"
)

func liveEditorFixture(t *testing.T) dashboard.AutomationConsoleData {
	t.Helper()
	data := caseEditorFixture(t)
	cases, _ := testCases(data)
	zero := 0
	cases[0].Live = &api.AutomationLiveTest{ExpectedRunStatus: "failed", Expectations: []api.AutomationLiveExpectation{
		{Step: "collect", State: "succeeded", Output: json.RawMessage(`{"id":9007199254740993}`), AttemptCount: &zero},
		{Step: "deliver", State: "skipped", Output: json.RawMessage(`null`)},
	}}
	data.Scenarios, data.TestEditor = prettyScenarios(cases), nil
	return data
}

func liveEditorForm(data *dashboard.AutomationConsoleData) url.Values {
	f := caseEditorForm(data)
	v := data.TestEditor.Live
	f.Set("test_live_source", "guided")
	f.Set("test_live_index", strconv.Itoa(data.TestLiveIndex))
	f.Set("test_live_run_status", v.RunStatus)
	f.Set("test_live_step", v.Step)
	f.Set("test_live_state", v.State)
	f.Set("test_live_item_index", v.ItemIndex)
	f.Set("test_live_output", v.Output)
	f.Set("test_live_attempt_count", v.AttemptCount)
	if v.AssertOutput {
		f.Set("test_live_assert_output", "yes")
	}
	return f
}

func TestGuidedLiveEditsPreserveMockChecksAndExactJSON(t *testing.T) {
	data := liveEditorFixture(t)
	data.TestAssertionIndex = 1 // Mock and live assertion selections are independent.
	before, _ := editorTestSuite(data)
	if msg := applyCaseEditor(&data, "apply-test-case", liveEditorForm(&data)); msg != "" {
		t.Fatal(msg)
	}
	after, _ := editorTestSuite(data)
	if !automationchecks.EqualJSON([]byte(pretty(before)), []byte(pretty(after))) {
		t.Fatal("no-op live controls changed mock data, null/zero, or exact numbers")
	}
	f := liveEditorForm(&data)
	f.Set("test_live_run_status", "")
	f.Set("test_live_output", "null")
	f.Set("test_live_attempt_count", strconv.Itoa(api.AutomationLiveMaxAttempts))
	f.Set("test_live", "stale advanced JSON must not overwrite guided controls")
	if msg := applyCaseEditor(&data, "apply-test-case", f); msg != "" {
		t.Fatal(msg)
	}
	cases, _ := testCases(data)
	live := cases[0].Live
	if live.ExpectedRunStatus != "" || string(live.Expectations[0].Output) != "null" || *live.Expectations[0].AttemptCount != api.AutomationLiveMaxAttempts {
		t.Fatal("live default, explicit null, or resumed attempt count was lost")
	}
	if !automationchecks.EqualJSON([]byte(pretty(cases[0].Expectations)), []byte(pretty(before.Cases[0].Expectations))) {
		t.Fatal("live edits changed mock assertions")
	}
	f = liveEditorForm(&data)
	f.Del("test_live_assert_output")
	f.Set("test_live_attempt_count", "")
	if msg := applyCaseEditor(&data, "apply-test-case", f); msg != "" {
		t.Fatal(msg)
	}
	cases, _ = testCases(data)
	if e := cases[0].Live.Expectations[0]; e.Output != nil || e.AttemptCount != nil {
		t.Fatal("omitted checks became null/zero assertions")
	}
}

func TestGuidedLiveManagementAndAdvancedSource(t *testing.T) {
	data := caseEditorFixture(t)
	if msg := applyCaseEditor(&data, "add-live-assertion", liveEditorForm(&data)); msg != "" {
		t.Fatal(msg)
	}
	cases, _ := testCases(data)
	if cases[0].Live == nil || len(cases[0].Live.Expectations) != 1 || cases[0].Live.Expectations[0].State != "" {
		t.Fatal("new live assertions must require explicit checks")
	}
	f := liveEditorForm(&data)
	f.Set("test_live_state", "succeeded")
	if msg := applyCaseEditor(&data, "add-live-assertion", f); msg != "" {
		t.Fatal(msg)
	}
	cases, _ = testCases(data)
	if len(cases[0].Live.Expectations) != 2 || sameLiveTarget(cases[0].Live.Expectations[0], cases[0].Live.Expectations[1]) {
		t.Fatal("add created a duplicate live target")
	}
	if data.TestLiveIndex != 1 || data.TestAssertionIndex != 0 {
		t.Fatal("live navigation changed mock selection")
	}
	f = liveEditorForm(&data)
	f.Set("test_live_output", "invalid removed output")
	f.Set("test_live_assert_output", "yes")
	if msg := applyCaseEditor(&data, "remove-live-assertion", f); msg != "" {
		t.Fatal(msg)
	}
	if msg := applyCaseEditor(&data, "duplicate-test-case", liveEditorForm(&data)); msg != "" {
		t.Fatal(msg)
	}
	cases, _ = testCases(data)
	if data.TestLiveIndex != 0 || cases[1].Live == nil || len(cases[1].Live.Expectations) != 1 {
		t.Fatal("case duplication lost live configuration or selection")
	}
	f = liveEditorForm(&data)
	f.Set("test_live_run_status", "mocked")
	f.Set("test_live_output", "invalid removed output")
	f.Set("test_live_assert_output", "yes")
	if msg := applyCaseEditor(&data, "disable-live-test", f); msg != "" {
		t.Fatal(msg)
	}
	cases, _ = testCases(data)
	if cases[1].Live != nil || cases[0].Live == nil || len(cases[1].Expectations) == 0 {
		t.Fatal("disabling live checks affected another case or mock checks")
	}
	before := data.Scenarios
	f = liveEditorForm(&data)
	f.Set("test_editor_mode", "json")
	f.Set("test_live_state", "mocked")
	if msg := applyCaseEditor(&data, "test-editor", f); msg != "" || data.Scenarios != before {
		t.Fatal("advanced mode applied stale guided live fields")
	}
	f.Set("test_editor_mode", "guided")
	f.Set("test_editor_source", "json")
	if msg := applyCaseEditor(&data, "test-editor", f); msg != "" || data.Scenarios != before {
		t.Fatal("switching from advanced JSON applied stale live controls")
	}
}

func TestGuidedLiveValidationRetainsSelectedFields(t *testing.T) {
	for _, tc := range []struct{ field, value string }{
		{"test_live_state", "mocked"}, {"test_live_state", "running"}, {"test_live_run_status", "pending"},
		{"test_live_attempt_count", "-1"}, {"test_live_attempt_count", strconv.Itoa(api.AutomationLiveMaxAttempts + 1)},
		{"test_live_output", "invalid JSON"}, {"test_live_step", "unknown"}, {"test_live_index", "-1"},
		{"test_live_output", `"` + strings.Repeat("x", int(api.WorkflowRunInputMaxBytes)) + `"`},
	} {
		t.Run(tc.field+strconv.Itoa(len(tc.value)), func(t *testing.T) {
			data := liveEditorFixture(t)
			data.TestLiveIndex = 1
			before := data.Scenarios
			f := liveEditorForm(&data)
			f.Set(tc.field, tc.value)
			data.TestEditor, data.TestLiveIndex = nil, 0 // A fresh HTTP request.
			if msg := applyCaseEditor(&data, "apply-test-case", f); msg == "" || data.Scenarios != before {
				t.Fatal("invalid live controls changed case JSON")
			}
			if tc.field != "test_live_index" && (data.TestLiveIndex != 1 || data.TestEditor.Live.Step != f.Get("test_live_step")) {
				t.Fatal("validation error rebound fields to another live assertion")
			}
			if tc.field == "test_live_output" && data.TestEditor.Live.Output != tc.value {
				t.Fatal("validation error lost submitted live output")
			}
		})
	}
}

func TestGuidedLiveDisableFailureKeepsOriginalSelection(t *testing.T) {
	data := liveEditorFixture(t)
	data.TestLiveIndex = 1
	f := liveEditorForm(&data)
	f.Set("test_case_name", "")
	f.Set("test_live_output", "invalid output being removed")
	f.Set("test_live_assert_output", "yes")
	before := data.Scenarios
	data.TestEditor, data.TestLiveIndex = nil, 0
	if msg := applyCaseEditor(&data, "disable-live-test", f); msg == "" || data.Scenarios != before {
		t.Fatal("disabling accepted an invalid parent case")
	}
	if data.TestLiveIndex != 1 || data.TestEditor.Live.Step != "deliver" || data.TestEditor.Live.Output != f.Get("test_live_output") {
		t.Fatal("failed disable rebound live fields to another assertion")
	}
}

func TestGuidedLiveLoopIdentityAndDuplicateTargets(t *testing.T) {
	data := liveEditorFixture(t)
	def, _ := definition(data)
	def.Steps = append(def.Steps, api.WorkflowStepSpec{Name: "batch", ForEach: &api.WorkflowForEachSpec{Items: "input.items", Action: api.WorkflowForEachActionSpec{Path: "/handle"}}})
	data.Definition = pretty(def)
	f := liveEditorForm(&data)
	f.Set("test_live_step", "batch/action")
	f.Set("test_live_item_index", "127")
	if msg := applyCaseEditor(&data, "apply-test-case", f); msg != "" {
		t.Fatal(msg)
	}
	cases, _ := testCases(data)
	e := cases[0].Live.Expectations[0]
	if e.Loop != "batch" || e.ItemIndex == nil || *e.ItemIndex != 127 {
		t.Fatal("live loop target was not retained")
	}
	f = liveEditorForm(&data)
	f.Set("test_live_item_index", strconv.Itoa(api.WorkflowForEachMaxItems))
	if applyCaseEditor(&data, "apply-test-case", f) == "" {
		t.Fatal("out of range live loop item accepted")
	}
	f = liveEditorForm(&data)
	f.Set("test_live_step", "deliver")
	if applyCaseEditor(&data, "apply-test-case", f) == "" {
		t.Fatal("duplicate live target accepted")
	}
	f = liveEditorForm(&data)
	f.Set("test_live_step", "collect")
	if msg := applyCaseEditor(&data, "apply-test-case", f); msg != "" {
		t.Fatal(msg)
	}
	cases, _ = testCases(data)
	if e := cases[0].Live.Expectations[0]; e.Loop != "" || e.ItemIndex != nil {
		t.Fatal("root live assertion retained a loop identity")
	}
}

func TestGuidedLiveLimitsAndLegacyJSON(t *testing.T) {
	data := liveEditorFixture(t)
	f := caseEditorForm(&data)
	f.Set("test_live", `{"expectations":[{"step":"collect","output":9007199254740993,"attempt_count":0}]}`)
	if msg := applyCaseEditor(&data, "apply-test-case", f); msg != "" {
		t.Fatal(msg)
	}
	cases, _ := testCases(data)
	if e := cases[0].Live.Expectations[0]; string(e.Output) != "9007199254740993" || e.AttemptCount == nil || *e.AttemptCount != 0 {
		t.Fatal("legacy raw live form lost exact values")
	}
	cases[0].Live.Expectations = make([]api.AutomationLiveExpectation, api.AutomationLiveMaxExpectations+1)
	data.Scenarios, data.TestEditor = prettyScenarios(cases), nil
	setupTestEditor(&data)
	if data.TestEditor.Error == "" || data.TestEditor.Live != nil {
		t.Fatal("oversized live data expanded unbounded controls")
	}
	data = liveEditorFixture(t)
	cases, _ = testCases(data)
	cases[0].Live.Expectations = nil
	for i := 0; i < 3; i++ {
		length := int(api.WorkflowRunInputMaxBytes)
		if i == 0 {
			length -= 1024
		}
		cases[0].Live.Expectations = append(cases[0].Live.Expectations, api.AutomationLiveExpectation{
			Step: "step" + strconv.Itoa(i), Output: json.RawMessage(`"` + strings.Repeat("x", length-2) + `"`),
		})
	}
	data.Scenarios = prettyScenarios(cases)
	before := data.Scenarios
	f = liveEditorForm(&data)
	f.Set("test_live_output", `"`+strings.Repeat("x", int(api.WorkflowRunInputMaxBytes)-2)+`"`)
	if msg := applyCaseEditor(&data, "apply-test-case", f); msg == "" || data.Scenarios != before {
		t.Fatal("total live spec byte limit ignored")
	}
}
