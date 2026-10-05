package api

import (
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestWorkflowForEachWireValidationAndInputMapping(t *testing.T) {
	raw := `{"name":"batch","steps":[{"name":"lookup","run":"lookup"},{"name":"send","depends_on":["lookup"],"for_each":{"items":"steps.lookup.output.items","action":{"run":"send","timeout":"30s","input":{"record":"{{input.item}}","index":"{{input.index}}","customer":"{{input.input.customer}}"},"retry":{"max_attempts":2}}}}]}`
	var spec WorkflowSpec
	if err := json.Unmarshal([]byte(raw), &spec); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateWorkflowDAG(spec, PlanHobby); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(spec)
	if err != nil || !strings.Contains(string(encoded), `"timeout":"30s"`) {
		t.Fatalf("duration wire format: %s %v", encoded, err)
	}
	yamlBytes, err := yaml.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	var decoded WorkflowSpec
	if err := yaml.Unmarshal(yamlBytes, &decoded); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateWorkflowDAG(decoded, PlanHobby); err != nil {
		t.Fatal(err)
	}
	items, inputs, err := ResolveWorkflowForEachInputs(spec.Steps[1], json.RawMessage(`{"customer":"c-1"}`), map[string]json.RawMessage{"lookup": json.RawMessage(`{"items":[{"n":9007199254740993,"literal":"{{input.secret}}"},null]}`)})
	if err != nil || len(inputs) != 2 || !strings.Contains(string(items), "9007199254740993") {
		t.Fatalf("snapshot: %s %s %v", items, inputs, err)
	}
	want := `{"customer":"c-1","index":0,"record":{"literal":"{{input.secret}}","n":9007199254740993}}`
	if string(inputs[0]) != want || string(inputs[1]) != `{"customer":"c-1","index":1,"record":null}` {
		t.Fatalf("typed mapping or literal changed: %s", inputs)
	}
	parent, index, ok := WorkflowForEachItemIdentity(WorkflowForEachItemName("send", 127))
	if !ok || parent != "send" || index != 127 {
		t.Fatal("item identity did not roundtrip")
	}
	for _, name := range []string{"_foreach.c2VuZA.01", "_foreach.c2VuZA.128", "_foreach.c2VuZA.-1", "_foreach..0", "_foreach.c2VuZA==.0"} {
		if _, _, ok := WorkflowForEachItemIdentity(name); ok {
			t.Fatalf("accepted noncanonical item name %q", name)
		}
	}
}

func TestWorkflowForEachRejectsUnsupportedDefinitions(t *testing.T) {
	for _, fragment := range []string{
		`{"items":"input.items","unknown":true,"action":{"run":"send"}}`,
		`{"items":"input.items","action":{"run":"send","parallelism":2}}`,
		`{"items":"input.items","action":{"run":"send","for_each":{}}}`,
		`{"items":"input.items","action":{"run":"send","when":{}}}`,
		`{"items":"input.items","action":{"run":"send","on_failure":"fallback"}}`,
		`{"items":"input.items","action":{"run":"send","depends_on":["lookup"]}}`,
	} {
		var spec WorkflowSpec
		if err := json.Unmarshal([]byte(`{"name":"test","steps":[{"name":"batch","for_each":`+fragment+`}]}`), &spec); err == nil {
			t.Fatalf("accepted unknown/nested action: %s", fragment)
		}
	}
	for _, fragment := range []string{
		`{"items":"input.items","action":{}}`,
		`{"items":"input.items","action":{"run":"send","path":"/send"}}`,
		`{"items":"{{input.items}}","action":{"run":"send"}}`,
		`{"items":"failure.items","action":{"run":"send"}}`,
		`{"items":"steps.other.output.items","action":{"run":"send"}}`,
		`{"items":"input.items","action":{"run":"send","input":"{{steps.other.output}}"}}`,
		`{"items":"input.items","action":{"run":"send","input":"{{failure}}"}}`,
		`{"items":"input.items","action":{"run":"send","timeout":"24h"}}`,
	} {
		var spec WorkflowSpec
		if err := json.Unmarshal([]byte(`{"name":"test","steps":[{"name":"batch","for_each":`+fragment+`}]}`), &spec); err != nil {
			t.Fatal(err)
		}
		if _, err := ValidateWorkflowDAG(spec, PlanHobby); err == nil {
			t.Fatalf("accepted invalid action: %s", fragment)
		}
	}
	for _, options := range []string{`"run":"send",`, `"input":null,`, `"timeout":"1s",`, `"retry":{"max_attempts":1},`, `"on_timeout":"batch",`} {
		var spec WorkflowSpec
		if err := json.Unmarshal([]byte(`{"name":"test","steps":[{"name":"batch",`+options+`"for_each":{"items":"input.items","action":{"run":"send"}}}]}`), &spec); err != nil {
			t.Fatal(err)
		}
		if _, err := ValidateWorkflowDAG(spec, PlanHobby); err == nil {
			t.Fatal("accepted ambiguous parent options")
		}
	}
}

func TestWorkflowForEachInputBoundsAndEmptyList(t *testing.T) {
	step := WorkflowStepSpec{Name: "batch", ForEach: &WorkflowForEachSpec{Items: "input.items", Action: WorkflowForEachActionSpec{Run: "send"}}}
	for _, raw := range []string{`{"items":null}`, `{"items":{}}`, `{"items":1}`, `{"items":[` + strings.Repeat("0,", WorkflowForEachMaxItems) + `0]}`, `{"items":["` + strings.Repeat("x", int(WorkflowForEachMaxInputBytes)) + `"]}`} {
		if _, _, err := ResolveWorkflowForEachInputs(step, json.RawMessage(raw), nil); err == nil {
			t.Fatal("accepted non-array or oversized source")
		}
	}
	if _, items, err := ResolveWorkflowForEachInputs(step, json.RawMessage(`{"items":[]}`), nil); err != nil || len(items) != 0 {
		t.Fatalf("empty batch: %v %v", items, err)
	}
	step.ForEach.Action.Input = json.RawMessage(`"{{input.input.large}}"`)
	raw := `{"items":[0,1,2],"large":"` + strings.Repeat("x", int(WorkflowForEachMaxInputBytes)/2) + `"}`
	if _, _, err := ResolveWorkflowForEachInputs(step, json.RawMessage(raw), nil); err == nil {
		t.Fatal("accepted oversized prepared inputs")
	}
}
