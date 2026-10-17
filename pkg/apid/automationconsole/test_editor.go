package automationconsole

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
)

func testCases(data dashboard.AutomationConsoleData) ([]api.AutomationPublishCheckScenario, error) {
	var cases []api.AutomationPublishCheckScenario
	err := decode(data.Scenarios, api.AutomationPublishCheckMaxBytes, &cases)
	if err == nil && len(cases) > api.AutomationPublishCheckMaxScenarios {
		err = fmt.Errorf("too many cases")
	}
	for _, c := range cases {
		if len(c.Expectations) > api.AutomationPublishCheckMaxExpectations {
			return nil, fmt.Errorf("too many assertions")
		}
		if c.Live != nil && len(c.Live.Expectations) > api.AutomationLiveMaxExpectations {
			return nil, fmt.Errorf("too many live assertions")
		}
	}
	if liveEditorBounds(cases) != "" {
		return nil, fmt.Errorf("live expectations exceed their size limit")
	}
	return cases, err
}

func testTargets(def api.WorkflowSpec) []string {
	var targets []string
	for _, step := range def.Steps {
		targets = append(targets, step.Name)
		if step.ForEach != nil {
			targets = append(targets, step.Name+"/action")
		}
	}
	return targets
}

func setupTestEditor(data *dashboard.AutomationConsoleData) {
	if data.TestEditorMode == "" {
		data.TestEditorMode = "guided"
	}
	if data.TestEditor != nil {
		return // Preserve posted fields on validation errors.
	}
	view := &dashboard.AutomationTestEditor{}
	data.TestEditor = view
	cases, err := testCases(*data)
	if err != nil {
		view.Error = "The case JSON cannot be opened in the guided editor. Use advanced JSON to correct it."
		return
	}
	def, _ := definition(*data)
	view.Targets = testTargets(def)
	view.CanAddCase = len(cases) < api.AutomationPublishCheckMaxScenarios
	for i, c := range cases {
		view.Cases = append(view.Cases, dashboard.AutomationTestChoice{Index: i, Label: c.Name})
	}
	if len(cases) == 0 {
		return
	}
	if data.TestCaseIndex < 0 || data.TestCaseIndex >= len(cases) {
		data.TestCaseIndex = 0
	}
	c := cases[data.TestCaseIndex]
	view.HasCase, view.Name, view.Simulation = true, c.Name, pretty(api.TestInputsFromSimulation(c.Simulation))
	setupLiveEditor(data, c.Live, view.Targets)
	view.CanAddAssertion = len(c.Expectations) < api.AutomationPublishCheckMaxExpectations
	for i, e := range c.Expectations {
		label := e.Step
		if e.ItemIndex != nil {
			label += " item " + strconv.Itoa(*e.ItemIndex)
		}
		view.Assertions = append(view.Assertions, dashboard.AutomationTestChoice{Index: i, Label: label})
	}
	if len(c.Expectations) == 0 {
		return
	}
	if data.TestAssertionIndex < 0 || data.TestAssertionIndex >= len(c.Expectations) {
		data.TestAssertionIndex = 0
	}
	e := c.Expectations[data.TestAssertionIndex]
	view.HasAssertion, view.Step, view.State = true, e.Step, e.State
	if !slices.Contains(view.Targets, e.Step) {
		view.Targets = append(view.Targets, e.Step)
	}
	if e.ItemIndex != nil {
		view.ItemIndex = strconv.Itoa(*e.ItemIndex)
	}
	view.AssertOutput = len(e.Output) != 0
	if view.AssertOutput {
		view.Output = string(e.Output)
	}
	if e.AttemptCount != nil {
		view.AttemptCount = strconv.Itoa(*e.AttemptCount)
	}
}

// Missing mode is the legacy JSON form contract. Switching to guided mode first
// loads the JSON without applying stale controls from a previous selection.
func prepareTestEditor(r *http.Request, data *dashboard.AutomationConsoleData) string {
	data.TestEditorMode = r.PostFormValue("test_editor_mode")
	if data.TestEditorMode == "" {
		data.TestEditorMode = "json"
	}
	if data.TestEditorMode != "guided" && data.TestEditorMode != "json" {
		return "Choose guided tests or advanced test JSON."
	}
	if data.TestEditorMode == "json" || r.PostFormValue("test_editor_source") != "guided" {
		return ""
	}
	cases, err := testCases(*data)
	if err != nil {
		return "Correct the case JSON before editing cases."
	}
	if len(cases) > 0 {
		index, err := testIndex(r.PostFormValue("test_case_index"), len(cases))
		if err != nil {
			return "Reload the selected test case."
		}
		data.TestCaseIndex = index
		if message := selectPostedLiveAssertion(r, data, cases[index].Live); message != "" {
			return message
		}
		if len(cases[index].Expectations) > 0 && r.PathValue("action") != "remove-test-case" {
			assertion, err := testIndex(r.PostFormValue("test_assertion_index"), len(cases[index].Expectations))
			if err != nil {
				return "Reload the selected assertion."
			}
			data.TestAssertionIndex = assertion
		}
	}
	setupTestEditor(data)
	view := data.TestEditor
	view.Name, view.Simulation = r.PostFormValue("test_case_name"), r.PostFormValue("test_case_simulation")
	readLiveEditor(r, view.Live)
	view.Step, view.State, view.ItemIndex = r.PostFormValue("test_step"), r.PostFormValue("test_state"), r.PostFormValue("test_item_index")
	view.Output, view.AttemptCount, view.AssertOutput = r.PostFormValue("test_output"), r.PostFormValue("test_attempt_count"), r.PostFormValue("test_assert_output") == "yes"
	if len(cases) > 0 && r.PathValue("action") != "remove-test-case" {
		if message := patchTestCase(r, data, cases); message != "" {
			return message
		}
	}
	caseIndex, assertionIndex, liveIndex := data.TestCaseIndex, data.TestAssertionIndex, data.TestLiveIndex
	if message := manageTestCases(r, data, &cases); message != "" {
		return message
	}
	if message := liveEditorBounds(cases); message != "" {
		data.TestCaseIndex, data.TestAssertionIndex, data.TestLiveIndex = caseIndex, assertionIndex, liveIndex
		return message
	}
	raw := prettyScenarios(cases)
	if int64(len(raw)) > api.AutomationPublishCheckMaxBytes {
		data.TestCaseIndex, data.TestAssertionIndex, data.TestLiveIndex = caseIndex, assertionIndex, liveIndex
		return "The test cases exceed the saved test data size limit."
	}
	data.Scenarios, data.TestEditor = raw, nil
	return ""
}

func testIndex(raw string, count int) (int, error) {
	index, err := strconv.Atoi(raw)
	if err != nil || index < 0 || index >= count {
		return 0, fmt.Errorf("invalid selection")
	}
	return index, nil
}

func patchTestCase(r *http.Request, data *dashboard.AutomationConsoleData, cases []api.AutomationPublishCheckScenario) string {
	view := data.TestEditor
	c := &cases[data.TestCaseIndex]
	name := strings.TrimSpace(view.Name)
	if name == "" || len(name) > api.AutomationTestCaseNameMaxBytes || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return "Give the case a name of 1–128 bytes without control characters."
	}
	for i, other := range cases {
		if i != data.TestCaseIndex && strings.TrimSpace(other.Name) == name {
			return "Choose a unique test case name."
		}
	}
	var inputs api.AutomationTestInputs
	if decode(view.Simulation, api.AutomationSimulationRequestMaxBytes, &inputs) != nil {
		return "Enter valid input and mock JSON for this case."
	}
	c.Name, c.Simulation = name, inputs.Simulation(api.WorkflowSpec{})
	if message := patchLiveTest(r, data, c); message != "" {
		return message
	}
	if len(c.Expectations) == 0 {
		return ""
	}
	if r.PathValue("action") == "remove-test-assertion" {
		return ""
	}
	def, err := definition(*data)
	if err != nil {
		return "Correct the workflow definition before choosing assertion steps."
	}
	return patchTestAssertion(view, &c.Expectations[data.TestAssertionIndex], testTargets(def))
}

func patchTestAssertion(view *dashboard.AutomationTestEditor, e *api.AutomationCheckExpectation, targets []string) string {
	if view.Step != e.Step && !slices.Contains(targets, view.Step) {
		return "Choose a step from this workflow."
	}
	e.Step = view.Step
	if strings.HasSuffix(e.Step, "/action") {
		index, err := testIndex(view.ItemIndex, api.WorkflowForEachMaxItems)
		if err != nil {
			return "Choose a loop item index from 0 to 127."
		}
		e.Loop, e.ItemIndex = strings.TrimSuffix(e.Step, "/action"), &index
	} else {
		e.Loop, e.ItemIndex = "", nil
	}
	switch view.State {
	case "", "mocked", "would_execute", "resolved", "expanded", "would_wait", "would_retry", "timed_out", "failed", "dead", "skipped", "blocked", "error":
		e.State = view.State
	default:
		return "Choose a supported expected state."
	}
	e.Output = nil
	if view.AssertOutput {
		if int64(len(view.Output)) > api.WorkflowRunInputMaxBytes || !json.Valid([]byte(view.Output)) {
			return "Enter valid expected output JSON, including null if you expect null."
		}
		e.Output = json.RawMessage(view.Output)
	}
	e.AttemptCount = nil
	if view.AttemptCount != "" {
		count, err := testIndex(view.AttemptCount, api.WorkflowRetryMaxAttempts+1)
		if err != nil {
			return fmt.Sprintf("Expected attempt count must be between 0 and %d.", api.WorkflowRetryMaxAttempts)
		}
		if e.Attempts != nil && count != len(*e.Attempts) {
			return "The count must match the detailed attempt assertions. Edit those in advanced JSON."
		}
		e.AttemptCount = &count
	}
	return ""
}

func manageTestCases(r *http.Request, data *dashboard.AutomationConsoleData, cases *[]api.AutomationPublishCheckScenario) string {
	switch r.PathValue("action") {
	case "select-test-case":
		index, err := testIndex(r.PostFormValue("test_case_select"), len(*cases))
		if err != nil {
			return "Choose an existing test case."
		}
		data.TestCaseIndex, data.TestAssertionIndex, data.TestLiveIndex = index, 0, 0
	case "add-test-case", "duplicate-test-case":
		return addTestCase(r, data, cases)
	case "remove-test-case":
		if len(*cases) > 0 {
			*cases = slices.Delete(*cases, data.TestCaseIndex, data.TestCaseIndex+1)
			data.TestCaseIndex, data.TestAssertionIndex, data.TestLiveIndex = 0, 0, 0
		}
	case "select-test-assertion", "add-test-assertion", "remove-test-assertion":
		return manageTestAssertions(r, data, *cases)
	case "select-live-assertion", "add-live-assertion", "remove-live-assertion", "disable-live-test":
		return manageLiveAssertions(r, data, *cases)
	}
	return ""
}

func addTestCase(r *http.Request, data *dashboard.AutomationConsoleData, cases *[]api.AutomationPublishCheckScenario) string {
	if len(*cases) >= api.AutomationPublishCheckMaxScenarios {
		return "The test suite has reached its case limit."
	}
	c := api.AutomationPublishCheckScenario{Expectations: []api.AutomationCheckExpectation{}}
	if r.PathValue("action") == "duplicate-test-case" {
		if len(*cases) == 0 {
			return "Choose a case to duplicate."
		}
		// JSON roundtrip deep-copies RawMessage and pointer fields without floats.
		if json.Unmarshal([]byte(pretty((*cases)[data.TestCaseIndex])), &c) != nil {
			return "The selected case could not be duplicated."
		}
	} else {
		sim, err := sample(*data, api.WorkflowSpec{})
		if err != nil {
			return "Enter valid sample JSON before adding a case."
		}
		c.Simulation = sim
	}
	for n := 1; ; n++ {
		c.Name = fmt.Sprintf("Test case %d", n)
		if !slices.ContainsFunc(*cases, func(other api.AutomationPublishCheckScenario) bool { return strings.TrimSpace(other.Name) == c.Name }) {
			break
		}
	}
	*cases = append(*cases, c)
	data.TestCaseIndex, data.TestAssertionIndex, data.TestLiveIndex = len(*cases)-1, 0, 0
	return ""
}

func manageTestAssertions(r *http.Request, data *dashboard.AutomationConsoleData, cases []api.AutomationPublishCheckScenario) string {
	if len(cases) == 0 {
		return "Add a case before editing assertions."
	}
	c := &cases[data.TestCaseIndex]
	switch r.PathValue("action") {
	case "select-test-assertion":
		index, err := testIndex(r.PostFormValue("test_assertion_select"), len(c.Expectations))
		if err != nil {
			return "Choose an existing assertion."
		}
		data.TestAssertionIndex = index
	case "remove-test-assertion":
		if len(c.Expectations) > 0 {
			c.Expectations = slices.Delete(c.Expectations, data.TestAssertionIndex, data.TestAssertionIndex+1)
			data.TestAssertionIndex = 0
		}
	case "add-test-assertion":
		if len(c.Expectations) >= api.AutomationPublishCheckMaxExpectations {
			return "This case has reached its assertion limit."
		}
		def, err := definition(*data)
		if err != nil || len(def.Steps) == 0 {
			return "Define workflow steps before adding assertions."
		}
		// Start unconfigured; saving and publishing still reject empty checks.
		var next *api.AutomationCheckExpectation
		for _, step := range def.Steps {
			candidate := api.AutomationCheckExpectation{Step: step.Name}
			if !slices.ContainsFunc(c.Expectations, func(e api.AutomationCheckExpectation) bool { return e.Step == candidate.Step && e.Loop == "" }) {
				next = &candidate
				break
			}
			if step.ForEach != nil {
				for item := 0; item < api.WorkflowForEachMaxItems; item++ {
					candidate = api.AutomationCheckExpectation{Step: step.Name + "/action", Loop: step.Name, ItemIndex: &item}
					if !slices.ContainsFunc(c.Expectations, func(e api.AutomationCheckExpectation) bool {
						return e.Loop == step.Name && e.ItemIndex != nil && *e.ItemIndex == item
					}) {
						next = &candidate
						break
					}
				}
				if next != nil {
					break
				}
			}
		}
		if next == nil {
			return "Every available step and loop item already has an assertion."
		}
		c.Expectations = append(c.Expectations, *next)
		data.TestAssertionIndex = len(c.Expectations) - 1
	}
	return ""
}

func prepareConsoleEditors(r *http.Request, data *dashboard.AutomationConsoleData) string {
	if message := prepareEditor(r, data); message != "" {
		return message
	}
	return prepareTestEditor(r, data)
}
