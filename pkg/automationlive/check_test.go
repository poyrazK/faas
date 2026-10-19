// adr: 1291
package automationlive

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestLiveAssertionsAreSeparateAndBounded(t *testing.T) {
	def := api.WorkflowSpec{Steps: []api.WorkflowStepSpec{{Name: "main"}, {Name: "batch", ForEach: &api.WorkflowForEachSpec{}}}}
	index, zero := 0, 0
	valid := api.AutomationLiveTest{Expectations: []api.AutomationLiveExpectation{{Step: "main", State: "succeeded"}, {Step: "batch/action", Loop: "batch", ItemIndex: &index, Output: json.RawMessage(`null`), AttemptCount: &zero}}}
	if err := Validate(def, valid); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		edit func(*api.AutomationLiveTest)
	}{
		{"mock state", func(v *api.AutomationLiveTest) { v.Expectations[0].State = "mocked" }},
		{"running state", func(v *api.AutomationLiveTest) { v.Expectations[0].State = "running" }},
		{"unknown target", func(v *api.AutomationLiveTest) { v.Expectations[0].Step = "unknown" }},
		{"duplicate", func(v *api.AutomationLiveTest) { v.Expectations = append(v.Expectations, v.Expectations[0]) }},
		{"empty check", func(v *api.AutomationLiveTest) { v.Expectations[0].State = "" }},
		{"missing item", func(v *api.AutomationLiveTest) { v.Expectations[1].ItemIndex = nil }},
		{"pending run", func(v *api.AutomationLiveTest) { v.ExpectedRunStatus = "pending" }},
		{"too many", func(v *api.AutomationLiveTest) {
			v.Expectations = make([]api.AutomationLiveExpectation, api.AutomationLiveMaxExpectations+1)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, _ := json.Marshal(valid)
			var test api.AutomationLiveTest
			_ = json.Unmarshal(raw, &test)
			tc.edit(&test)
			if Validate(def, test) == nil {
				t.Fatal("invalid live assertion accepted")
			}
		})
	}
}

func TestLiveComparisonPreservesNullNumbersAndSafeFailures(t *testing.T) {
	count := 2
	e := api.AutomationLiveExpectation{Step: "main", State: "succeeded", Output: json.RawMessage(`{"id":9007199254740993}`), AttemptCount: &count}
	rows := []ObservedStep{{State: "succeeded", Output: json.RawMessage(`{"id":9007199254740993.0}`), AttemptCount: 2}}
	if result := Compare(e, rows); !result.Passed {
		t.Fatal("exact numeric comparison failed")
	}
	rows[0] = ObservedStep{State: "failed", Output: json.RawMessage(`"PRIVATE-ACTUAL-OUTPUT"`), AttemptCount: 1}
	result := Compare(e, rows)
	if result.Passed || !reflect.DeepEqual(result.Mismatches, []string{"state", "output", "attempt_count"}) {
		t.Fatal("failed fields were not identified")
	}
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), "PRIVATE") || strings.Contains(string(raw), "9007199254740993") {
		t.Fatal("comparison retained an output value")
	}
	e.State, e.AttemptCount, e.Output = "", nil, json.RawMessage(`null`)
	if !Compare(e, []ObservedStep{{Output: json.RawMessage(`null`)}}).Passed || Compare(e, []ObservedStep{{}}).Passed {
		t.Fatal("explicit null became an omitted output check")
	}
	if result := Compare(e, nil); !reflect.DeepEqual(result.Mismatches, []string{"step"}) {
		t.Fatal("missing target passed")
	}
	if result := Compare(e, []ObservedStep{{}, {}}); !reflect.DeepEqual(result.Mismatches, []string{"step"}) {
		t.Fatal("ambiguous target passed")
	}
}
