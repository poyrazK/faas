// adr: 20261027090010
package api

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func agentWorkflow(step WorkflowStepSpec) WorkflowSpec {
	return WorkflowSpec{Name: "triage", Steps: []WorkflowStepSpec{{Name: "fetch", Run: "fetch"}, step}}
}

func TestWorkflowAgentStepValidation(t *testing.T) {
	ok := WorkflowStepSpec{Name: "review", Agent: "pr-triage", DependsOn: []string{"fetch"},
		Input: json.RawMessage(`{"inputs":{"pr":"{{steps.fetch.output.number}}"}}`), Timeout: 30 * time.Minute}
	if _, err := ValidateWorkflowDAG(agentWorkflow(ok), PlanPro); err != nil {
		t.Fatalf("a valid agent step: %v", err)
	}
	for name, mutate := range map[string]func(*WorkflowStepSpec){
		"bad name":       func(s *WorkflowStepSpec) { s.Agent = "PR Triage" },
		"also a run":     func(s *WorkflowStepSpec) { s.Run = "other" },
		"retry":          func(s *WorkflowStepSpec) { s.Retry = &WorkflowRetrySpec{MaxAttempts: 2} },
		"method":         func(s *WorkflowStepSpec) { s.Method = "GET" },
		"short timeout":  func(s *WorkflowStepSpec) { s.Timeout = time.Second },
		"long timeout":   func(s *WorkflowStepSpec) { s.Timeout = 48 * time.Hour },
		"failure route":  func(s *WorkflowStepSpec) { s.OnFailure = "fetch" },
		"timeout route":  func(s *WorkflowStepSpec) { s.OnTimeout = "fetch" },
		"managed op":     func(s *WorkflowStepSpec) { s.ManagedOperation = true },
		"waits as well":  func(s *WorkflowStepSpec) { s.WaitForEvent = "x" },
		"input not json": func(s *WorkflowStepSpec) { s.Input = json.RawMessage(`{`) },
	} {
		step := ok
		mutate(&step)
		if _, err := ValidateWorkflowDAG(agentWorkflow(step), PlanPro); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if !WorkflowUsesAgentSteps(agentWorkflow(ok)) || WorkflowUsesAgentSteps(agentWorkflow(WorkflowStepSpec{Name: "x", Run: "x"})) {
		t.Fatal("WorkflowUsesAgentSteps")
	}
	if WorkflowAgentStepTimeout(WorkflowStepSpec{}) != WorkflowAgentStepDefaultTimeout || WorkflowAgentStepTimeout(ok) != 30*time.Minute {
		t.Fatal("agent step timeout")
	}
}

func TestWorkflowAgentStepWireShapes(t *testing.T) {
	var fromJSON WorkflowStepSpec
	if err := json.Unmarshal([]byte(`{"name":"review","agent":"pr-triage","timeout":"30m"}`), &fromJSON); err != nil || fromJSON.Agent != "pr-triage" {
		t.Fatalf("json %+v %v", fromJSON, err)
	}
	var fromYAML WorkflowStepSpec
	if err := yaml.Unmarshal([]byte("name: review\nagent: pr-triage\n"), &fromYAML); err != nil || fromYAML.Agent != "pr-triage" {
		t.Fatalf("yaml %+v %v", fromYAML, err)
	}
	out, err := json.Marshal(fromJSON)
	if err != nil || !strings.Contains(string(out), `"agent":"pr-triage"`) {
		t.Fatalf("marshal %s %v", out, err)
	}
	plain, _ := json.Marshal(WorkflowStepSpec{Name: "x", Run: "x"})
	if strings.Contains(string(plain), "agent") {
		t.Fatalf("a step without an agent mentions it: %s", plain)
	}
}

func TestWorkflowAgentEventsAreReserved(t *testing.T) {
	first, second := WorkflowAgentEventName("r", "s", 1), WorkflowAgentEventName("r", "s", 2)
	if first == second || first != WorkflowAgentEventName("r", "s", 1) || !IsWorkflowReservedEventName(first) || IsWorkflowCallbackEventName(first) {
		t.Fatal(first, second)
	}
	if !IsWorkflowReservedEventName(WorkflowCallbackEventName("r", "s")) || IsWorkflowReservedEventName("deploy.done") {
		t.Fatal("reserved names")
	}
	step := WorkflowStepSpec{Name: "wait", WaitForEvent: first, Timeout: time.Hour}
	if _, err := ValidateWorkflowDAG(WorkflowSpec{Name: "w", Steps: []WorkflowStepSpec{step}}, PlanPro); !errors.Is(err, ErrWorkflowReservedEventName) {
		t.Fatalf("waiting on an agent event: %v", err)
	}
}

func TestWorkflowAgentRunInputValidate(t *testing.T) {
	commit := strings.Repeat("A", 40)
	in := WorkflowAgentRunInput{Inputs: json.RawMessage(`{"pr":7}`), Checkout: &AgentCheckout{Repository: "acme/app", Commit: commit},
		GitHub: &AgentGitHubTarget{Repository: "acme/app", Number: 7}}
	if err := in.Validate(); err != nil || in.Checkout.Commit != strings.ToLower(commit) {
		t.Fatalf("valid input %v %+v", err, in.Checkout)
	}
	prompt := strings.Repeat("p", AgentRunPromptMaxBytes+1)
	for name, bad := range map[string]WorkflowAgentRunInput{
		"array inputs":  {Inputs: json.RawMessage(`[1]`)},
		"nul inputs":    {Inputs: json.RawMessage(`{"a":"\u0000"}`)},
		"long prompt":   {Prompt: &prompt},
		"short commit":  {Checkout: &AgentCheckout{Repository: "acme/app", Commit: "abc"}},
		"path repo":     {Checkout: &AgentCheckout{Repository: "acme/../x", Commit: commit}},
		"no issue":      {GitHub: &AgentGitHubTarget{Repository: "acme/app"}},
		"negative vers": {Version: -1},
	} {
		if err := bad.Validate(); !errors.Is(err, ErrWorkflowAgentStepInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
