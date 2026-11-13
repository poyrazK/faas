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

func TestStoredWorkflowCoverageOutcomes(t *testing.T) {
	definition := api.WorkflowSpec{Steps: []api.WorkflowStepSpec{
		{Name: "guarded", When: &api.WorkflowGuardSpec{Ref: "input.approved", Op: "exists", Value: json.RawMessage(`true`)}},
		{Name: "action", OnFailure: "recover", Retry: &api.WorkflowRetrySpec{MaxAttempts: 3}},
		{Name: "recover", Path: "/recover"},
		{Name: "approval", WaitForEvent: "approved", Timeout: time.Hour, OnTimeout: "expire"},
		{Name: "callback", WaitForCallback: true},
		{Name: "timer", WaitForDuration: time.Minute},
		{Name: "single", Retry: &api.WorkflowRetrySpec{MaxAttempts: 1}},
	}}
	coverage := storedWorkflowScenarioCoverage{}
	var codes []string
	for _, hint := range coverage.hints(definition) {
		codes = append(codes, hint.Step+":"+hint.Code)
	}
	want := []string{"action:failure_route_missing", "action:retry_missing", "approval:wait_success_missing", "approval:wait_timeout_missing", "callback:wait_success_missing", "guarded:guard_match_missing", "guarded:guard_skip_missing"}
	if !reflect.DeepEqual(codes, want) {
		t.Fatalf("hints=%v want=%v", codes, want)
	}
	yes, no := true, false
	coverage.observe(api.SimulateStoredWorkflowResponse{Trace: []api.StoredWorkflowSimulationStep{
		{StepName: "guarded", State: "mocked", WhenMatched: &yes},
		{StepName: "action", State: "dead", Attempts: []api.StoredWorkflowSimulationAttempt{{Attempt: 1, Outcome: "failure"}, {Attempt: 2, Outcome: "failure"}}},
		{StepName: "recover", State: "mocked"},
		{StepName: "approval", State: "mocked", Reason: "event_received_mocked"},
		{StepName: "callback", State: "mocked", Reason: "callback_received_mocked"},
	}})
	coverage.observe(api.SimulateStoredWorkflowResponse{Trace: []api.StoredWorkflowSimulationStep{
		{StepName: "guarded", State: "skipped", WhenMatched: &no},
		{StepName: "approval", State: "timed_out", Reason: "timeout_mocked"},
	}})
	if hints := coverage.hints(definition); len(hints) != 0 {
		t.Fatalf("covered suite has hints: %+v", hints)
	}
}

func TestStoredWorkflowCoverageDoesNotCountUnexercisedRoutes(t *testing.T) {
	definition := api.WorkflowSpec{Steps: []api.WorkflowStepSpec{{Name: "action", OnFailure: "recover", Retry: &api.WorkflowRetrySpec{MaxAttempts: 2}}, {Name: "recover"}, {Name: "wait", WaitForCallback: true, Timeout: time.Hour, OnTimeout: "expire"}}}
	coverage := storedWorkflowScenarioCoverage{}
	index := 0
	coverage.observe(api.SimulateStoredWorkflowResponse{Trace: []api.StoredWorkflowSimulationStep{
		{StepName: "action", State: "would_retry", Attempts: []api.StoredWorkflowSimulationAttempt{{Attempt: 1, Outcome: "failure"}}},
		{StepName: "recover", State: "mocked"},
		{StepName: "wait", State: "would_wait"},
		{StepName: "wait", ParentStep: "loop", ItemIndex: &index, State: "timed_out", Reason: "timeout_mocked"},
	}})
	if hints := coverage.hints(definition); len(hints) != 4 {
		t.Fatalf("unresolved or nested trace counted: %+v", hints)
	}
	coverage.observe(api.SimulateStoredWorkflowResponse{Trace: []api.StoredWorkflowSimulationStep{{StepName: "action", State: "failed"}, {StepName: "recover", State: "skipped"}}})
	if hints := coverage.hints(definition); len(hints) != 4 {
		t.Fatalf("skipped handler counted: %+v", hints)
	}
}

func TestStoredWorkflowCoverageRunnerAdvisory(t *testing.T) {
	for _, mode := range []string{"pass", "assertion_failure", "invalid", "request_error"} {
		t.Run(mode, func(t *testing.T) {
			yes := true
			definition := api.WorkflowSpec{Name: "flow", Steps: []api.WorkflowStepSpec{{Name: "guarded", Path: "/send", When: &api.WorkflowGuardSpec{Ref: "input.ok", Op: "exists", Value: json.RawMessage(`true`)}}}}
			response := api.SimulateStoredWorkflowResponse{DefinitionValid: mode != "invalid", Complete: true, Trace: []api.StoredWorkflowSimulationStep{{StepName: "guarded", State: "mocked", WhenMatched: &yes, Output: json.RawMessage(`{"token":"private-value"}`)}}}
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
			scenario := loadedStoredWorkflowScenario{request: api.SimulateStoredWorkflowRequest{Definition: definition}, scenario: storedWorkflowScenario{Name: "sample", Expect: map[string]storedWorkflowStepExpectation{"guarded": {State: expected}}}}
			report := runStoredWorkflowScenarios(context.Background(), api.NewClient(server.URL, "token"), "billing", []loadedStoredWorkflowScenario{scenario})
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
				output := captureStoredWorkflowStdout(t)
				code := printStoredWorkflowCheckReport(report)
				if (code == 0) != report.Passed {
					t.Fatalf("hints changed exit status: %d", code)
				}
				if strings.Contains(output.String(), "private-value") {
					t.Fatal("report leaked output")
				}
				if jsonMode {
					var parsed storedWorkflowCheckReport
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

func TestStoredWorkflowCoverageLoops(t *testing.T) {
	yes, no := true, false
	zero, one, two := 0, 1, 2
	definition := api.WorkflowSpec{Steps: []api.WorkflowStepSpec{{Name: "sync", ForEach: &api.WorkflowForEachSpec{Items: "input.contacts", Action: api.WorkflowForEachActionSpec{Path: "/update", When: &api.WorkflowGuardSpec{Ref: "input.item.enabled", Op: "exists", Value: json.RawMessage(`true`)}, Retry: &api.WorkflowRetrySpec{MaxAttempts: 2}}}}}}
	root := func(count *int, state string) api.StoredWorkflowSimulationStep {
		return api.StoredWorkflowSimulationStep{StepName: "sync", State: state, ItemCount: count}
	}
	item := func(parent string, index *int, state string, matched *bool) api.StoredWorkflowSimulationStep {
		return api.StoredWorkflowSimulationStep{StepName: "internal-name", ParentStep: parent, ItemIndex: index, State: state, WhenMatched: matched}
	}
	for _, tc := range []struct {
		name   string
		traces [][]api.StoredWorkflowSimulationStep
		want   []string
	}{
		{"unexercised", nil, []string{"sync:loop_empty_missing", "sync:loop_multiple_items_missing", "sync/action:guard_match_missing", "sync/action:guard_skip_missing", "sync/action:retry_missing"}},
		{"empty", [][]api.StoredWorkflowSimulationStep{{root(&zero, "resolved")}}, []string{"sync:loop_multiple_items_missing", "sync/action:guard_match_missing", "sync/action:guard_skip_missing", "sync/action:retry_missing"}},
		{"single", [][]api.StoredWorkflowSimulationStep{{root(&one, "resolved"), item("sync", &zero, "mocked", &yes)}}, []string{"sync:loop_empty_missing", "sync:loop_multiple_items_missing", "sync/action:guard_skip_missing", "sync/action:retry_missing"}},
		{"mixed_items", [][]api.StoredWorkflowSimulationStep{{root(&two, "resolved"), item("sync", &zero, "mocked", &yes), item("sync", &one, "skipped", &no)}}, []string{"sync:loop_empty_missing", "sync/action:retry_missing"}},
		{"combined_scenarios", [][]api.StoredWorkflowSimulationStep{{root(&zero, "resolved")}, {root(&two, "resolved"), item("sync", &zero, "mocked", &yes), item("sync", &one, "skipped", &no)}}, []string{"sync/action:retry_missing"}},
		{"unrelated_items", [][]api.StoredWorkflowSimulationStep{{root(&one, "resolved"), item("other", &zero, "mocked", &yes), item("other", &zero, "skipped", &no), item("sync", &two, "mocked", &yes)}}, []string{"sync:loop_empty_missing", "sync:loop_multiple_items_missing", "sync/action:guard_match_missing", "sync/action:guard_skip_missing", "sync/action:retry_missing"}},
		{"skipped_parent", [][]api.StoredWorkflowSimulationStep{{root(&two, "skipped"), item("sync", &zero, "mocked", &yes)}}, []string{"sync:loop_empty_missing", "sync:loop_multiple_items_missing", "sync/action:guard_match_missing", "sync/action:guard_skip_missing", "sync/action:retry_missing"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			coverage := storedWorkflowScenarioCoverage{}
			for _, trace := range tc.traces {
				coverage.observe(api.SimulateStoredWorkflowResponse{Trace: trace})
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
	coverage := storedWorkflowScenarioCoverage{}
	action := item("sync", &zero, "mocked", &yes)
	action.Attempts = []api.StoredWorkflowSimulationAttempt{{Attempt: 1, Outcome: "failure"}, {Attempt: 2, Outcome: "success"}}
	coverage.observe(api.SimulateStoredWorkflowResponse{Trace: []api.StoredWorkflowSimulationStep{root(&zero, "resolved")}})
	coverage.observe(api.SimulateStoredWorkflowResponse{Trace: []api.StoredWorkflowSimulationStep{root(&two, "resolved"), action, item("sync", &one, "skipped", &no)}})
	if hints := coverage.hints(definition); len(hints) != 0 {
		t.Fatalf("observed retries not counted: %+v", hints)
	}
}

func TestStoredWorkflowCoverageLoopRunner(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "passing", true: "failing"}[failed], func(t *testing.T) {
			zero, two := 0, 2
			definition := api.WorkflowSpec{Name: "flow", Steps: []api.WorkflowStepSpec{{Name: "sync", ForEach: &api.WorkflowForEachSpec{Items: "input.items", Action: api.WorkflowForEachActionSpec{Path: "/send"}}}}}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request api.SimulateStoredWorkflowRequest
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
				_ = json.NewEncoder(w).Encode(api.SimulateStoredWorkflowResponse{DefinitionValid: true, Complete: true, Trace: []api.StoredWorkflowSimulationStep{{StepName: "sync", State: state, ItemCount: count, Output: json.RawMessage(`{"secret":"private-loop-value"}`)}}})
			}))
			defer server.Close()
			scenarios := []loadedStoredWorkflowScenario{}
			for _, input := range []string{`{"items":[]}`, `{"items":[1,2]}`} {
				scenarios = append(scenarios, loadedStoredWorkflowScenario{request: api.SimulateStoredWorkflowRequest{Definition: definition, Input: json.RawMessage(input)}, scenario: storedWorkflowScenario{Name: input, Expect: map[string]storedWorkflowStepExpectation{"sync": {State: "resolved"}}}})
			}
			report := runStoredWorkflowScenarios(context.Background(), api.NewClient(server.URL, "token"), "billing", scenarios)
			if report.Passed == failed || len(report.CoverageHints) != map[bool]int{false: 0, true: 2}[failed] {
				t.Fatalf("report=%+v", report)
			}
			for _, jsonMode := range []bool{false, true} {
				resetJSONOut(t)
				jsonOutput = jsonMode
				output := captureStoredWorkflowStdout(t)
				if code := printStoredWorkflowCheckReport(report); (code == 0) == failed {
					t.Fatalf("exit=%d", code)
				}
				if strings.Contains(output.String(), "private-loop-value") {
					t.Fatal("leaked loop output")
				}
				if jsonMode {
					var decoded storedWorkflowCheckReport
					if err := json.Unmarshal(output.Bytes(), &decoded); err != nil || !reflect.DeepEqual(decoded.CoverageHints, report.CoverageHints) {
						t.Fatalf("decoded=%+v err=%v", decoded, err)
					}
				}
			}
		})
	}
}
