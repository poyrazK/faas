package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type parallelWorkflowTestClient struct {
	mu               sync.Mutex
	capabilities     api.ExecutionCapabilitiesResponse
	runs             map[string]api.ExecutionResponse
	requests         []api.CreateExecutionRequest
	createEntered    chan string
	createBlocks     map[string]<-chan struct{}
	pollBlocks       map[string]<-chan struct{}
	statuses         map[string]api.ExecutionStatus
	results          map[string]json.RawMessage
	activeCreates    int
	maxActiveCreates int
}

func newParallelWorkflowTestClient(maxConcurrent int) *parallelWorkflowTestClient {
	base := newExecutionWorkflowFakeClient()
	base.capabilities.Limits.MaxConcurrentRuns = maxConcurrent
	return &parallelWorkflowTestClient{
		capabilities:  base.capabilities,
		runs:          make(map[string]api.ExecutionResponse),
		createEntered: make(chan string, executionWorkflowMaxSteps),
		createBlocks:  make(map[string]<-chan struct{}),
		pollBlocks:    make(map[string]<-chan struct{}),
		statuses:      make(map[string]api.ExecutionStatus),
		results:       make(map[string]json.RawMessage),
	}
}

func (f *parallelWorkflowTestClient) GetExecutionCapabilities(context.Context) (api.ExecutionCapabilitiesResponse, error) {
	return f.capabilities, nil
}

func (f *parallelWorkflowTestClient) ListExecutionsForWorkflow(_ context.Context, workflowID string, limit, offset int, _ api.ExecutionStatus) (api.ExecutionListResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
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
	return api.ExecutionListResponse{Executions: runs[offset:end], Limit: limit, Offset: offset}, nil
}

func (f *parallelWorkflowTestClient) CreateExecution(_ context.Context, request api.CreateExecutionRequest) (api.ExecutionResponse, error) {
	label := workflowTestLabel(request.StepLabel)
	f.mu.Lock()
	f.requests = append(f.requests, request)
	f.activeCreates++
	if f.activeCreates > f.maxActiveCreates {
		f.maxActiveCreates = f.activeCreates
	}
	block := f.createBlocks[label]
	status := f.statuses[label]
	result := append(json.RawMessage(nil), f.results[label]...)
	if status == "" {
		status = api.ExecutionStatusSucceeded
	}
	f.mu.Unlock()
	f.createEntered <- label
	if block != nil {
		<-block
	}

	run := api.ExecutionResponse{
		ID: "parallel-" + label, WorkflowID: request.WorkflowID,
		StepLabel: request.StepLabel, Status: status, Result: result,
	}
	f.mu.Lock()
	f.runs[run.ID] = run
	f.activeCreates--
	f.mu.Unlock()
	return run, nil
}

func (f *parallelWorkflowTestClient) GetExecution(ctx context.Context, id string) (api.ExecutionResponse, error) {
	f.mu.Lock()
	run, ok := f.runs[id]
	if !ok {
		f.mu.Unlock()
		return api.ExecutionResponse{}, fmt.Errorf("missing run %s", id)
	}
	block := f.pollBlocks[workflowTestLabel(run.StepLabel)]
	f.mu.Unlock()
	if block != nil {
		select {
		case <-ctx.Done():
			return run, ctx.Err()
		case <-block:
		}
	}
	if !run.Status.Terminal() {
		run.Status = api.ExecutionStatusSucceeded
		f.mu.Lock()
		f.runs[id] = run
		f.mu.Unlock()
	}
	return run, nil
}

func workflowTestLabel(storageLabel string) string {
	index := strings.LastIndex(storageLabel, ":")
	if index < 0 {
		return storageLabel
	}
	return storageLabel[index+1:]
}

func TestRunExecutionWorkflowParallelFanInRespectsAccountLimit(t *testing.T) {
	releaseRoots := make(chan struct{})
	client := newParallelWorkflowTestClient(2)
	for index, label := range []string{"scan-a", "scan-b", "scan-c"} {
		client.createBlocks[label] = releaseRoots
		client.results[label] = json.RawMessage(fmt.Sprintf(`{"scan":%d}`, index+1))
	}
	plan := executionWorkflowPlan{
		WorkflowID: "parallel-fan-in", Version: "v1", MaxParallelSteps: 4,
		Steps: []executionWorkflowStep{
			{Label: "scan-a", Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "return {scan: 1}"}},
			{Label: "scan-b", Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "return {scan: 2}"}},
			{Label: "scan-c", Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "return {scan: 3}"}},
			{Label: "merge", DependsOn: []string{"scan-a", "scan-b", "scan-c"}, IncludeDependencyResults: true,
				Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "return input"}},
		},
	}
	type workflowResult struct {
		result executionWorkflowRunResult
		err    error
	}
	done := make(chan workflowResult, 1)
	go func() {
		result, err := runExecutionWorkflow(context.Background(), client, plan, time.Millisecond)
		done <- workflowResult{result: result, err: err}
	}()

	entered := map[string]bool{}
	for len(entered) < 2 {
		select {
		case label := <-client.createEntered:
			entered[label] = true
		case <-time.After(2 * time.Second):
			close(releaseRoots)
			t.Fatalf("only %d independent scans started; want two running together", len(entered))
		}
	}
	select {
	case label := <-client.createEntered:
		close(releaseRoots)
		t.Fatalf("account concurrency cap was exceeded while scans were blocked: %s", label)
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseRoots)

	var completed workflowResult
	select {
	case completed = <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("parallel workflow did not finish")
	}
	if completed.err != nil {
		t.Fatalf("runExecutionWorkflow: %v", completed.err)
	}
	if !completed.result.Complete || len(completed.result.Steps) != 4 {
		t.Fatalf("workflow result = %+v", completed.result)
	}

	client.mu.Lock()
	defer client.mu.Unlock()
	if client.maxActiveCreates != 2 {
		t.Fatalf("maximum concurrent admissions = %d, want exactly the account cap of 2", client.maxActiveCreates)
	}
	var mergeRequest *api.CreateExecutionRequest
	for i := range client.requests {
		if workflowTestLabel(client.requests[i].StepLabel) == "merge" {
			mergeRequest = &client.requests[i]
		}
	}
	if mergeRequest == nil {
		t.Fatal("merge step was not submitted")
	}
	var got map[string]executionWorkflowDependencyInput
	if err := json.Unmarshal(mergeRequest.Input, &got); err != nil {
		t.Fatalf("decode merge input %s: %v", mergeRequest.Input, err)
	}
	for label, expected := range map[string]string{"scan-a": `{"scan":1}`, "scan-b": `{"scan":2}`, "scan-c": `{"scan":3}`} {
		if got[label].Status != string(api.ExecutionStatusSucceeded) {
			t.Errorf("merge input[%q].status = %q, want %q", label, got[label].Status, api.ExecutionStatusSucceeded)
		}
		if string(got[label].Result) != expected {
			t.Errorf("merge input[%q].result = %s, want %s", label, got[label].Result, expected)
		}
	}
}

func TestRunExecutionWorkflowContinuesIndependentBranchesAndProvidesDependencyStatuses(t *testing.T) {
	client := newParallelWorkflowTestClient(1)
	client.statuses["fetch-failed"] = api.ExecutionStatusFailed
	client.results["fetch-good"] = json.RawMessage(`{"items":2}`)
	plan := executionWorkflowPlan{
		WorkflowID: "partial-results", Version: "v1", MaxParallelSteps: 1,
		FailurePolicy: executionWorkflowFailurePolicyContinueIndependent,
		Steps: []executionWorkflowStep{
			{Label: "fetch-failed", Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "return null"}},
			{Label: "analyze-failed", DependsOn: []string{"fetch-failed"}, Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "return null"}},
			{Label: "fetch-good", Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "return null"}},
			{Label: "summarize", DependsOn: []string{"fetch-failed", "analyze-failed", "fetch-good"}, IncludeDependencyResults: true,
				Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "return input"}},
		},
	}

	result, err := runExecutionWorkflow(context.Background(), client, plan, time.Millisecond)
	if err == nil {
		t.Fatal("workflow with a failed Run returned success")
	}
	if result.Complete {
		t.Fatal("workflow with a failed Run was marked complete")
	}
	if len(result.BlockedSteps) != 1 || result.BlockedSteps[0] != "analyze-failed" {
		t.Fatalf("blocked steps = %v, want [analyze-failed]", result.BlockedSteps)
	}
	if len(result.Steps) != 3 {
		t.Fatalf("retained receipts = %d, want failed branch, independent branch, and summary: %+v", len(result.Steps), result.Steps)
	}

	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.requests) != 3 {
		t.Fatalf("submitted %d Runs, want failed fetch, independent fetch, and summary", len(client.requests))
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
	if inputs["fetch-failed"].Status != string(api.ExecutionStatusFailed) || len(inputs["fetch-failed"].Result) != 0 {
		t.Errorf("failed dependency input = %+v", inputs["fetch-failed"])
	}
	if inputs["analyze-failed"].Status != "blocked" || len(inputs["analyze-failed"].Result) != 0 {
		t.Errorf("blocked dependency input = %+v", inputs["analyze-failed"])
	}
	if inputs["fetch-good"].Status != string(api.ExecutionStatusSucceeded) || string(inputs["fetch-good"].Result) != `{"items":2}` {
		t.Errorf("successful dependency input = %+v", inputs["fetch-good"])
	}
}

func TestRunExecutionWorkflowFailureStopsNewStepsAndDrainsStartedRuns(t *testing.T) {
	releaseSecond := make(chan struct{})
	client := newParallelWorkflowTestClient(2)
	client.statuses["first"] = api.ExecutionStatusFailed
	client.statuses["second"] = api.ExecutionStatusRunning
	client.pollBlocks["second"] = releaseSecond
	plan := executionWorkflowPlan{WorkflowID: "parallel-failure", Version: "v1", MaxParallelSteps: 2, Steps: []executionWorkflowStep{
		{Label: "first", Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "return null"}},
		{Label: "second", Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "return null"}},
		{Label: "never-start", Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "return null"}},
	}}
	type workflowResult struct {
		result executionWorkflowRunResult
		err    error
	}
	done := make(chan workflowResult, 1)
	go func() {
		result, err := runExecutionWorkflow(context.Background(), client, plan, time.Millisecond)
		done <- workflowResult{result: result, err: err}
	}()

	entered := map[string]bool{}
	for len(entered) < 2 {
		select {
		case label := <-client.createEntered:
			entered[label] = true
		case <-time.After(2 * time.Second):
			close(releaseSecond)
			t.Fatalf("only %d initial steps were admitted", len(entered))
		}
	}
	select {
	case label := <-client.createEntered:
		close(releaseSecond)
		t.Fatalf("new step %q was admitted while a started sibling was still settling", label)
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseSecond)

	var completed workflowResult
	select {
	case completed = <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("workflow did not drain its started sibling")
	}
	if completed.err == nil {
		t.Fatal("failed step was not surfaced")
	}
	if len(completed.result.Steps) != 2 {
		t.Fatalf("result should retain both started receipts: %+v", completed.result)
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.requests) != 2 {
		t.Fatalf("launched %d steps after a branch failure; want only the two started runs", len(client.requests))
	}
}

func TestExecutionWorkflowPlanValidatesDependencies(t *testing.T) {
	base := executionWorkflowPlan{WorkflowID: "dependency-validation", Version: "v1", Steps: []executionWorkflowStep{
		{Label: "first", Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "return null"}},
		{Label: "second", Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "return null"}},
	}}
	tests := []struct {
		name string
		edit func(*executionWorkflowPlan)
	}{
		{name: "unknown dependency", edit: func(plan *executionWorkflowPlan) { plan.Steps[1].DependsOn = []string{"missing"} }},
		{name: "forward dependency", edit: func(plan *executionWorkflowPlan) { plan.Steps[0].DependsOn = []string{"second"} }},
		{name: "duplicate dependency", edit: func(plan *executionWorkflowPlan) { plan.Steps[1].DependsOn = []string{"first", "first"} }},
		{name: "results without dependencies", edit: func(plan *executionWorkflowPlan) { plan.Steps[1].IncludeDependencyResults = true }},
		{name: "input conflict", edit: func(plan *executionWorkflowPlan) {
			plan.Steps[1].DependsOn = []string{"first"}
			plan.Steps[1].IncludeDependencyResults = true
			plan.Steps[1].Request.Input = json.RawMessage(`null`)
		}},
		{name: "unknown failure policy", edit: func(plan *executionWorkflowPlan) { plan.FailurePolicy = "continue_always" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := base
			plan.Steps = append([]executionWorkflowStep(nil), base.Steps...)
			test.edit(&plan)
			if _, _, err := executionWorkflowPlanID(plan); err == nil {
				t.Fatal("invalid dependency plan was accepted")
			}
		})
	}
}
