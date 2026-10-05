package api

import (
	"encoding/json"
	"errors"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestWorkflowEventTriggerValidation(t *testing.T) {
	for _, raw := range []string{
		`{"type":"event","event_type":"invoice.paid"}`,
		`{"type":"event","source":"billing","event_type":"in*voice"}`,
		`{"type":"event","source":"billing","event_type":"paid","filter":[]}`,
		`{"type":"event","source":"billing","event_type":"paid","filter":{"data":{"$oops":1}}}`,
		`{"type":"event","source":"billing","event_type":"paid","input":{}}`,
		`{"type":"schedule","schedule":"* * * * *","source":"billing"}`,
		`{"type":"manual","filter":{}}`,
	} {
		var trigger WorkflowTriggerSpec
		if err := json.Unmarshal([]byte(raw), &trigger); err != nil {
			t.Fatal(err)
		}
		if err := ValidateWorkflowTrigger(&trigger); !errors.Is(err, ErrWorkflowInvalidTrigger) {
			t.Fatalf("accepted %s: %v", raw, err)
		}
	}
	var spec WorkflowSpec
	if err := yaml.Unmarshal([]byte(`name: invoice
trigger:
  type: event
  source: billing.*
  event_type: invoice.paid
  filter:
    data:
      amount:
        $gt: 100
steps:
  - name: send
    path: /send
`), &spec); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateWorkflowDAG(spec, PlanHobby); err != nil {
		t.Fatal(err)
	}
	if string(spec.Trigger.Filter) != `{"data":{"amount":{"$gt":100}}}` {
		t.Fatalf("filter=%s", spec.Trigger.Filter)
	}
}
