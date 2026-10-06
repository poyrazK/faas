package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestWorkflowJoinJSONYAMLAndValidation(t *testing.T) {
	raw := `{"name":"merged","steps":[{"name":"a","run":"a"},{"name":"b","run":"b"},{"name":"merge","depends_on":["a","b"],"join":{"output_from":["b","a"]}}]}`
	var spec WorkflowSpec
	if err := json.Unmarshal([]byte(raw), &spec); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateWorkflowDAG(spec, PlanHobby); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(spec)
	if err != nil || !strings.Contains(string(encoded), `"join":{"output_from":["b","a"]}`) {
		t.Fatalf("join lost in JSON: %s %v", encoded, err)
	}
	yamlBytes, err := yaml.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	var decoded WorkflowSpec
	if err := yaml.Unmarshal(yamlBytes, &decoded); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateWorkflowDAG(decoded, PlanHobby); err != nil || decoded.Steps[2].Join.OutputFrom[0] != "b" {
		t.Fatalf("join lost in YAML: %+v %v", decoded, err)
	}
	for _, invalid := range []string{`{"unknown":true}`, `{"output_from":"a"}`, `{"output_from":["a","b"],"mode":"first"}`} {
		var step WorkflowStepSpec
		if err := json.Unmarshal([]byte(`{"name":"merge","join":`+invalid+`}`), &step); err == nil {
			t.Fatalf("accepted invalid join JSON: %s", invalid)
		}
	}
	if err := yaml.Unmarshal([]byte("name: merge\njoin:\n  output_from: [a, b]\n  unknown: true\n"), &WorkflowStepSpec{}); err == nil {
		t.Fatal("accepted unknown nested YAML join field")
	}
}

func TestWorkflowJoinRejectsAmbiguousOptionsAndOutputs(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*WorkflowStepSpec)
	}{
		{"missing priority", func(s *WorkflowStepSpec) { s.Join.OutputFrom = nil }},
		{"missing branch", func(s *WorkflowStepSpec) { s.Join.OutputFrom = []string{"a"} }},
		{"duplicate", func(s *WorkflowStepSpec) { s.Join.OutputFrom = []string{"a", "a"} }},
		{"unknown", func(s *WorkflowStepSpec) { s.Join.OutputFrom = []string{"a", "other"} }},
		{"single branch", func(s *WorkflowStepSpec) { s.DependsOn = []string{"a"}; s.Join.OutputFrom = []string{"a"} }},
		{"mixed target", func(s *WorkflowStepSpec) { s.Run = "merge" }},
		{"input", func(s *WorkflowStepSpec) { s.Input = json.RawMessage("null") }},
		{"method", func(s *WorkflowStepSpec) { s.Method = "POST" }},
		{"guard", func(s *WorkflowStepSpec) {
			s.When = &WorkflowGuardSpec{Ref: "input.x", Op: "exists", Value: json.RawMessage("true")}
		}},
		{"timeout", func(s *WorkflowStepSpec) { s.Timeout = time.Second }},
		{"retry", func(s *WorkflowStepSpec) { s.Retry = &WorkflowRetrySpec{MaxAttempts: 1} }},
		{"failure route", func(s *WorkflowStepSpec) { s.OnFailure = "a" }},
		{"timeout route", func(s *WorkflowStepSpec) { s.OnTimeout = "a" }},
		{"too many", func(s *WorkflowStepSpec) {
			s.DependsOn = make([]string, WorkflowJoinMaxDependencies+1)
			s.Join.OutputFrom = append([]string(nil), s.DependsOn...)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			join := WorkflowStepSpec{Name: "merge", DependsOn: []string{"a", "b"}, Join: &WorkflowJoinSpec{OutputFrom: []string{"a", "b"}}}
			test.edit(&join)
			spec := WorkflowSpec{Name: "joined", Steps: []WorkflowStepSpec{{Name: "a", Run: "a"}, {Name: "b", Run: "b"}, join}}
			if _, err := ValidateWorkflowDAG(spec, PlanHobby); err == nil {
				t.Fatal("accepted invalid join")
			}
		})
	}
	for _, target := range []string{"a", "merge"} {
		spec := WorkflowSpec{Name: "exceptions", Steps: []WorkflowStepSpec{
			{Name: "event", WaitForEvent: "approval", Timeout: time.Minute, OnTimeout: target},
			{Name: "a", Run: "a"}, {Name: "b", Run: "b"},
			{Name: "merge", DependsOn: []string{"a", "b"}, Join: &WorkflowJoinSpec{OutputFrom: []string{"a", "b"}}},
		}}
		if _, err := ValidateWorkflowDAG(spec, PlanHobby); err == nil {
			t.Fatal("join accepted timeout handler routing")
		}
	}
}
