package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func simulateTest(t *testing.T, request api.SimulateAutomationRequest) api.SimulateAutomationResponse {
	t.Helper()
	response, err := SimulateAutomation(context.Background(), request, api.PlanHobby)
	if err != nil || !response.DefinitionValid {
		t.Fatalf("simulation: %+v %v", response, err)
	}
	return response
}

func simulationStep(t *testing.T, response api.SimulateAutomationResponse, name string) api.AutomationSimulationStep {
	t.Helper()
	for _, row := range response.Trace {
		if row.StepName == name {
			return row
		}
	}
	t.Fatalf("missing trace step %q", name)
	return api.AutomationSimulationStep{}
}

func TestAutomationSimulationBranchJoinMatchesRuntime(t *testing.T) {
	request := api.SimulateAutomationRequest{Definition: joinTestSpec(), Input: json.RawMessage(`{"active":false}`), MockOutputs: map[string]json.RawMessage{"a": json.RawMessage(`{"n":9007199254740993,"literal":"{{input.secret}}"}`), "b": json.RawMessage(`null`)}}
	request.Definition.Steps[3].Input = json.RawMessage(`{"value":"{{steps.merge.output.value}}"}`)
	before, _ := json.Marshal(request)
	response := simulateTest(t, request)
	if response.Complete || len(response.Warnings) != 1 || len(response.DefinitionHash) != 64 {
		t.Fatalf("unexpected completeness or warnings: %+v", response)
	}
	b := simulationStep(t, response, "b")
	if b.State != "skipped" || b.Reason != WorkflowSkipWhenFalse || b.WhenMatched == nil || *b.WhenMatched {
		t.Fatalf("guard: %+v", b)
	}
	merge := simulationStep(t, response, "merge")
	if merge.State != "resolved" || string(merge.Output) != `{"source":"a","value":{"n":9007199254740993,"literal":"{{input.secret}}"}}` {
		t.Fatalf("join: %+v", merge)
	}
	next := simulationStep(t, response, "next")
	if next.State != "would_execute" || string(next.Input) != `{"value":{"literal":"{{input.secret}}","n":9007199254740993}}` || next.Path != "/next" || next.Method != "POST" {
		t.Fatalf("mapping: %+v", next)
	}
	store := NewMemStore()
	run := seedGuardRunWithSpec(t, store, request.Definition, string(request.Input))
	ctx := context.Background()
	if err := store.MarkWorkflowStepStatus(ctx, run.ID, "a", WorkflowStepStatusSucceeded, 1, request.MockOutputs["a"], nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ResolveWorkflowStepGuard(ctx, run.ID, "b"); err != nil {
		t.Fatal(err)
	}
	if ready, err := store.ResolveWorkflowStepJoin(ctx, run.ID, "merge"); err != nil || !ready {
		t.Fatalf("runtime join: %v %v", ready, err)
	}
	if !equalWorkflowJSON(guardStep(t, store, run.ID, "merge").Output, merge.Output) {
		t.Fatal("simulation diverged from runtime")
	}
	second := simulateTest(t, request)
	firstRaw, _ := json.Marshal(response)
	secondRaw, _ := json.Marshal(second)
	if string(firstRaw) != string(secondRaw) {
		t.Fatal("trace is not deterministic")
	}
	merge.Output[0] = 'x'
	next.Input[0] = 'x'
	after, _ := json.Marshal(request)
	if string(before) != string(after) {
		t.Fatal("simulation mutated caller data")
	}
}

func TestAutomationSimulationMissingOutputsAndWaits(t *testing.T) {
	spec := api.WorkflowSpec{Name: "waits", Steps: []api.WorkflowStepSpec{
		{Name: "a", Run: "lookup"},
		{Name: "next", Run: "next", DependsOn: []string{"a"}},
		{Name: "event", WaitForEvent: "paid", Timeout: time.Minute, OnTimeout: "fallback"},
		{Name: "fallback", Run: "fallback"},
		{Name: "callback", WaitForCallback: true, Timeout: time.Minute},
		{Name: "delay", WaitForDuration: time.Second},
		{Name: "condition", WaitForCondition: &api.WorkflowConditionSpec{Run: "check", Interval: time.Minute, MaxAttempts: 3}, Timeout: time.Minute},
	}}
	response := simulateTest(t, api.SimulateAutomationRequest{Definition: spec})
	if response.Complete {
		t.Fatal("assumed missing outputs")
	}
	if row := simulationStep(t, response, "next"); row.State != "blocked" || strings.Join(row.BlockedBy, ",") != "a" {
		t.Fatalf("dependent action: %+v", row)
	}
	for _, name := range []string{"event", "callback", "delay", "condition"} {
		if row := simulationStep(t, response, name); row.State != "would_wait" {
			t.Fatalf("wait executed: %+v", row)
		}
	}
	if row := simulationStep(t, response, "fallback"); row.State != "blocked" || row.Reason != "exception_outcome_missing" {
		t.Fatalf("invented timeout: %+v", row)
	}
}

func TestAutomationSimulationFailureRouteOrderingAndNull(t *testing.T) {
	spec := api.WorkflowSpec{Name: "routes", Steps: []api.WorkflowStepSpec{
		{Name: "z_source", Run: "lookup", OnFailure: "a_handler"},
		{Name: "a_handler", Run: "recover", Input: json.RawMessage(`"{{failure}}"`)},
		{Name: "next", Run: "next", DependsOn: []string{"z_source"}, Input: json.RawMessage(`"{{steps.z_source.output}}"`)},
	}}
	request := api.SimulateAutomationRequest{Definition: spec, MockOutputs: map[string]json.RawMessage{"z_source": json.RawMessage(`null`), "next": json.RawMessage(`false`)}}
	response := simulateTest(t, request)
	if !response.Complete || len(response.Issues) != 0 {
		t.Fatalf("successful null: %+v", response)
	}
	if row := simulationStep(t, response, "a_handler"); row.Reason != WorkflowSkipRouteNotTaken {
		t.Fatalf("exception ordering: %+v", row)
	}
	if row := simulationStep(t, response, "next"); string(row.Input) != "null" || row.State != "mocked" {
		t.Fatalf("null lost: %+v", row)
	}
	delete(request.MockOutputs, "z_source")
	response = simulateTest(t, request)
	if row := simulationStep(t, response, "a_handler"); row.State != "blocked" {
		t.Fatalf("missing result activated failure handler: %+v", row)
	}
}

func TestAutomationSimulationRetriesThenSucceeds(t *testing.T) {
	status := 503
	spec := api.WorkflowSpec{Name: "retry", Steps: []api.WorkflowStepSpec{
		{Name: "source", Run: "lookup", Retry: &api.WorkflowRetrySpec{MaxAttempts: 3}},
		{Name: "handler", Run: "recover", OnFailure: ""},
	}}
	// Route the recovery step from the source after it has become terminally
	// failed in the second half of this test; successful retries skip it.
	spec.Steps[0].OnFailure = "handler"
	response := simulateTest(t, api.SimulateAutomationRequest{Definition: spec, MockAttempts: map[string][]api.AutomationSimulationMockAttempt{
		"source": {{Outcome: "failure", HTTPStatus: &status}, {Outcome: "success", Output: json.RawMessage(`{"ok":true}`)}},
	}, MockOutputs: map[string]json.RawMessage{"handler": json.RawMessage(`null`)}})
	source := simulationStep(t, response, "source")
	if !response.Complete || source.State != "mocked" || len(source.Attempts) != 2 || source.Attempts[0].Outcome != "failure" || source.Attempts[1].Outcome != "success" {
		t.Fatalf("retry success trace: %+v", response)
	}
	if handler := simulationStep(t, response, "handler"); handler.State != "skipped" || handler.Reason != WorkflowSkipRouteNotTaken {
		t.Fatalf("successful retry activated recovery: %+v", handler)
	}
}

func TestAutomationSimulationTerminalFailureRoutesFailureContext(t *testing.T) {
	failureStatus := 503
	terminalStatus := 400
	spec := api.WorkflowSpec{Name: "recover", Steps: []api.WorkflowStepSpec{
		{Name: "source", Run: "charge", Retry: &api.WorkflowRetrySpec{MaxAttempts: 3}, OnFailure: "handler"},
		{Name: "handler", Run: "recover", Input: json.RawMessage(`"{{failure}}"`)},
		{Name: "dependent", Run: "notify", DependsOn: []string{"source"}},
	}}
	response := simulateTest(t, api.SimulateAutomationRequest{Definition: spec, MockAttempts: map[string][]api.AutomationSimulationMockAttempt{
		"source": {{Outcome: "failure", HTTPStatus: &failureStatus}, {Outcome: "failure", HTTPStatus: &terminalStatus}},
	}, MockOutputs: map[string]json.RawMessage{"handler": json.RawMessage(`{"recovered":true}`)}})
	source := simulationStep(t, response, "source")
	if !response.Complete || source.State != WorkflowStepStatusFailed || source.Reason != "non_retryable_failure" || len(source.Attempts) != 2 {
		t.Fatalf("terminal failure trace: %+v", response)
	}
	handler := simulationStep(t, response, "handler")
	wantFailure := `{"step":"source","status":"failed","attempt":2,"message":"HTTP 400"}`
	if handler.State != "mocked" || !equalWorkflowJSON(handler.Input, json.RawMessage(wantFailure)) {
		t.Fatalf("failure context was not resolved: %+v", handler)
	}
	if dependent := simulationStep(t, response, "dependent"); dependent.State != "skipped" || dependent.Reason != WorkflowSkipDependencyFailed {
		t.Fatalf("failure did not close dependent branch: %+v", dependent)
	}
}

func TestAutomationSimulationWaitTimeoutRoutesHandler(t *testing.T) {
	spec := api.WorkflowSpec{Name: "timeout", Steps: []api.WorkflowStepSpec{
		{Name: "event", WaitForEvent: "paid", Timeout: time.Minute, OnTimeout: "fallback"},
		{Name: "fallback", Run: "notify"},
	}}
	response := simulateTest(t, api.SimulateAutomationRequest{Definition: spec,
		MockAttempts: map[string][]api.AutomationSimulationMockAttempt{"event": {{Outcome: "timeout"}}},
		MockOutputs:  map[string]json.RawMessage{"fallback": json.RawMessage(`{"sent":true}`)},
	})
	event := simulationStep(t, response, "event")
	if !response.Complete || event.State != "timed_out" || string(event.Output) != `{"timeout":true}` {
		t.Fatalf("timeout trace: %+v", response)
	}
	if fallback := simulationStep(t, response, "fallback"); fallback.State != "mocked" {
		t.Fatalf("timeout route was not activated: %+v", fallback)
	}
}

func TestAutomationSimulationIncompleteRetryWaitsForNextMock(t *testing.T) {
	status := 503
	spec := api.WorkflowSpec{Name: "retry", Steps: []api.WorkflowStepSpec{
		{Name: "source", Run: "lookup", Retry: &api.WorkflowRetrySpec{MaxAttempts: 2}, OnFailure: "handler"},
		{Name: "handler", Run: "recover"},
	}}
	response := simulateTest(t, api.SimulateAutomationRequest{Definition: spec, MockAttempts: map[string][]api.AutomationSimulationMockAttempt{
		"source": {{Outcome: "failure", HTTPStatus: &status}},
	}})
	if response.Complete {
		t.Fatal("missing retry outcome produced a complete trace")
	}
	if source := simulationStep(t, response, "source"); source.State != "would_retry" {
		t.Fatalf("retry was not left unresolved: %+v", source)
	}
	if handler := simulationStep(t, response, "handler"); handler.State != "blocked" || handler.Reason != "exception_outcome_missing" {
		t.Fatalf("incomplete retry triggered failure handler: %+v", handler)
	}
}

func TestAutomationSimulationMatchesOutboundRetrySafety(t *testing.T) {
	const integrationID = "00000000-0000-0000-0000-000000000001"
	status := 429
	safe := api.WorkflowSpec{Name: "safe", Steps: []api.WorkflowStepSpec{{
		Name: "read", Outbound: &api.WorkflowOutboundSpec{IntegrationID: integrationID, Method: "GET", Path: "/v1/items"},
		Retry: &api.WorkflowRetrySpec{MaxAttempts: 2},
	}}}
	response := simulateTest(t, api.SimulateAutomationRequest{Definition: safe, MockAttempts: map[string][]api.AutomationSimulationMockAttempt{
		"read": {{Outcome: "failure", HTTPStatus: &status}, {Outcome: "success", Output: json.RawMessage(`[]`)}},
	}})
	if !response.Complete || simulationStep(t, response, "read").State != "mocked" || len(simulationStep(t, response, "read").Attempts) != 2 {
		t.Fatalf("safe outbound did not retry a transient response: %+v", response)
	}
	unsafe := api.WorkflowSpec{Name: "unsafe", Steps: []api.WorkflowStepSpec{{
		Name: "write", Outbound: &api.WorkflowOutboundSpec{IntegrationID: integrationID, Method: "POST", Path: "/v1/items"},
	}}}
	response = simulateTest(t, api.SimulateAutomationRequest{Definition: unsafe, MockAttempts: map[string][]api.AutomationSimulationMockAttempt{
		"write": {{Outcome: "failure", HTTPStatus: &status}},
	}})
	write := simulationStep(t, response, "write")
	if !response.Complete || write.State != WorkflowStepStatusFailed || write.Reason != "non_retryable_failure" || len(write.Attempts) != 1 {
		t.Fatalf("unsafe outbound incorrectly retried a transient response: %+v", response)
	}
}

func TestAutomationSimulationTransportErrorIsDead(t *testing.T) {
	spec := api.WorkflowSpec{Name: "transport-error", Steps: []api.WorkflowStepSpec{{Name: "source", Run: "lookup", Retry: &api.WorkflowRetrySpec{MaxAttempts: 1}}}}
	response := simulateTest(t, api.SimulateAutomationRequest{Definition: spec, MockAttempts: map[string][]api.AutomationSimulationMockAttempt{
		"source": {{Outcome: "failure", Error: "connection reset"}},
	}})
	if !response.Complete {
		t.Fatalf("transport error did not produce a terminal trace: %+v", response)
	}
	if source := simulationStep(t, response, "source"); source.State != WorkflowStepStatusDead || source.Reason != "retry_limit_reached" {
		t.Fatalf("transport error did not match scheduler terminal status: %+v", source)
	}
}

func TestAutomationSimulationEvaluationErrorAndInactiveDescendants(t *testing.T) {
	spec := api.WorkflowSpec{Name: "errors", Steps: []api.WorkflowStepSpec{
		{Name: "bad", Run: "read", Input: json.RawMessage(`"{{input.missing}}"`), OnFailure: "handler"},
		{Name: "handler", Run: "recover"},
		{Name: "child", Run: "child", DependsOn: []string{"bad"}},
		{Name: "inactive", Run: "inactive", When: &api.WorkflowGuardSpec{Ref: "input.active", Op: "eq", Value: json.RawMessage(`true`)}},
		{Name: "skip", Run: "skip", DependsOn: []string{"inactive"}},
	}}
	response := simulateTest(t, api.SimulateAutomationRequest{Definition: spec, Input: json.RawMessage(`{"active":false,"secret":"private"}`)})
	for name, want := range map[string]string{"bad": "input_resolution_failed", "child": WorkflowSkipDependencyFailed, "handler": "exception_outcome_missing", "skip": WorkflowSkipDependencySkipped} {
		if row := simulationStep(t, response, name); row.Reason != want {
			t.Fatalf("%s: %+v", name, row)
		}
	}
	if len(response.Issues) != 1 || strings.Contains(strings.Join(response.Issues, ""), "private") {
		t.Fatalf("unsafe diagnostics: %+v", response)
	}
}

func TestAutomationSimulationRejectsReservedTimeoutOutcome(t *testing.T) {
	spec := api.WorkflowSpec{Name: "timeout-route", Steps: []api.WorkflowStepSpec{
		{Name: "source", Run: "source", OnTimeout: "handler"},
		{Name: "handler", Run: "handler"},
	}}
	for _, raw := range []string{`{"timeout":true}`, ` { "timeout" : true } `} {
		_, err := SimulateAutomation(context.Background(), api.SimulateAutomationRequest{Definition: spec, MockOutputs: map[string]json.RawMessage{"source": json.RawMessage(raw)}}, api.PlanHobby)
		if !errors.Is(err, ErrAutomationSimulationInvalid) {
			t.Fatalf("reserved timeout outcome accepted: %v", err)
		}
	}
	for _, raw := range []string{`{"timeout":false}`, `{"timeout":true,"data":null}`, `{"body":{"timeout":true}}`, `null`} {
		response := simulateTest(t, api.SimulateAutomationRequest{Definition: spec, MockOutputs: map[string]json.RawMessage{"source": json.RawMessage(raw)}})
		if !response.Complete || simulationStep(t, response, "handler").Reason != WorkflowSkipRouteNotTaken {
			t.Fatalf("ordinary output misclassified: %+v", response)
		}
	}
}

func simulationLoopSpec() api.WorkflowSpec {
	return api.WorkflowSpec{Name: "batch", Steps: []api.WorkflowStepSpec{
		{Name: "batch", ForEach: &api.WorkflowForEachSpec{Items: "input.items", Action: api.WorkflowForEachActionSpec{Run: "send", Input: json.RawMessage(`{"item":"{{input.item}}","index":"{{input.index}}"}`)}}},
		{Name: "finish", Run: "finish", DependsOn: []string{"batch"}, Input: json.RawMessage(`"{{steps.batch.output}}"`)},
	}}
}

func TestAutomationSimulationSequentialLoop(t *testing.T) {
	for _, count := range []int{0, 1, 3} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			mocks := []json.RawMessage{json.RawMessage(`{"n":9007199254740993}`), json.RawMessage(`null`), json.RawMessage(`true`)}[:count]
			request := api.SimulateAutomationRequest{Definition: simulationLoopSpec(), Input: json.RawMessage(`{"items":["a",null,"{{input.secret}}"]}`), MockItemOutputs: map[string][]json.RawMessage{"batch": mocks}}
			response := simulateTest(t, request)
			batch := simulationStep(t, response, "batch")
			if batch.ItemCount == nil || *batch.ItemCount != 3 || len(response.Trace) != 5 {
				t.Fatalf("loop expansion: %+v", response)
			}
			for index := range 3 {
				row := simulationStep(t, response, api.WorkflowForEachItemName("batch", index))
				want := "blocked"
				if index < count {
					want = "mocked"
				} else if index == count {
					want = "would_execute"
				}
				if row.State != want || row.ParentStep != "batch" || row.ItemIndex == nil || *row.ItemIndex != index {
					t.Fatalf("sequential item: %+v", row)
				}
			}
			finish := simulationStep(t, response, "finish")
			if count == 3 {
				if batch.State != "resolved" || string(finish.Input) != `[{"n":9007199254740993},null,true]` {
					t.Fatalf("aggregate: %+v %+v", batch, finish)
				}
			} else if finish.State != "blocked" {
				t.Fatalf("premature continuation: %+v", finish)
			}
		})
	}
	response := simulateTest(t, api.SimulateAutomationRequest{Definition: simulationLoopSpec(), Input: json.RawMessage(`{"items":[]}`), MockOutputs: map[string]json.RawMessage{"finish": json.RawMessage(`null`)}})
	if !response.Complete || string(simulationStep(t, response, "batch").Output) != "[]" {
		t.Fatalf("empty loop: %+v", response)
	}
}

func TestAutomationSimulationForEachGuardsPreserveItemPositions(t *testing.T) {
	spec := simulationLoopSpec()
	spec.Steps[0].ForEach.OnItemFailure = "continue"
	spec.Steps[0].ForEach.Action.When = &api.WorkflowGuardSpec{Ref: "input.item.active", Op: "eq", Value: json.RawMessage("true")}
	request := api.SimulateAutomationRequest{
		Definition:      spec,
		Input:           json.RawMessage(`{"items":[{"id":"skip","active":false},{"id":"send","active":true},{"id":"skip-too","active":false}]}`),
		MockItemOutputs: map[string][]json.RawMessage{"batch": {json.RawMessage(`null`), json.RawMessage(`"sent"`), json.RawMessage(`null`)}},
		MockOutputs:     map[string]json.RawMessage{"finish": json.RawMessage(`null`)},
	}
	response := simulateTest(t, request)
	batch := simulationStep(t, response, "batch")
	if !response.Complete || batch.State != "resolved" || string(batch.Output) != `[null,"sent",null]` {
		t.Fatalf("guarded simulation did not preserve positional results: %+v", response)
	}
	for index, matched := range []bool{false, true, false} {
		item := simulationStep(t, response, api.WorkflowForEachItemName("batch", index))
		if item.WhenMatched == nil || *item.WhenMatched != matched {
			t.Fatalf("item %d guard trace: %+v", index, item)
		}
		if !matched && (item.State != "skipped" || item.Reason != WorkflowSkipWhenFalse) {
			t.Fatalf("item %d skip trace: %+v", index, item)
		}
	}
}

func TestAutomationSimulationInvalidSamplesAndDefinitions(t *testing.T) {
	base := api.WorkflowSpec{Name: "test", Steps: []api.WorkflowStepSpec{{Name: "a", Run: "a"}}}
	for name, request := range map[string]api.SimulateAutomationRequest{
		"unknown":                  {Definition: base, MockOutputs: map[string]json.RawMessage{"missing": json.RawMessage(`null`)}},
		"invalid JSON":             {Definition: base, Input: json.RawMessage(`{`)},
		"empty output":             {Definition: base, MockOutputs: map[string]json.RawMessage{"a": nil}},
		"control mock":             {Definition: simulationLoopSpec(), MockOutputs: map[string]json.RawMessage{"batch": json.RawMessage(`[]`)}},
		"wrong item parent":        {Definition: base, MockItemOutputs: map[string][]json.RawMessage{"a": {json.RawMessage(`null`)}}},
		"excess item samples":      {Definition: simulationLoopSpec(), Input: json.RawMessage(`{"items":[]}`), MockItemOutputs: map[string][]json.RawMessage{"batch": {json.RawMessage(`null`)}}},
		"attempts on control":      {Definition: simulationLoopSpec(), MockAttempts: map[string][]api.AutomationSimulationMockAttempt{"batch": {{Outcome: "timeout"}}}},
		"empty attempts":           {Definition: base, MockAttempts: map[string][]api.AutomationSimulationMockAttempt{"a": {}}},
		"output after success":     {Definition: base, MockAttempts: map[string][]api.AutomationSimulationMockAttempt{"a": {{Outcome: "success", Output: json.RawMessage(`null`)}, {Outcome: "success", Output: json.RawMessage(`null`)}}}},
		"conflicting mock formats": {Definition: base, MockOutputs: map[string]json.RawMessage{"a": json.RawMessage(`null`)}, MockAttempts: map[string][]api.AutomationSimulationMockAttempt{"a": {{Outcome: "success", Output: json.RawMessage(`null`)}}}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := SimulateAutomation(context.Background(), request, api.PlanHobby); !errors.Is(err, ErrAutomationSimulationInvalid) {
				t.Fatalf("invalid sample accepted: %v", err)
			}
		})
	}
	for _, plan := range []api.Plan{api.PlanFree, api.PlanHobby} {
		spec := base
		if plan == api.PlanHobby {
			spec.Name = ""
		}
		response, err := SimulateAutomation(context.Background(), api.SimulateAutomationRequest{Definition: spec}, plan)
		if err != nil || response.DefinitionValid || response.Complete || len(response.Trace) != 0 || len(response.Issues) == 0 {
			t.Fatalf("invalid definition: %+v %v", response, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := SimulateAutomation(ctx, api.SimulateAutomationRequest{Definition: base}, api.PlanHobby); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestAutomationSimulationBudgets(t *testing.T) {
	base := api.WorkflowSpec{Name: "test", Steps: []api.WorkflowStepSpec{{Name: "a", Run: "a"}}}
	large, _ := json.Marshal(strings.Repeat("x", int(api.WorkflowRunInputMaxBytes)/2+10))
	for name, build := range map[string]func() api.SimulateAutomationRequest{
		"input": func() api.SimulateAutomationRequest {
			return api.SimulateAutomationRequest{Definition: base, Input: json.RawMessage(`"` + strings.Repeat("x", int(api.WorkflowRunInputMaxBytes)) + `"`)}
		},
		"steps": func() api.SimulateAutomationRequest {
			spec := base
			spec.Steps = make([]api.WorkflowStepSpec, api.AutomationSimulationMaxSteps+1)
			return api.SimulateAutomationRequest{Definition: spec}
		},
		"template expansion": func() api.SimulateAutomationRequest {
			spec := base
			spec.Steps = []api.WorkflowStepSpec{{Name: "a", Run: "a", Input: json.RawMessage(`["{{input}}","{{input}}"]`)}}
			return api.SimulateAutomationRequest{Definition: spec, Input: large}
		},
		"loop inputs": func() api.SimulateAutomationRequest {
			spec := simulationLoopSpec()
			spec.Steps[0].ForEach.Action.Input = json.RawMessage(`"{{input.input.large}}"`)
			raw, _ := json.Marshal(map[string]any{"items": []int{0, 1, 2}, "large": strings.Repeat("x", int(api.WorkflowRunInputMaxBytes)/2)})
			return api.SimulateAutomationRequest{Definition: spec, Input: raw}
		},
		"loop item count": func() api.SimulateAutomationRequest {
			return api.SimulateAutomationRequest{Definition: simulationLoopSpec(), Input: json.RawMessage(`{"items":[` + strings.Repeat(`0,`, api.WorkflowForEachMaxItems) + `0]}`)}
		},
		"loop outputs": func() api.SimulateAutomationRequest {
			return api.SimulateAutomationRequest{Definition: simulationLoopSpec(), Input: json.RawMessage(`{"items":[0,1]}`), MockItemOutputs: map[string][]json.RawMessage{"batch": {large, large}}}
		},
		"trace entries": func() api.SimulateAutomationRequest {
			spec := api.WorkflowSpec{Name: "many"}
			for index := range 9 {
				spec.Steps = append(spec.Steps, api.WorkflowStepSpec{Name: fmt.Sprintf("batch%d", index), ForEach: &api.WorkflowForEachSpec{Items: "input.items", Action: api.WorkflowForEachActionSpec{Run: "send"}}})
			}
			return api.SimulateAutomationRequest{Definition: spec, Input: json.RawMessage(`{"items":[` + strings.Repeat(`0,`, 127) + `0]}`)}
		},
		"response bytes": func() api.SimulateAutomationRequest {
			spec := api.WorkflowSpec{Name: "big"}
			for index := range 10 {
				spec.Steps = append(spec.Steps, api.WorkflowStepSpec{Name: fmt.Sprintf("a%d", index), Run: "a"})
			}
			return api.SimulateAutomationRequest{Definition: spec, Input: large}
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := SimulateAutomation(context.Background(), build(), api.PlanHobby)
			var limit *AutomationSimulationLimitError
			if !errors.As(err, &limit) || limit.Observed <= limit.Limit {
				t.Fatalf("budget not enforced: %v", err)
			}
		})
	}
}
