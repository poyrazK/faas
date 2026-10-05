package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestManagedExecutionWorkflowRequestPreservesDAGContract(t *testing.T) {
	plan := executionWorkflowPlan{
		WorkflowID: "managed-cli", Version: "v1", MaxParallelSteps: 2,
		FailurePolicy: executionWorkflowFailurePolicyContinueIndependent,
		Steps: []executionWorkflowStep{
			{Label: "inspect", Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "return 1"}},
			{Label: "test", Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "return true"}},
			{Label: "summarize", DependsOn: []string{"inspect", "test"}, IncludeDependencyResults: true, Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "return input"}},
			{Label: "report", InputFromPreviousResult: true, Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "return input"}},
		},
	}
	request, err := managedExecutionWorkflowRequest(plan)
	if err != nil {
		t.Fatalf("managed request: %v", err)
	}
	if len(request.Steps) != 4 || request.WorkflowID != plan.WorkflowID || request.MaxParallelSteps != 2 ||
		request.FailurePolicy != executionWorkflowFailurePolicyContinueIndependent ||
		!request.Steps[2].IncludeDependencyResults ||
		len(request.Steps[2].DependsOn) != 2 || request.Steps[2].DependsOn[1] != "test" ||
		!request.Steps[3].InputFromPreviousResult {
		t.Fatalf("managed request = %+v", request)
	}

	plan.Steps[0].ResultSchema = json.RawMessage(`{"type":"integer"}`)
	request, err = managedExecutionWorkflowRequest(plan)
	if err != nil {
		t.Fatalf("managed result schema request: %v", err)
	}
	if string(request.Steps[0].ResultSchema) != string(plan.Steps[0].ResultSchema) {
		t.Fatalf("managed result schema = %s; want %s", request.Steps[0].ResultSchema, plan.Steps[0].ResultSchema)
	}
	plan.Steps[0].ResultSchema = json.RawMessage(`{"$ref":"https://example.com/schema.json"}`)
	if _, err := managedExecutionWorkflowRequest(plan); err == nil {
		t.Fatal("managed request accepted an external schema reference")
	}
	plan.Steps[0].ResultSchema = nil
	artifactPlan := executionWorkflowPlan{WorkflowID: "managed-artifact-cli", Version: "v1", Steps: []executionWorkflowStep{
		{Label: "collect", Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "return null", OutputFiles: []string{"patch.diff"}}},
		{Label: "review", ArtifactInputs: []executionWorkflowArtifactInput{{FromStep: "collect", Name: "patch.diff", Path: "input/patch.diff"}},
			Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimePython313, Source: "print('review')"}},
	}}
	artifactRequest, err := managedExecutionWorkflowRequest(artifactPlan)
	if err != nil {
		t.Fatalf("managed artifact request: %v", err)
	}
	review := artifactRequest.Steps[1]
	if len(review.ArtifactInputs) != 1 || review.ArtifactInputs[0].FromStep != "collect" ||
		review.ArtifactInputs[0].Name != "patch.diff" || review.ArtifactInputs[0].Path != "input/patch.diff" ||
		review.Request.Entrypoint != "main.py" || len(review.Request.Files) != 1 || string(review.Request.Files[0].Content) != "print('review')" {
		t.Fatalf("managed artifact handoff = %+v", review)
	}
}

type executionWorkflowFakeClient struct {
	runs            map[string]api.ExecutionResponse
	requests        []api.CreateExecutionRequest
	idemKeys        []string
	getCalls        int
	capabilityCalls int
	capabilities    api.ExecutionCapabilitiesResponse
	createRun       func(api.CreateExecutionRequest, int) api.ExecutionResponse
	createError     func(*executionWorkflowFakeClient, api.CreateExecutionRequest, int) error
}

func newExecutionWorkflowFakeClient() *executionWorkflowFakeClient {
	planLimits, _ := api.PlanPro.ExecutionLimits()
	return &executionWorkflowFakeClient{
		runs: make(map[string]api.ExecutionResponse),
		capabilities: api.ExecutionCapabilitiesResponse{
			Plan: string(api.PlanPro), AdmissionAvailable: true,
			Runtimes: []api.ExecutionRuntime{api.ExecutionRuntimeNode22, api.ExecutionRuntimeNode24, api.ExecutionRuntimePython312, api.ExecutionRuntimePython313},
			Profiles: []api.ExecutionProfileCapability{
				{Profile: api.ExecutionProfileStandard, Runtimes: []api.ExecutionRuntime{api.ExecutionRuntimeNode22, api.ExecutionRuntimeNode24, api.ExecutionRuntimePython312, api.ExecutionRuntimePython313}},
				{Profile: api.ExecutionProfilePythonDataV1, Runtimes: []api.ExecutionRuntime{api.ExecutionRuntimePython313}},
			},
			NetworkModes: []api.ExecutionNetworkMode{api.ExecutionNetworkNone},
			Limits: &api.ExecutionCapabilityLimits{
				MaxConcurrentRuns:      planLimits.MaxConcurrent,
				MaxSourceBytes:         planLimits.MaxSourceBytes,
				MaxInputBytes:          planLimits.MaxInputBytes,
				DefaultOutputBytes:     planLimits.DefaultOutputBytes,
				MaxOutputBytes:         planLimits.MaxOutputBytes,
				DefaultTimeoutMS:       planLimits.DefaultTimeoutMS,
				MaxTimeoutMS:           planLimits.MaxTimeoutMS,
				DefaultMemoryMB:        planLimits.DefaultMemoryMB,
				MaxMemoryMB:            planLimits.MaxMemoryMB,
				DefaultCPUMillicores:   planLimits.DefaultCPUMillicores,
				MaxCPUMillicores:       planLimits.MaxCPUMillicores,
				DefaultEphemeralDiskMB: planLimits.DefaultEphemeralDiskMB,
				MaxEphemeralDiskMB:     planLimits.MaxEphemeralDiskMB,
				PIDsMax:                planLimits.PIDsMax,
				MaxBundleFiles:         api.ExecutionBundleMaxFiles,
				MaxArtifactInputs:      api.ExecutionArtifactInputMaxFiles,
				MaxOutputFiles:         api.ExecutionArtifactMaxFiles,
				MaxArtifactPathBytes:   api.ExecutionArtifactMaxPathBytes,
			},
		},
	}
}

func (f *executionWorkflowFakeClient) GetExecutionCapabilities(context.Context) (api.ExecutionCapabilitiesResponse, error) {
	f.capabilityCalls++
	return f.capabilities, nil
}

func (f *executionWorkflowFakeClient) ListExecutionsForWorkflow(_ context.Context, workflowID string, limit, offset int, _ api.ExecutionStatus) (api.ExecutionListResponse, error) {
	var runs []api.ExecutionResponse
	for _, run := range f.runs {
		if run.WorkflowID == workflowID {
			runs = append(runs, run)
		}
	}
	if offset >= len(runs) {
		return api.ExecutionListResponse{Limit: limit, Offset: offset}, nil
	}
	end := offset + limit
	if end > len(runs) {
		end = len(runs)
	}
	page := runs[offset:end]
	next := 0
	if end < len(runs) {
		next = end
	}
	return api.ExecutionListResponse{Executions: page, Limit: limit, Offset: offset, NextOffset: next}, nil
}

func (f *executionWorkflowFakeClient) CreateExecution(ctx context.Context, request api.CreateExecutionRequest) (api.ExecutionResponse, error) {
	index := len(f.requests)
	f.requests = append(f.requests, request)
	f.idemKeys = append(f.idemKeys, api.IdempotencyKeyFromContext(ctx))
	if f.createError != nil {
		if err := f.createError(f, request, index); err != nil {
			return api.ExecutionResponse{}, err
		}
	}
	run := api.ExecutionResponse{
		ID: fmt.Sprintf("run-%d", index+1), WorkflowID: request.WorkflowID,
		StepLabel: request.StepLabel, Status: api.ExecutionStatusSucceeded,
	}
	if f.createRun != nil {
		run = f.createRun(request, index)
	}
	if run.WorkflowID == "" {
		run.WorkflowID = request.WorkflowID
	}
	if run.StepLabel == "" {
		run.StepLabel = request.StepLabel
	}
	f.runs[run.ID] = run
	return run, nil
}

func (f *executionWorkflowFakeClient) GetExecution(_ context.Context, id string) (api.ExecutionResponse, error) {
	f.getCalls++
	run, ok := f.runs[id]
	if !ok {
		return api.ExecutionResponse{}, fmt.Errorf("missing run %s", id)
	}
	if !run.Status.Terminal() {
		run.Status = api.ExecutionStatusSucceeded
		f.runs[id] = run
	}
	return run, nil
}

func TestRunExecutionWorkflowChainsResultAndArtifactAndResumes(t *testing.T) {
	plan := executionWorkflowPlan{
		WorkflowID: "support-case-42",
		Version:    "v1",
		Steps: []executionWorkflowStep{
			{Label: "collect", Request: api.CreateExecutionRequest{
				Runtime: api.ExecutionRuntimeNode22, Source: "return {ok:true}", OutputFiles: []string{"report.json"},
			}},
			{Label: "analyze", InputFromPreviousResult: true,
				ArtifactInputs: []executionWorkflowArtifactInput{{FromStep: "collect", Name: "report.json", Path: "input/report.json"}},
				Request:        api.CreateExecutionRequest{Runtime: api.ExecutionRuntimePython313, Source: "print('analyze')"}},
		},
	}
	client := newExecutionWorkflowFakeClient()
	client.createRun = func(request api.CreateExecutionRequest, index int) api.ExecutionResponse {
		run := api.ExecutionResponse{ID: fmt.Sprintf("run-%d", index+1), WorkflowID: request.WorkflowID,
			StepLabel: request.StepLabel, Status: api.ExecutionStatusSucceeded}
		if index == 0 {
			run.Result = json.RawMessage(`{"item_count":3}`)
			run.Artifacts = []api.ExecutionArtifact{{Name: "report.json", Content: []byte(`{"items":[]}`)}}
		}
		return run
	}

	first, err := runExecutionWorkflow(context.Background(), client, plan, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Complete || len(first.Steps) != 2 || first.Steps[1].Run.Status != api.ExecutionStatusSucceeded {
		t.Fatalf("workflow result = %+v", first)
	}
	secondRequest := client.requests[1]
	if string(secondRequest.Input) != `{"item_count":3}` {
		t.Fatalf("previous result input = %s", secondRequest.Input)
	}
	if len(secondRequest.ArtifactInputs) != 1 || secondRequest.ArtifactInputs[0].ExecutionID != "run-1" ||
		secondRequest.ArtifactInputs[0].Name != "report.json" || secondRequest.ArtifactInputs[0].Path != "input/report.json" {
		t.Fatalf("artifact handoff = %+v", secondRequest.ArtifactInputs)
	}
	if secondRequest.Source != "" || secondRequest.Entrypoint != "main.py" || len(secondRequest.Files) != 1 ||
		string(secondRequest.Files[0].Content) != "print('analyze')" {
		t.Fatalf("source with artifact handoff was not converted to an ephemeral bundle: %+v", secondRequest)
	}
	if client.idemKeys[0] == "" || client.idemKeys[0] == client.idemKeys[1] {
		t.Fatalf("step idempotency keys = %q, %q", client.idemKeys[0], client.idemKeys[1])
	}

	resumed, err := runExecutionWorkflow(context.Background(), client, plan, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if len(resumed.Steps) != 2 || len(client.requests) != 2 {
		t.Fatalf("resume created duplicate runs: result=%+v creates=%d", resumed, len(client.requests))
	}
}

func TestRunExecutionWorkflowPreflightsEveryPendingStepBeforeCreatingRuns(t *testing.T) {
	plan := executionWorkflowPlan{WorkflowID: "preflight-13", Version: "v1", Steps: []executionWorkflowStep{
		{Label: "valid-first", Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "return null"}},
		{Label: "invalid-second", Request: api.CreateExecutionRequest{Runtime: "unsupported", Source: "return null"}},
	}}
	client := newExecutionWorkflowFakeClient()
	if _, err := runExecutionWorkflow(context.Background(), client, plan, time.Millisecond); err == nil {
		t.Fatal("workflow with an invalid later step passed preflight")
	}
	if len(client.requests) != 0 {
		t.Fatalf("preflight failure submitted %d Run(s) before detecting the invalid step", len(client.requests))
	}
	if client.capabilityCalls != 1 {
		t.Fatalf("capabilities fetched %d times, want once", client.capabilityCalls)
	}
}

func TestRunExecutionWorkflowStopsOnFailureAndRefusesChangedPlan(t *testing.T) {
	plan := executionWorkflowPlan{WorkflowID: "case-9", Version: "v1", Steps: []executionWorkflowStep{
		{Label: "inspect", Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "return null"}},
		{Label: "fix", Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "return null"}},
	}}
	client := newExecutionWorkflowFakeClient()
	client.createRun = func(request api.CreateExecutionRequest, index int) api.ExecutionResponse {
		status := api.ExecutionStatusSucceeded
		if index == 0 {
			status = api.ExecutionStatusFailed
		}
		return api.ExecutionResponse{ID: fmt.Sprintf("run-%d", index+1), WorkflowID: request.WorkflowID, StepLabel: request.StepLabel, Status: status}
	}
	if _, err := runExecutionWorkflow(context.Background(), client, plan, time.Millisecond); err == nil {
		t.Fatal("failed step was not surfaced")
	}
	if len(client.requests) != 1 {
		t.Fatalf("launched later step after failure: %d requests", len(client.requests))
	}

	client = newExecutionWorkflowFakeClient()
	client.createRun = func(request api.CreateExecutionRequest, _ int) api.ExecutionResponse {
		return api.ExecutionResponse{ID: "run-1", WorkflowID: request.WorkflowID, StepLabel: request.StepLabel, Status: api.ExecutionStatusSucceeded}
	}
	if _, err := runExecutionWorkflow(context.Background(), client, plan, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	plan.Steps[0].Request.Source = "return 'changed without changing version'"
	if _, err := runExecutionWorkflow(context.Background(), client, plan, time.Millisecond); err == nil {
		t.Fatal("changed plan body reused the same workflow ID and version")
	}
	plan.Steps[0].Request.Source = "return null"
	plan.Version = "v2"
	if _, err := runExecutionWorkflow(context.Background(), client, plan, time.Millisecond); err == nil {
		t.Fatal("changed plan version reused prior workflow_id")
	}
}

func TestRunExecutionWorkflowAdoptsConcurrentStepReceipt(t *testing.T) {
	plan := executionWorkflowPlan{WorkflowID: "parallel-resume", Version: "v1", Steps: []executionWorkflowStep{
		{Label: "inspect", Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "return null"}},
	}}
	planID, storageLabels, err := executionWorkflowPlanID(plan)
	if err != nil {
		t.Fatal(err)
	}
	if planID == plan.Version {
		t.Fatalf("plan identity %q must bind the complete manifest", planID)
	}
	client := newExecutionWorkflowFakeClient()
	client.createError = func(fake *executionWorkflowFakeClient, request api.CreateExecutionRequest, _ int) error {
		fake.runs["winner"] = api.ExecutionResponse{
			ID: "winner", WorkflowID: request.WorkflowID,
			StepLabel: storageLabels["inspect"], Status: api.ExecutionStatusSucceeded,
		}
		return &api.APIError{Problem: *api.NewProblem(409, api.CodeExecutionWorkflowStepExists, "duplicate", "already admitted")}
	}
	result, err := runExecutionWorkflow(context.Background(), client, plan, time.Millisecond)
	if err != nil {
		t.Fatalf("runExecutionWorkflow: %v", err)
	}
	if !result.Complete || len(result.Steps) != 1 || result.Steps[0].Run.ID != "winner" || len(client.requests) != 1 {
		t.Fatalf("result=%+v requests=%d; want existing concurrent receipt", result, len(client.requests))
	}
}

func TestExecutionWorkflowPlanRejectsForwardArtifactReferences(t *testing.T) {
	plan := executionWorkflowPlan{WorkflowID: "case-10", Version: "v1", Steps: []executionWorkflowStep{
		{Label: "first", Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "return null"}},
		{Label: "second", ArtifactInputs: []executionWorkflowArtifactInput{{FromStep: "third", Name: "out", Path: "input/out"}}, Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "return null"}},
		{Label: "third", Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "return null"}},
	}}
	if _, _, err := executionWorkflowPlanID(plan); err == nil {
		t.Fatal("forward reference was accepted")
	}
}

func TestRunExecutionWorkflowWaitsForExistingStepBeforeLaunchingNext(t *testing.T) {
	plan := executionWorkflowPlan{WorkflowID: "resume-11", Version: "v1", Steps: []executionWorkflowStep{
		{Label: "prepare", Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "return null"}},
		{Label: "wait", Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "return null"}},
		{Label: "finish", Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "return null"}},
	}}
	planID, labels, err := executionWorkflowPlanID(plan)
	if err != nil {
		t.Fatal(err)
	}
	client := newExecutionWorkflowFakeClient()
	client.runs["run-1"] = api.ExecutionResponse{ID: "run-1", WorkflowID: plan.WorkflowID, StepLabel: labels["prepare"], Status: api.ExecutionStatusSucceeded}
	client.runs["run-2"] = api.ExecutionResponse{ID: "run-2", WorkflowID: plan.WorkflowID, StepLabel: labels["wait"], Status: api.ExecutionStatusRunning}

	result, err := runExecutionWorkflow(context.Background(), client, plan, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if result.PlanID != planID || len(result.Steps) != 3 || client.getCalls != 1 || len(client.requests) != 1 {
		t.Fatalf("resumed workflow = %+v; gets=%d creates=%d", result, client.getCalls, len(client.requests))
	}
	if client.requests[0].StepLabel != labels["finish"] {
		t.Fatalf("launched step label = %q, want %q", client.requests[0].StepLabel, labels["finish"])
	}
}

func TestRunExecutionWorkflowReturnsSubmittedReceiptWhenWaitIsInterrupted(t *testing.T) {
	plan := executionWorkflowPlan{WorkflowID: "interrupted-12", Version: "v1", Steps: []executionWorkflowStep{
		{Label: "first", Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "return null"}},
	}}
	client := newExecutionWorkflowFakeClient()
	client.createRun = func(request api.CreateExecutionRequest, _ int) api.ExecutionResponse {
		return api.ExecutionResponse{ID: "run-1", WorkflowID: request.WorkflowID, StepLabel: request.StepLabel, Status: api.ExecutionStatusRunning}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := runExecutionWorkflow(ctx, client, plan, time.Millisecond)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("runner error = %v, want context.Canceled", err)
	}
	if result.Complete || len(result.Steps) != 1 || result.Steps[0].Run.ID != "run-1" {
		t.Fatalf("interrupted workflow result lost its submitted receipt: %+v", result)
	}
}
