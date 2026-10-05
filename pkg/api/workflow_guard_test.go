package api

import (
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestWorkflowGuardEvaluation(t *testing.T) {
	input := json.RawMessage(`{"n":9007199254740993,"decimal":1.00,"null":null,"text":"1","active":true,"items":[{"amount":12}],"object":{}}`)
	outputs := map[string]json.RawMessage{"lookup.customer": json.RawMessage(`{"body":{"tier":"pro"}}`), "lookup": json.RawMessage(`{"customer":{"output":{"body":{"tier":"wrong"}}}}`)}
	for _, test := range []struct {
		name, predicate string
		want            bool
	}{
		{"exact integer", `{"ref":"input.n","op":"gt","value":9007199254740992}`, true},
		{"decimal equality", `{"ref":"input.decimal","op":"eq","value":1e0}`, true},
		{"numeric type", `{"ref":"input.text","op":"gte","value":1}`, false},
		{"equality type", `{"ref":"input.text","op":"eq","value":1}`, false},
		{"null exists", `{"ref":"input.null","op":"exists","value":true}`, true},
		{"null equal", `{"ref":"input.null","op":"eq","value":null}`, true},
		{"missing exists", `{"ref":"input.missing","op":"exists","value":false}`, true},
		{"missing unequal", `{"ref":"input.missing","op":"ne","value":null}`, false},
		{"array path", `{"ref":"input.items.0.amount","op":"lte","value":12}`, true},
		{"array bounds", `{"ref":"input.items.1.amount","op":"eq","value":12}`, false},
		{"object scalar", `{"ref":"input.object","op":"eq","value":"x"}`, false},
		{"dotted step", `{"ref":"steps.lookup.customer.output.body.tier","op":"eq","value":"pro"}`, true},
		{"all", `{"all":[{"ref":"input.active","op":"eq","value":true},{"ref":"input.n","op":"gt","value":10}]}`, true},
		{"any", `{"any":[{"ref":"input.active","op":"eq","value":false},{"ref":"input.n","op":"gt","value":10}]}`, true},
		{"not", `{"not":{"ref":"input.active","op":"eq","value":true}}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var guard WorkflowGuardSpec
			if err := json.Unmarshal([]byte(test.predicate), &guard); err != nil {
				t.Fatal(err)
			}
			got, err := EvaluateWorkflowGuard(&guard, input, outputs)
			if err != nil || got != test.want {
				t.Fatalf("matched=%v want=%v err=%v", got, test.want, err)
			}
		})
	}
}

func TestWorkflowGuardRejectsInvalidDeclarations(t *testing.T) {
	for _, predicate := range []string{
		`{}`, `null`, `[]`, `{"all":[]}`, `{"not":null}`,
		`{"ref":"input.x","op":"eq"}`,
		`{"ref":"input.x","op":"eq","value":1,"script":"return true"}`,
		`{"all":[{"ref":"input.x","op":"eq","value":1}],"ref":""}`,
		`{"ref":"steps.foreign.output.id","op":"eq","value":1}`,
		`{"ref":"failure.message","op":"eq","value":"x"}`,
		`{"ref":"input..x","op":"eq","value":1}`,
		`{"ref":"input.x","op":"regex","value":".*"}`,
		`{"ref":"input.x","op":"gt","value":"1"}`,
		`{"ref":"input.x","op":"exists","value":null}`,
		`{"ref":"input.x","op":"eq","value":{}}`,
		`{"ref":"input.x","op":"eq","value":1e1000000000}`,
	} {
		t.Run(predicate, func(t *testing.T) {
			var guard WorkflowGuardSpec
			if err := json.Unmarshal([]byte(predicate), &guard); err != nil {
				return
			}
			if err := ValidateWorkflowGuard(&guard, []string{"lookup"}); err == nil {
				t.Fatal("accepted invalid guard")
			}
		})
	}
	leaf := `{"ref":"input.x","op":"eq","value":1}`
	tooDeep := strings.Repeat(`{"not":`, WorkflowGuardMaxDepth) + leaf + strings.Repeat("}", WorkflowGuardMaxDepth)
	tooMany := `{"all":[` + strings.Repeat(leaf+",", WorkflowGuardMaxNodes-1) + leaf + `]}`
	tooLarge := `{"ref":"input.x","op":"eq","value":"` + strings.Repeat("x", WorkflowGuardMaxBytes) + `"}`
	for _, raw := range []string{tooDeep, tooMany, tooLarge} {
		var guard WorkflowGuardSpec
		if err := json.Unmarshal([]byte(raw), &guard); err == nil {
			t.Fatal("accepted guard exceeding resource bounds")
		}
	}
	var guard WorkflowGuardSpec
	if err := json.Unmarshal([]byte(leaf), &guard); err != nil {
		t.Fatal(err)
	}
	if _, err := EvaluateWorkflowGuard(&guard, json.RawMessage(`{"x":1e1000000000}`), nil); err == nil {
		t.Fatal("unbounded runtime exponent accepted")
	}
}

func TestWorkflowGuardWireValidationAndRoundTrip(t *testing.T) {
	raw := `{"name":"guarded","steps":[{"name":"lookup","run":"lookup"},{"name":"send","run":"send","depends_on":["lookup"],"when":{"ref":"steps.lookup.output.active","op":"eq","value":true}}]}`
	var spec WorkflowSpec
	if err := json.Unmarshal([]byte(raw), &spec); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateWorkflowDAG(spec, PlanHobby); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(spec)
	if err != nil || !strings.Contains(string(encoded), `"when"`) {
		t.Fatalf("guard lost on round trip: %s %v", encoded, err)
	}
	var yamlSpec WorkflowSpec
	if err := yaml.Unmarshal([]byte("name: guarded\nsteps:\n  - name: send\n    run: send\n    when:\n      ref: input.active\n      op: eq\n      value: true\n"), &yamlSpec); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateWorkflowDAG(yamlSpec, PlanHobby); err != nil {
		t.Fatal(err)
	}
	spec.Steps[1].DependsOn = nil
	if _, err := ValidateWorkflowDAG(spec, PlanHobby); err == nil {
		t.Fatal("guard accessed an undeclared dependency")
	}
	spec.Steps[1].When = yamlSpec.Steps[0].When
	spec.Steps[0].OnFailure = "send"
	if _, err := ValidateWorkflowDAG(spec, PlanHobby); err == nil {
		t.Fatal("guard allowed on exception handler")
	}
}
