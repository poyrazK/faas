package runpipeline_test

import (
	"context"
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/runpipeline"
	"github.com/onebox-faas/faas/pkg/state"
	"strings"
	"testing"
	"time"
)

func TestPinnedBatchCompileAndSafeRetry(t *testing.T) {
	for _, p := range []api.ExecutionRetryPolicy{{MaxAttempts: 0}, {MaxAttempts: 4, SafeToRepeat: true}, {MaxAttempts: 2}, {MaxAttempts: 2, SafeToRepeat: true, BackoffMS: 999}} {
		if _, err := runpipeline.NormalizeRetry(&p); err == nil {
			t.Fatal("unsafe policy", p)
		}
	}
	original := api.CreateManagedExecutionWorkflowRequest{WorkflowID: "batch", Version: "v1", Batch: &api.ExecutionTaskBatch{Name: "report", Version: 2, Retry: &api.ExecutionRetryPolicy{MaxAttempts: 3, SafeToRepeat: true}, Items: []api.ExecutionTaskBatchItem{{Label: "one", Input: json.RawMessage(`{"x":1}`)}, {Label: "two"}}}}
	v := state.ExecutionTaskVersion{ID: "immutable", Name: "report", Version: 2, Digest: strings.Repeat("a", 64), Status: "active"}
	compiler := runpipeline.Compiler{Lookup: func(context.Context, string, int) (state.ExecutionTaskVersion, error) { return v, nil }, Open: func(context.Context, state.ExecutionTaskVersion) (api.PublishExecutionTaskRequest, error) {
		return api.PublishExecutionTaskRequest{Name: "report", Version: 2, Definition: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode24, Source: "private code"}}, nil
	}}
	p, err := compiler.Compile(t.Context(), original, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Request.Steps) != 2 || p.Policy.Steps[1].Task.ID != v.ID || p.Request.Steps[0].Request.Source != "private code" || string(p.Request.Steps[0].Request.Input) != `{"x":1}` {
		t.Fatal("compile", p)
	}
	if original.Batch == nil || len(original.Steps) != 0 {
		t.Fatal("caller request mutated")
	}
	single := api.CreateManagedExecutionWorkflowRequest{WorkflowID: "single", Version: "v1", Steps: []api.CreateManagedExecutionWorkflowStep{{Label: "one", Task: &api.InvokeExecutionTaskRequest{Name: "report", Version: 2}}}}
	if _, err = compiler.Compile(t.Context(), single, false, false); err != nil {
		t.Fatal(err)
	}
	if single.Steps[0].Task == nil || single.Steps[0].Request.Source != "" || single.Steps[0].Retry != nil {
		t.Fatal("step compilation mutated caller request")
	}
	v.Status = "retired"
	if _, err = compiler.Compile(t.Context(), original, false, false); err == nil {
		t.Fatal("retired version compiled")
	}
}
func TestInfrastructureRetryAllowlist(t *testing.T) {
	for _, code := range []string{"restore_failed", "execution_transport_failed", "lease_expired", "guest_error", "guest_protocol_error", "timeout", "out_of_memory", "cancelled", "unknown"} {
		r := state.Execution{Status: api.ExecutionStatusFailed, FailureCode: &code}
		want := code == "restore_failed" || code == "execution_transport_failed" || code == "lease_expired"
		if runpipeline.Retryable(r) != want {
			t.Fatal(code)
		}
	}
	now := time.Now()
	r := state.Execution{FinishedAt: &now}
	p := api.ExecutionRetryPolicy{BackoffMS: 40000}
	if !runpipeline.RetryAt(r, p, 3).Equal(now.Add(60 * time.Second)) {
		t.Fatal("unbounded backoff")
	}
}
