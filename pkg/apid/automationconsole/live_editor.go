package automationconsole

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
)

func setupLiveEditor(data *dashboard.AutomationConsoleData, test *api.AutomationLiveTest, targets []string) {
	v := &dashboard.AutomationLiveEditor{Targets: slices.Clone(targets), CanAddAssertion: true,
		MaxAttempts: api.AutomationLiveMaxAttempts, MaxItemIndex: api.WorkflowForEachMaxItems - 1}
	data.TestEditor.Live = v
	if test == nil {
		return
	}
	v.Enabled, v.RunStatus = true, test.ExpectedRunStatus
	v.CanAddAssertion = len(test.Expectations) < api.AutomationLiveMaxExpectations
	for i, e := range test.Expectations {
		label := e.Step
		if e.ItemIndex != nil {
			label += " item " + strconv.Itoa(*e.ItemIndex)
		}
		v.Assertions = append(v.Assertions, dashboard.AutomationTestChoice{Index: i, Label: label})
	}
	if len(test.Expectations) == 0 {
		return
	}
	if data.TestLiveIndex < 0 || data.TestLiveIndex >= len(test.Expectations) {
		data.TestLiveIndex = 0
	}
	e := test.Expectations[data.TestLiveIndex]
	v.HasAssertion, v.Step, v.State = true, e.Step, e.State
	if !slices.Contains(v.Targets, e.Step) {
		v.Targets = append(v.Targets, e.Step)
	}
	if e.ItemIndex != nil {
		v.ItemIndex = strconv.Itoa(*e.ItemIndex)
	}
	v.AssertOutput = len(e.Output) != 0
	if v.AssertOutput {
		v.Output = string(e.Output)
	}
	if e.AttemptCount != nil {
		v.AttemptCount = strconv.Itoa(*e.AttemptCount)
	}
}

func selectPostedLiveAssertion(r *http.Request, data *dashboard.AutomationConsoleData, test *api.AutomationLiveTest) string {
	if r.PostFormValue("test_live_source") != "guided" || test == nil || len(test.Expectations) == 0 ||
		r.PathValue("action") == "remove-test-case" {
		return ""
	}
	index, err := testIndex(r.PostFormValue("test_live_index"), len(test.Expectations))
	if err != nil {
		return "Reload the selected live assertion."
	}
	data.TestLiveIndex = index
	return ""
}

func readLiveEditor(r *http.Request, v *dashboard.AutomationLiveEditor) {
	if v == nil || r.PostFormValue("test_live_source") != "guided" {
		return
	}
	v.RunStatus, v.Step, v.State = r.PostFormValue("test_live_run_status"), r.PostFormValue("test_live_step"), r.PostFormValue("test_live_state")
	v.ItemIndex, v.Output, v.AttemptCount = r.PostFormValue("test_live_item_index"), r.PostFormValue("test_live_output"), r.PostFormValue("test_live_attempt_count")
	v.AssertOutput = r.PostFormValue("test_live_assert_output") == "yes"
}

func patchLiveTest(r *http.Request, data *dashboard.AutomationConsoleData, c *api.AutomationPublishCheckScenario) string {
	if r.PathValue("action") == "disable-live-test" {
		return "" // Removing configuration must work even with invalid local fields.
	}
	if r.PostFormValue("test_live_source") != "guided" {
		// Retain the older raw form contract. Advanced case JSON is authoritative
		// when the parent editor is in JSON mode and never reaches this function.
		if _, posted := r.PostForm["test_live"]; posted {
			if decode(r.PostFormValue("test_live"), api.AutomationLiveSpecMaxBytes, &c.Live) != nil {
				return "Enter valid live expectations in advanced test JSON, or null to disable them."
			}
		}
		return ""
	}
	if c.Live == nil {
		return "" // Adding an assertion explicitly enables local live configuration.
	}
	v := data.TestEditor.Live
	switch v.RunStatus {
	case "", "succeeded", "failed", "dead":
		c.Live.ExpectedRunStatus = v.RunStatus
	default:
		return "Choose a supported terminal live run state."
	}
	if len(c.Live.Expectations) == 0 || r.PathValue("action") == "remove-live-assertion" {
		return ""
	}
	e := &c.Live.Expectations[data.TestLiveIndex]
	if message := patchLiveAssertion(v, e); message != "" {
		return message
	}
	for i, other := range c.Live.Expectations {
		if i != data.TestLiveIndex && sameLiveTarget(*e, other) {
			return "This step or loop item already has a live assertion."
		}
	}
	return ""
}

func patchLiveAssertion(v *dashboard.AutomationLiveEditor, e *api.AutomationLiveExpectation) string {
	if v.Step != e.Step && !slices.Contains(v.Targets, v.Step) {
		return "Choose a live assertion step from this workflow."
	}
	e.Step = v.Step
	if strings.HasSuffix(e.Step, "/action") {
		index, err := testIndex(v.ItemIndex, api.WorkflowForEachMaxItems)
		if err != nil {
			return fmt.Sprintf("Choose a live loop item index from 0 to %d.", api.WorkflowForEachMaxItems-1)
		}
		e.Loop, e.ItemIndex = strings.TrimSuffix(e.Step, "/action"), &index
	} else {
		e.Loop, e.ItemIndex = "", nil
	}
	switch v.State {
	case "", "succeeded", "failed", "dead", "skipped":
		e.State = v.State
	default:
		return "Choose a real terminal step state for live verification."
	}
	e.Output = nil
	if v.AssertOutput {
		if int64(len(v.Output)) > api.WorkflowRunInputMaxBytes || !json.Valid([]byte(v.Output)) {
			return "Enter valid expected live output JSON, including null if you expect null."
		}
		e.Output = json.RawMessage(v.Output)
	}
	e.AttemptCount = nil
	if v.AttemptCount != "" {
		count, err := testIndex(v.AttemptCount, api.AutomationLiveMaxAttempts+1)
		if err != nil {
			return fmt.Sprintf("Expected live attempt count must be between 0 and %d.", api.AutomationLiveMaxAttempts)
		}
		e.AttemptCount = &count
	}
	return ""
}

func sameLiveTarget(a, b api.AutomationLiveExpectation) bool {
	if a.Loop == "" || b.Loop == "" {
		return a.Loop == b.Loop && a.Step == b.Step
	}
	return a.Loop == b.Loop && a.ItemIndex != nil && b.ItemIndex != nil && *a.ItemIndex == *b.ItemIndex
}

func manageLiveAssertions(r *http.Request, data *dashboard.AutomationConsoleData, cases []api.AutomationPublishCheckScenario) string {
	if len(cases) == 0 {
		return "Add a case before editing live assertions."
	}
	c := &cases[data.TestCaseIndex]
	switch r.PathValue("action") {
	case "disable-live-test":
		c.Live, data.TestLiveIndex = nil, 0
	case "select-live-assertion":
		if c.Live == nil {
			return "Add a live assertion first."
		}
		index, err := testIndex(r.PostFormValue("test_live_select"), len(c.Live.Expectations))
		if err != nil {
			return "Choose an existing live assertion."
		}
		data.TestLiveIndex = index
	case "remove-live-assertion":
		if c.Live != nil && len(c.Live.Expectations) > 0 {
			c.Live.Expectations = slices.Delete(c.Live.Expectations, data.TestLiveIndex, data.TestLiveIndex+1)
			data.TestLiveIndex = 0
		}
	case "add-live-assertion":
		if c.Live == nil {
			c.Live = &api.AutomationLiveTest{}
		}
		if len(c.Live.Expectations) >= api.AutomationLiveMaxExpectations {
			return "This case has reached its live assertion limit."
		}
		def, err := definition(*data)
		if err != nil || len(def.Steps) == 0 {
			return "Define workflow steps before adding live assertions."
		}
		next := nextLiveAssertion(def, c.Live.Expectations)
		if next == nil {
			return "Every available step and loop item already has a live assertion."
		}
		// New assertions deliberately have no checks until the author chooses one.
		c.Live.Expectations = append(c.Live.Expectations, *next)
		data.TestLiveIndex = len(c.Live.Expectations) - 1
	}
	return ""
}

func nextLiveAssertion(def api.WorkflowSpec, checks []api.AutomationLiveExpectation) *api.AutomationLiveExpectation {
	for _, step := range def.Steps {
		e := api.AutomationLiveExpectation{Step: step.Name}
		if !slices.ContainsFunc(checks, func(other api.AutomationLiveExpectation) bool { return sameLiveTarget(e, other) }) {
			return &e
		}
		if step.ForEach != nil {
			for item := 0; item < api.WorkflowForEachMaxItems; item++ {
				e = api.AutomationLiveExpectation{Step: step.Name + "/action", Loop: step.Name, ItemIndex: &item}
				if !slices.ContainsFunc(checks, func(other api.AutomationLiveExpectation) bool { return sameLiveTarget(e, other) }) {
					return &e
				}
			}
		}
	}
	return nil
}

func liveEditorBounds(cases []api.AutomationPublishCheckScenario) string {
	for _, c := range cases {
		if c.Live == nil {
			continue
		}
		raw, err := json.Marshal(c.Live)
		if err != nil || int64(len(raw)) > api.AutomationLiveSpecMaxBytes || len(c.Live.Expectations) > api.AutomationLiveMaxExpectations {
			return "The live expectations exceed the saved live test size or assertion limit."
		}
	}
	return ""
}
