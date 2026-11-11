package faas_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	faas "github.com/poyrazK/faas/sdk/go"
	"strings"
	"testing"
)

func invokeWorkflowRegression(t *testing.T, definition faas.WorkflowDefinition, journal []faas.WorkflowJournalEntry) *faas.WorkflowInvocationResponse {
	t.Helper()
	raw, err := json.Marshal(faas.WorkflowInvocationRequest{Protocol: faas.WorkflowCodeProtocol, RunID: "run", Workflow: definition.Name, Input: json.RawMessage(`{}`), Invocation: len(journal) + 1, Journal: journal})
	if err != nil {
		t.Fatal(err)
	}
	response, err := faas.RunWorkflowInvocation(context.Background(), []faas.WorkflowDefinition{definition}, raw)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func TestWorkflowLargeNonRetryableErrorFitsLimit(t *testing.T) {
	definition := faas.DefineWorkflow("audit", func(ctx context.Context, wf *faas.Workflow, input json.RawMessage) (any, error) {
		return faas.WorkflowStep(wf, "work", func(context.Context, faas.WorkflowStepContext) (int, error) {
			return 0, faas.NonRetryable(errors.New(strings.Repeat("x", 2*1024*1024)))
		})
	})
	response := invokeWorkflowRegression(t, definition, nil)
	if len(response.Ops) != 1 || !response.Ops[0].NonRetryable {
		t.Fatal("expected nonretryable step failure")
	}
	raw, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > faas.WorkflowResponseMaxBytes {
		t.Fatalf("nonretryable error bypasses response fitting: bytes=%d limit=%d", len(raw), faas.WorkflowResponseMaxBytes)
	}
}

func TestWorkflowLargeTerminalErrorFitsLimit(t *testing.T) {
	definition := faas.DefineWorkflow("audit", func(context.Context, *faas.Workflow, json.RawMessage) (any, error) {
		return nil, errors.New(strings.Repeat("failure", 300000))
	})
	response := invokeWorkflowRegression(t, definition, nil)
	raw, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if response.Result.Status != "failed" || len(raw) > faas.WorkflowResponseMaxBytes {
		t.Fatalf("oversized terminal failure: status=%s bytes=%d", response.Result.Status, len(raw))
	}
}

func TestWorkflowParallelErrorsShareResponseBudget(t *testing.T) {
	definition := faas.DefineWorkflow("audit", func(ctx context.Context, wf *faas.Workflow, _ json.RawMessage) (any, error) {
		steps := make([]faas.WorkflowParallelStep, 1000)
		for i := range steps {
			steps[i] = faas.WorkflowParallelStep{Name: fmt.Sprintf("work-%d", i), Run: func(context.Context, faas.WorkflowStepContext) (any, error) {
				return nil, faas.NonRetryable(errors.New(strings.Repeat("failure", 1000)))
			}}
		}
		return faas.WorkflowParallel(wf, steps...), nil
	})
	response := invokeWorkflowRegression(t, definition, nil)
	raw, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Ops) != 1000 || len(raw) > faas.WorkflowResponseMaxBytes {
		t.Fatalf("parallel response lost executed operations or exceeded budget: ops=%d bytes=%d", len(response.Ops), len(raw))
	}
}

func TestWorkflowTerminalResponseReportsReplayCursor(t *testing.T) {
	definition := faas.DefineWorkflow("audit", func(context.Context, *faas.Workflow, json.RawMessage) (any, error) { return "done", nil })
	response := invokeWorkflowRegression(t, definition, []faas.WorkflowJournalEntry{{Seq: 0, Name: "approval", Kind: "event", Status: "waiting"}})
	if response.ReplayedOps == nil || *response.ReplayedOps != 0 {
		t.Fatal("omitted journal history was not reflected in the replay cursor")
	}
}

func TestWorkflowRepeatedMaximumBaseName(t *testing.T) {
	name := strings.Repeat("a", 128)
	calls := 0
	definition := faas.DefineWorkflow("audit", func(ctx context.Context, wf *faas.Workflow, input json.RawMessage) (any, error) {
		for range 2 {
			_, err := faas.WorkflowStep(wf, name, func(context.Context, faas.WorkflowStepContext) (int, error) { calls++; return 1, nil })
			if err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	first := invokeWorkflowRegression(t, definition, nil)
	if len(first.Ops) != 1 {
		t.Fatalf("expected first operation: %+v", first)
	}
	second := invokeWorkflowRegression(t, definition, []faas.WorkflowJournalEntry{{Seq: 0, Name: name, Kind: "step", Status: "succeeded", Attempt: 1, Output: first.Ops[0].Output}})
	if len(second.Ops) != 1 {
		t.Fatalf("expected second operation: %+v", second)
	}
	if second.Ops[0].Name != name+"#2" || calls != 2 {
		t.Fatalf("generated invalid operation name: base_bytes=%d generated_bytes=%d executed_functions=%d", len(name), len(second.Ops[0].Name), calls)
	}
}
