package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestRunExecutionWorkflowValidatesResultContractsBeforeHandoff(t *testing.T) {
	client := newParallelWorkflowTestClient(1)
	client.results["fetch"] = json.RawMessage(`{"items":"not-an-array"}`)
	client.results["backup"] = json.RawMessage(`{"ok":true}`)
	plan := executionWorkflowPlan{
		WorkflowID: "result-contracts", Version: "v1", MaxParallelSteps: 1,
		FailurePolicy: executionWorkflowFailurePolicyContinueIndependent,
		Steps: []executionWorkflowStep{
			{
				Label:        "fetch",
				ResultSchema: json.RawMessage(`{"type":"object","required":["items"],"properties":{"items":{"type":"array"}}}`),
				Request:      api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "return {items: 'not-an-array'}"},
			},
			{Label: "analyze", DependsOn: []string{"fetch"}, Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "return input"}},
			{Label: "backup", Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "return {ok: true}"}},
			{Label: "summarize", DependsOn: []string{"fetch", "analyze", "backup"}, IncludeDependencyResults: true,
				Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "return input"}},
		},
	}

	result, err := runExecutionWorkflow(context.Background(), client, plan, time.Millisecond)
	if err == nil {
		t.Fatal("workflow with a contract violation returned success")
	}
	if result.Complete {
		t.Fatal("workflow with a contract violation was marked complete")
	}
	if len(result.ResultContractFailures) != 1 {
		t.Fatalf("result contract failures = %+v, want one failure", result.ResultContractFailures)
	}
	failure := result.ResultContractFailures[0]
	if failure.Label != "fetch" || failure.Detail == "" {
		t.Fatalf("result contract failure = %+v", failure)
	}
	if len(result.BlockedSteps) != 1 || result.BlockedSteps[0] != "analyze" {
		t.Fatalf("blocked steps = %v, want [analyze]", result.BlockedSteps)
	}
	if len(result.Steps) != 3 || result.Steps[0].Run.Status != api.ExecutionStatusSucceeded {
		t.Fatalf("Run receipts do not retain the successful producer: %+v", result.Steps)
	}

	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.requests) != 3 {
		t.Fatalf("submitted %d Runs, want fetch, independent backup, and status-aware summary", len(client.requests))
	}
	var summaryRequest *api.CreateExecutionRequest
	for i := range client.requests {
		if workflowTestLabel(client.requests[i].StepLabel) == "summarize" {
			summaryRequest = &client.requests[i]
		}
	}
	if summaryRequest == nil {
		t.Fatal("summary step was not submitted")
	}
	var inputs map[string]executionWorkflowDependencyInput
	if err := json.Unmarshal(summaryRequest.Input, &inputs); err != nil {
		t.Fatalf("decode summary input %s: %v", summaryRequest.Input, err)
	}
	if inputs["fetch"].Status != "contract_failed" || len(inputs["fetch"].Result) != 0 {
		t.Errorf("contract-failed dependency input = %+v", inputs["fetch"])
	}
	if inputs["analyze"].Status != "blocked" {
		t.Errorf("blocked dependency input = %+v", inputs["analyze"])
	}
	if inputs["backup"].Status != string(api.ExecutionStatusSucceeded) || string(inputs["backup"].Result) != `{"ok":true}` {
		t.Errorf("successful dependency input = %+v", inputs["backup"])
	}
}

func TestCompileExecutionWorkflowResultSchemasAllowsLocalRefsAndRejectsExternalRefs(t *testing.T) {
	plan := executionWorkflowPlan{Steps: []executionWorkflowStep{{
		Label: "validate",
		ResultSchema: json.RawMessage(`{
			"$schema":"https://json-schema.org/draft/2020-12/schema",
			"type":"object",
			"properties":{"result":{"$ref":"#/$defs/result"}},
			"$defs":{"result":{"type":"string"}}
		}`),
	}}}
	schemas, err := compileExecutionWorkflowResultSchemas(plan)
	if err != nil {
		t.Fatalf("compile schema with a local reference: %v", err)
	}
	if err := validateExecutionWorkflowResult("validate", json.RawMessage(`{"result":"ok"}`), schemas["validate"]); err != nil {
		t.Fatalf("valid result rejected: %v", err)
	}
	if err := validateExecutionWorkflowResult("validate", json.RawMessage(`{"result":42}`), schemas["validate"]); err == nil {
		t.Fatal("invalid result was accepted")
	}

	plan.Steps[0].ResultSchema = json.RawMessage(`{"$ref":"https://example.com/schema.json"}`)
	if _, err := compileExecutionWorkflowResultSchemas(plan); err == nil {
		t.Fatal("external schema reference was accepted")
	}
}

func TestRunExecutionWorkflowRevalidatesResumedReceipts(t *testing.T) {
	client := newParallelWorkflowTestClient(1)
	plan := executionWorkflowPlan{WorkflowID: "resumed-result-contract", Version: "v1", Steps: []executionWorkflowStep{
		{
			Label:        "fetch",
			ResultSchema: json.RawMessage(`{"type":"object","required":["items"],"properties":{"items":{"type":"array"}}}`),
			Request:      api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "return {items: []}"},
		},
		{Label: "analyze", DependsOn: []string{"fetch"}, Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "return input"}},
	}}
	_, labels, err := executionWorkflowPlanID(plan)
	if err != nil {
		t.Fatalf("executionWorkflowPlanID: %v", err)
	}
	client.runs["existing-fetch"] = api.ExecutionResponse{
		ID: "existing-fetch", WorkflowID: plan.WorkflowID, StepLabel: labels["fetch"],
		Status: api.ExecutionStatusSucceeded, Result: json.RawMessage(`{"items":"invalid"}`),
	}

	result, err := runExecutionWorkflow(context.Background(), client, plan, time.Millisecond)
	if err == nil {
		t.Fatal("resumed workflow with an invalid result contract returned success")
	}
	if len(result.Steps) != 1 || result.Steps[0].Run.ID != "existing-fetch" {
		t.Fatalf("resumed Run receipt was not retained: %+v", result.Steps)
	}
	if len(result.ResultContractFailures) != 1 || result.ResultContractFailures[0].Label != "fetch" {
		t.Fatalf("resumed result contract failure = %+v", result.ResultContractFailures)
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.requests) != 0 {
		t.Fatalf("resumed workflow repeated %d Runs despite its existing receipt", len(client.requests))
	}
}
