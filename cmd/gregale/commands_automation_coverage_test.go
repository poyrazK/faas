package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestAutomationCoverageOutcomes(t *testing.T) {
	definition := api.WorkflowSpec{Steps: []api.WorkflowStepSpec{
		{Name: "guarded", When: &api.WorkflowGuardSpec{Ref: "input.approved", Op: "exists", Value: json.RawMessage(`true`)}},
		{Name: "action", OnFailure: "recover", Retry: &api.WorkflowRetrySpec{MaxAttempts: 3}},
		{Name: "recover", Path: "/recover"},
		{Name: "approval", WaitForEvent: "approved", Timeout: time.Hour, OnTimeout: "expire"},
		{Name: "callback", WaitForCallback: true},
		{Name: "timer", WaitForDuration: time.Minute},
		{Name: "single", Retry: &api.WorkflowRetrySpec{MaxAttempts: 1}},
	}}
	coverage := automationScenarioCoverage{}
	var codes []string
	for _, hint := range coverage.hints(definition) {
		codes = append(codes, hint.Step+":"+hint.Code)
	}
	want := []string{"action:failure_route_missing", "action:retry_missing", "approval:wait_success_missing", "approval:wait_timeout_missing", "callback:wait_success_missing", "guarded:guard_match_missing", "guarded:guard_skip_missing"}
	if !reflect.DeepEqual(codes, want) {
		t.Fatalf("hints=%v want=%v", codes, want)
	}
	yes, no := true, false
	coverage.observe(api.SimulateAutomationResponse{Trace: []api.AutomationSimulationStep{
		{StepName: "guarded", State: "mocked", WhenMatched: &yes},
		{StepName: "action", State: "dead", Attempts: []api.AutomationSimulationAttempt{{Attempt: 1, Outcome: "failure"}, {Attempt: 2, Outcome: "failure"}}},
		{StepName: "recover", State: "mocked"},
		{StepName: "approval", State: "mocked", Reason: "event_received_mocked"},
		{StepName: "callback", State: "mocked", Reason: "callback_received_mocked"},
	}})
	coverage.observe(api.SimulateAutomationResponse{Trace: []api.AutomationSimulationStep{
		{StepName: "guarded", State: "skipped", WhenMatched: &no},
		{StepName: "approval", State: "timed_out", Reason: "timeout_mocked"},
	}})
	if hints := coverage.hints(definition); len(hints) != 0 {
		t.Fatalf("covered suite has hints: %+v", hints)
	}
}

func TestAutomationCoverageDoesNotCountUnexercisedRoutes(t *testing.T) {
	definition := api.WorkflowSpec{Steps: []api.WorkflowStepSpec{{Name: "action", OnFailure: "recover", Retry: &api.WorkflowRetrySpec{MaxAttempts: 2}}, {Name: "recover"}, {Name: "wait", WaitForCallback: true, Timeout: time.Hour, OnTimeout: "expire"}}}
	coverage := automationScenarioCoverage{}
	index := 0
	coverage.observe(api.SimulateAutomationResponse{Trace: []api.AutomationSimulationStep{
		{StepName: "action", State: "would_retry", Attempts: []api.AutomationSimulationAttempt{{Attempt: 1, Outcome: "failure"}}},
		{StepName: "recover", State: "mocked"},
		{StepName: "wait", State: "would_wait"},
		{StepName: "wait", ParentStep: "loop", ItemIndex: &index, State: "timed_out", Reason: "timeout_mocked"},
	}})
	if hints := coverage.hints(definition); len(hints) != 4 {
		t.Fatalf("unresolved or nested trace counted: %+v", hints)
	}
	coverage.observe(api.SimulateAutomationResponse{Trace: []api.AutomationSimulationStep{{StepName: "action", State: "failed"}, {StepName: "recover", State: "skipped"}}})
	if hints := coverage.hints(definition); len(hints) != 4 {
		t.Fatalf("skipped handler counted: %+v", hints)
	}
}

func TestAutomationCoverageRunnerAdvisory(t *testing.T) {
	for _, mode := range []string{"pass", "assertion_failure", "invalid", "request_error"} {
		t.Run(mode, func(t *testing.T) {
			yes := true
			definition := api.WorkflowSpec{Name: "flow", Steps: []api.WorkflowStepSpec{{Name: "guarded", Path: "/send", When: &api.WorkflowGuardSpec{Ref: "input.ok", Op: "exists", Value: json.RawMessage(`true`)}}}}
			response := api.SimulateAutomationResponse{DefinitionValid: mode != "invalid", Complete: true, Trace: []api.AutomationSimulationStep{{StepName: "guarded", State: "mocked", WhenMatched: &yes, Output: json.RawMessage(`{"token":"private-value"}`)}}}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || !strings.HasSuffix(r.URL.Path, ":simulate") {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				if mode == "request_error" {
					w.WriteHeader(503)
					_, _ = w.Write([]byte(`{}`))
					return
				}
				_ = json.NewEncoder(w).Encode(response)
			}))
			defer server.Close()
			expected := "mocked"
			if mode == "assertion_failure" {
				expected = "skipped"
			}
			scenario := loadedAutomationScenario{request: api.SimulateAutomationRequest{Definition: definition}, scenario: automationScenario{Name: "sample", Expect: map[string]automationStepExpectation{"guarded": {State: expected}}}}
			report := runAutomationScenarios(context.Background(), api.NewClient(server.URL, "token"), "billing", []loadedAutomationScenario{scenario})
			wantHints := 2
			if mode == "pass" {
				wantHints = 1
			}
			if report.Passed != (mode == "pass") || len(report.CoverageHints) != wantHints {
				t.Fatalf("report=%+v", report)
			}
			for _, jsonMode := range []bool{false, true} {
				resetJSONOut(t)
				jsonOutput = jsonMode
				output := captureAutomationStdout(t)
				code := printAutomationCheckReport(report)
				if (code == 0) != report.Passed {
					t.Fatalf("hints changed exit status: %d", code)
				}
				if strings.Contains(output.String(), "private-value") {
					t.Fatal("report leaked output")
				}
				if jsonMode {
					var parsed automationCheckReport
					if err := json.Unmarshal(output.Bytes(), &parsed); err != nil || !reflect.DeepEqual(parsed.CoverageHints, report.CoverageHints) {
						t.Fatalf("parsed=%+v err=%v", parsed, err)
					}
				} else if !strings.Contains(output.String(), "Coverage hint:") {
					t.Fatal("missing human coverage hint")
				}
			}
		})
	}
}

func TestAutomationCoverageLoops(t *testing.T) {
	yes, no := true, false
	zero, one, two := 0, 1, 2
	definition := api.WorkflowSpec{Steps: []api.WorkflowStepSpec{{Name: "sync", ForEach: &api.WorkflowForEachSpec{Items: "input.contacts", Action: api.WorkflowForEachActionSpec{Path: "/update", When: &api.WorkflowGuardSpec{Ref: "input.item.enabled", Op: "exists", Value: json.RawMessage(`true`)}, Retry: &api.WorkflowRetrySpec{MaxAttempts: 2}}}}}}
	root := func(count *int, state string) api.AutomationSimulationStep {
		return api.AutomationSimulationStep{StepName: "sync", State: state, ItemCount: count}
	}
	item := func(parent string, index *int, state string, matched *bool) api.AutomationSimulationStep {
		return api.AutomationSimulationStep{StepName: "internal-name", ParentStep: parent, ItemIndex: index, State: state, WhenMatched: matched}
	}
	for _, tc := range []struct {
		name   string
		traces [][]api.AutomationSimulationStep
		want   []string
	}{
		{"unexercised", nil, []string{"sync:loop_empty_missing", "sync:loop_multiple_items_missing", "sync/action:guard_match_missing", "sync/action:guard_skip_missing", "sync/action:retry_missing"}},
		{"empty", [][]api.AutomationSimulationStep{{root(&zero, "resolved")}}, []string{"sync:loop_multiple_items_missing", "sync/action:guard_match_missing", "sync/action:guard_skip_missing", "sync/action:retry_missing"}},
		{"single", [][]api.AutomationSimulationStep{{root(&one, "resolved"), item("sync", &zero, "mocked", &yes)}}, []string{"sync:loop_empty_missing", "sync:loop_multiple_items_missing", "sync/action:guard_skip_missing", "sync/action:retry_missing"}},
		{"mixed_items", [][]api.AutomationSimulationStep{{root(&two, "resolved"), item("sync", &zero, "mocked", &yes), item("sync", &one, "skipped", &no)}}, []string{"sync:loop_empty_missing", "sync/action:retry_missing"}},
		{"combined_scenarios", [][]api.AutomationSimulationStep{{root(&zero, "resolved")}, {root(&two, "resolved"), item("sync", &zero, "mocked", &yes), item("sync", &one, "skipped", &no)}}, []string{"sync/action:retry_missing"}},
		{"unrelated_items", [][]api.AutomationSimulationStep{{root(&one, "resolved"), item("other", &zero, "mocked", &yes), item("other", &zero, "skipped", &no), item("sync", &two, "mocked", &yes)}}, []string{"sync:loop_empty_missing", "sync:loop_multiple_items_missing", "sync/action:guard_match_missing", "sync/action:guard_skip_missing", "sync/action:retry_missing"}},
		{"skipped_parent", [][]api.AutomationSimulationStep{{root(&two, "skipped"), item("sync", &zero, "mocked", &yes)}}, []string{"sync:loop_empty_missing", "sync:loop_multiple_items_missing", "sync/action:guard_match_missing", "sync/action:guard_skip_missing", "sync/action:retry_missing"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			coverage := automationScenarioCoverage{}
			for _, trace := range tc.traces {
				coverage.observe(api.SimulateAutomationResponse{Trace: trace})
			}
			var codes []string
			for _, hint := range coverage.hints(definition) {
				codes = append(codes, hint.Step+":"+hint.Code)
			}
			if !reflect.DeepEqual(codes, tc.want) {
				t.Fatalf("hints=%v want=%v", codes, tc.want)
			}
		})
	}
	coverage := automationScenarioCoverage{}
	action := item("sync", &zero, "mocked", &yes)
	action.Attempts = []api.AutomationSimulationAttempt{{Attempt: 1, Outcome: "failure"}, {Attempt: 2, Outcome: "success"}}
	coverage.observe(api.SimulateAutomationResponse{Trace: []api.AutomationSimulationStep{root(&zero, "resolved")}})
	coverage.observe(api.SimulateAutomationResponse{Trace: []api.AutomationSimulationStep{root(&two, "resolved"), action, item("sync", &one, "skipped", &no)}})
	if hints := coverage.hints(definition); len(hints) != 0 {
		t.Fatalf("observed retries not counted: %+v", hints)
	}
}

func TestAutomationCoverageLoopRunner(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "passing", true: "failing"}[failed], func(t *testing.T) {
			zero, two := 0, 2
			definition := api.WorkflowSpec{Name: "flow", Steps: []api.WorkflowStepSpec{{Name: "sync", ForEach: &api.WorkflowForEachSpec{Items: "input.items", Action: api.WorkflowForEachActionSpec{Path: "/send"}}}}}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request api.SimulateAutomationRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				count := &zero
				if string(request.Input) == `{"items":[1,2]}` {
					count = &two
				}
				state := "resolved"
				if failed {
					state = "skipped"
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(api.SimulateAutomationResponse{DefinitionValid: true, Complete: true, Trace: []api.AutomationSimulationStep{{StepName: "sync", State: state, ItemCount: count, Output: json.RawMessage(`{"secret":"private-loop-value"}`)}}})
			}))
			defer server.Close()
			scenarios := []loadedAutomationScenario{}
			for _, input := range []string{`{"items":[]}`, `{"items":[1,2]}`} {
				scenarios = append(scenarios, loadedAutomationScenario{request: api.SimulateAutomationRequest{Definition: definition, Input: json.RawMessage(input)}, scenario: automationScenario{Name: input, Expect: map[string]automationStepExpectation{"sync": {State: "resolved"}}}})
			}
			report := runAutomationScenarios(context.Background(), api.NewClient(server.URL, "token"), "billing", scenarios)
			if report.Passed == failed || len(report.CoverageHints) != map[bool]int{false: 0, true: 2}[failed] {
				t.Fatalf("report=%+v", report)
			}
			for _, jsonMode := range []bool{false, true} {
				resetJSONOut(t)
				jsonOutput = jsonMode
				output := captureAutomationStdout(t)
				if code := printAutomationCheckReport(report); (code == 0) == failed {
					t.Fatalf("exit=%d", code)
				}
				if strings.Contains(output.String(), "private-loop-value") {
					t.Fatal("leaked loop output")
				}
				if jsonMode {
					var decoded automationCheckReport
					if err := json.Unmarshal(output.Bytes(), &decoded); err != nil || !reflect.DeepEqual(decoded.CoverageHints, report.CoverageHints) {
						t.Fatalf("decoded=%+v err=%v", decoded, err)
					}
				}
			}
		})
	}
}
