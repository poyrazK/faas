package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestPreviewExecutionWorkflowIsReadOnlyAndShowsAdmissionState(t *testing.T) {
	plan := executionWorkflowPlan{
		WorkflowID: "preview-read-only", Version: "v1", MaxParallelSteps: 4,
		Steps: []executionWorkflowStep{
			{Label: "root", Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "return null"}},
			{Label: "dependent", DependsOn: []string{"root"}, Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "return null"}},
			{Label: "independent", Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "return null"}},
		},
	}
	_, labels, err := executionWorkflowPlanID(plan)
	if err != nil {
		t.Fatal(err)
	}
	client := newExecutionWorkflowFakeClient()
	client.capabilities.Limits.MaxConcurrentRuns = 2
	client.runs["run-existing"] = api.ExecutionResponse{
		ID: "run-existing", WorkflowID: plan.WorkflowID, StepLabel: labels["root"], Status: api.ExecutionStatusRunning,
	}

	preview, err := previewExecutionWorkflow(context.Background(), client, plan)
	if err != nil {
		t.Fatalf("previewExecutionWorkflow: %v", err)
	}
	if !preview.DryRun || preview.RequestedMaxParallelSteps != 4 || preview.EffectiveMaxParallelSteps != 2 ||
		preview.AccountMaxConcurrentRuns == nil || *preview.AccountMaxConcurrentRuns != 2 ||
		preview.InProgressRuns != 1 || preview.AvailableParallelSlots != 1 {
		t.Fatalf("preview concurrency = %+v", preview)
	}
	if len(preview.Steps) != 3 || preview.Steps[0].State != "in_progress" || preview.Steps[0].RunStatus != api.ExecutionStatusRunning ||
		preview.Steps[1].State != "waiting_for_dependencies" || preview.Steps[2].State != "ready" {
		t.Fatalf("preview step states = %+v", preview.Steps)
	}
	if len(client.requests) != 0 || client.getCalls != 0 {
		t.Fatalf("preview performed a mutation or poll: creates=%d gets=%d", len(client.requests), client.getCalls)
	}
	if client.capabilityCalls != 1 {
		t.Fatalf("capabilities fetched %d times, want once", client.capabilityCalls)
	}
}

func TestPreviewExecutionWorkflowReportsContractFailureAndFailFastState(t *testing.T) {
	plan := executionWorkflowPlan{
		WorkflowID: "preview-contract", Version: "v1",
		Steps: []executionWorkflowStep{
			{Label: "producer", ResultSchema: json.RawMessage(`{"type":"string"}`), Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "return null"}},
			{Label: "consumer", DependsOn: []string{"producer"}, Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "return null"}},
		},
	}
	_, labels, err := executionWorkflowPlanID(plan)
	if err != nil {
		t.Fatal(err)
	}
	client := newExecutionWorkflowFakeClient()
	client.runs["run-contract"] = api.ExecutionResponse{
		ID: "run-contract", WorkflowID: plan.WorkflowID, StepLabel: labels["producer"],
		Status: api.ExecutionStatusSucceeded, Result: json.RawMessage(`{"ok":true}`),
	}

	preview, err := previewExecutionWorkflow(context.Background(), client, plan)
	if err != nil {
		t.Fatalf("previewExecutionWorkflow: %v", err)
	}
	if preview.Steps[0].State != "contract_failed" || preview.Steps[0].Detail == "" ||
		preview.Steps[1].State != "not_admitted_fail_fast" {
		t.Fatalf("preview step states = %+v", preview.Steps)
	}
	if client.capabilityCalls != 0 || len(client.requests) != 0 || client.getCalls != 0 {
		t.Fatalf("preview should stop after the known fail-fast receipt: capabilities=%d creates=%d gets=%d", client.capabilityCalls, len(client.requests), client.getCalls)
	}
}

func TestPreviewExecutionWorkflowShowsBlockedAndPartialResultSteps(t *testing.T) {
	plan := executionWorkflowPlan{
		WorkflowID: "preview-partial", Version: "v1", FailurePolicy: executionWorkflowFailurePolicyContinueIndependent,
		Steps: []executionWorkflowStep{
			{Label: "failed", Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "return null"}},
			{Label: "succeeded", Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "return null"}},
			{Label: "blocked", DependsOn: []string{"failed"}, Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "return null"}},
			{Label: "aggregate", DependsOn: []string{"failed", "succeeded"}, IncludeDependencyResults: true, Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "return null"}},
		},
	}
	_, labels, err := executionWorkflowPlanID(plan)
	if err != nil {
		t.Fatal(err)
	}
	client := newExecutionWorkflowFakeClient()
	client.runs["run-failed"] = api.ExecutionResponse{
		ID: "run-failed", WorkflowID: plan.WorkflowID, StepLabel: labels["failed"], Status: api.ExecutionStatusFailed,
	}
	client.runs["run-succeeded"] = api.ExecutionResponse{
		ID: "run-succeeded", WorkflowID: plan.WorkflowID, StepLabel: labels["succeeded"], Status: api.ExecutionStatusSucceeded,
	}

	preview, err := previewExecutionWorkflow(context.Background(), client, plan)
	if err != nil {
		t.Fatalf("previewExecutionWorkflow: %v", err)
	}
	if preview.Steps[0].State != "failed" || preview.Steps[1].State != "succeeded" ||
		preview.Steps[2].State != "blocked" || preview.Steps[3].State != "ready" {
		t.Fatalf("preview step states = %+v", preview.Steps)
	}
	if len(client.requests) != 0 || client.getCalls != 0 {
		t.Fatalf("preview performed a mutation or poll: creates=%d gets=%d", len(client.requests), client.getCalls)
	}
}

func TestPreviewExecutionWorkflowRunsTheSamePendingStepPreflight(t *testing.T) {
	plan := executionWorkflowPlan{WorkflowID: "preview-preflight", Version: "v1", Steps: []executionWorkflowStep{
		{Label: "invalid", Request: api.CreateExecutionRequest{Runtime: "unsupported", Source: "return null"}},
	}}
	client := newExecutionWorkflowFakeClient()
	if _, err := previewExecutionWorkflow(context.Background(), client, plan); err == nil {
		t.Fatal("preview accepted a step with an unsupported runtime")
	}
	if len(client.requests) != 0 || client.getCalls != 0 {
		t.Fatalf("failed preview performed a mutation or poll: creates=%d gets=%d", len(client.requests), client.getCalls)
	}
}
