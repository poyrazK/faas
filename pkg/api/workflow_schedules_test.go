package api

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestWorkflowScheduleValidation(t *testing.T) {
	for _, test := range []struct {
		name    string
		trigger WorkflowTriggerSpec
		invalid bool
	}{
		{"manual", WorkflowTriggerSpec{Type: "manual"}, false},
		{"schedule", WorkflowTriggerSpec{Type: "schedule", Schedule: "0 7 * * *", Timezone: "Europe/Istanbul"}, false},
		{"tenant-configurable schedule", WorkflowTriggerSpec{Type: "schedule", Schedule: "0 7 * * *", TenantConfigurable: true}, false},
		{"tenant-configurable manual", WorkflowTriggerSpec{Type: "manual", TenantConfigurable: true}, true},
		{"tenant-configurable event", WorkflowTriggerSpec{Type: "event", Source: "billing.*", EventType: "paid", TenantConfigurable: true}, true},
		{"unknown", WorkflowTriggerSpec{Type: "event"}, true},
		{"missing schedule", WorkflowTriggerSpec{Type: "schedule"}, true},
		{"seconds", WorkflowTriggerSpec{Type: "schedule", Schedule: "* * * * * *"}, true},
		{"impossible date", WorkflowTriggerSpec{Type: "schedule", Schedule: "0 0 30 2 *"}, true},
		{"bad timezone", WorkflowTriggerSpec{Type: "schedule", Schedule: "* * * * *", Timezone: "Missing/Zone"}, true},
		{"host local timezone", WorkflowTriggerSpec{Type: "schedule", Schedule: "* * * * *", Timezone: "Local"}, true},
		{"bad overlap", WorkflowTriggerSpec{Type: "schedule", Schedule: "* * * * *", Overlap: "replace"}, true},
		{"bad input", WorkflowTriggerSpec{Type: "schedule", Schedule: "* * * * *", Input: json.RawMessage(`{`)}, true},
		{"oversized input", WorkflowTriggerSpec{Type: "schedule", Schedule: "* * * * *", Input: json.RawMessage(`"` + strings.Repeat("a", int(WorkflowRunInputMaxBytes)) + `"`)}, true},
		{"manual options", WorkflowTriggerSpec{Type: "manual", Schedule: "* * * * *"}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := ValidateWorkflowDAG(WorkflowSpec{Name: "nightly", Trigger: &test.trigger,
				Steps: []WorkflowStepSpec{{Name: "main", Run: "report"}}}, PlanHobby)
			if test.invalid != errors.Is(err, ErrWorkflowInvalidTrigger) {
				t.Fatalf("validation error = %v, invalid=%t", err, test.invalid)
			}
		})
	}
}

func TestWorkflowScheduleYAMLAndStrictJSON(t *testing.T) {
	var definition WorkflowSpec
	if err := yaml.Unmarshal([]byte(`name: nightly
trigger:
  type: schedule
  schedule: '0 7 * * *'
  timezone: Europe/Istanbul
  input:
    report: daily
    count: 7
steps:
  - name: report
    run: generate
`), &definition); err != nil {
		t.Fatal(err)
	}
	if string(definition.Trigger.Input) != `{"count":7,"report":"daily"}` {
		t.Fatalf("scheduled input = %s", definition.Trigger.Input)
	}
	for _, raw := range []string{`{"type":"schedule","schedule":"* * * * *","typo":true}`, `{"type":"manual","input":{"secret":true},"typo":1}`} {
		var trigger WorkflowTriggerSpec
		if json.Unmarshal([]byte(raw), &trigger) == nil {
			t.Fatalf("accepted unknown option: %s", raw)
		}
	}
}
