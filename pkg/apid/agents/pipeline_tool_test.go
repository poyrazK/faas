package agents_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/agents"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
	"strings"
	"testing"
)

type pipelineFeed struct {
	link    state.ExecutionPipelineDelivery
	markErr error
	done    bool
}

func (f *pipelineFeed) PendingExecutionPipelineCallbacks(context.Context, string, int) ([]state.ExecutionPipelineDelivery, error) {
	if f.done {
		return nil, nil
	}
	return []state.ExecutionPipelineDelivery{f.link}, nil
}
func (f *pipelineFeed) MarkExecutionPipelineCallbackDelivered(context.Context, string, string) error {
	if f.markErr != nil {
		return f.markErr
	}
	f.done = true
	return nil
}
func (f *pipelineFeed) ExecutionPipelineDeliveryByID(_ context.Context, account, id string) (state.ExecutionPipelineDelivery, error) {
	if account != f.link.AccountID || id != f.link.JobID {
		return state.ExecutionPipelineDelivery{}, state.ErrNotFound
	}
	return f.link, nil
}
func (f *pipelineFeed) ExecutionPipelineDeliveryForRun(ctx context.Context, account, id string) (state.ExecutionPipelineDelivery, error) {
	return f.ExecutionPipelineDeliveryByID(ctx, account, id)
}
func TestPipelineToolRequiresOwnedAppAndBoundedTaskAuthority(t *testing.T) {
	f, _ := newToolFixture(t)
	token, _ := f.issue(t, callbackAccount, api.CreateAgentCallbackRequest{EndpointID: "ep-1", ThreadID: "thread"})
	calls := 0
	f.h.StartPipeline = func(_ context.Context, _ state.Account, r api.CreateManagedExecutionWorkflowRequest, _ map[string][]string, c *state.ExecutionTaskCallback) (state.ExecutionWorkflowJob, bool, error) {
		calls++
		if !strings.HasPrefix(r.WorkflowID, "pipeline-gregale-run-") || r.Version != "v1" || c.ToolCallID != "call" || c.ThreadID != "thread" {
			t.Fatal("bad binding", r, c)
		}
		return state.ExecutionWorkflowJob{}, false, nil
	}
	args := map[string]any{"steps": []any{map[string]any{"label": "report", "task": map[string]any{"name": "report", "version": 1, "input": map[string]any{"rows": 3}}}}}
	inv := invocation(token, "thread", "call", args)
	inv["operation"] = "gregale_pipeline"
	if code := f.invokeAs(t, inv, nil); code != http.StatusUnauthorized {
		t.Fatal(code)
	}
	other := agents.CallbackCaller{Account: state.Account{ID: callbackAccount}, AppID: "other-app"}
	if code := f.invokeAs(t, inv, &other); code != 404 {
		t.Fatal(code)
	}
	caller := keyCaller(callbackAccount)
	if code := f.invokeAs(t, inv, &caller); code != 200 || calls != 1 {
		t.Fatal(code, calls)
	}
	for i, bad := range []map[string]any{
		{"workflow_id": "hijack", "steps": args["steps"]},
		{"steps": []any{map[string]any{"label": "raw", "request": map[string]any{"runtime": "node24", "source": "private"}}}},
		{"batch": map[string]any{"name": "report", "version": 1, "items": []any{map[string]any{"label": "a", "input_files": []any{map[string]any{"file_id": "forbidden", "path": "x"}}}}}},
	} {
		inv["id"] = fmt.Sprintf("bad-%d", i)
		inv["arguments"] = bad
		if code := f.invokeAs(t, inv, &caller); code != 200 || calls != 1 {
			t.Fatal("unsafe admission", code, calls)
		}
	}
}
func TestPipelineContinuationSurvivesLostAckRestartAndCapabilityDisable(t *testing.T) {
	f, _ := newToolFixture(t)
	feed := &pipelineFeed{link: state.ExecutionPipelineDelivery{JobID: "pipeline", AccountID: callbackAccount, Callback: &state.ExecutionTaskCallback{EndpointID: "ep-1", ThreadID: "thread", ToolCallID: "call", ProviderCallID: "provider-call", IssuedAt: f.h.Now()}, Completion: &api.ExecutionPipelineCompletion{CompletionID: "pipeline", WorkflowID: "workflow", Outcome: "succeeded"}}, markErr: errors.New("lost ack")}
	f.h.PipelineCallbacks = feed
	f.h.AgentsEnabled = func(context.Context, string) bool { return false }
	if n, err := f.h.DeliverPipelineCallbacks(t.Context()); n != 0 || err != nil || feed.done {
		t.Fatal(n, err)
	}
	f.h.AgentsEnabled = func(context.Context, string) bool { return true }
	if _, err := f.h.DeliverPipelineCallbacks(t.Context()); err == nil || len(f.inbox.messages) != 1 {
		t.Fatal(err, len(f.inbox.messages))
	}
	fresh := &agents.Handler{Callbacks: f.h.Callbacks, Inbox: f.h.Inbox, Revocations: f.h.Revocations, AgentsEnabled: f.h.AgentsEnabled, PipelineCallbacks: feed}
	feed.markErr = nil
	if n, err := fresh.DeliverPipelineCallbacks(t.Context()); n != 1 || err != nil || len(f.inbox.messages) != 1 || !feed.done {
		t.Fatal(n, err, len(f.inbox.messages))
	}
	raw := string(f.inbox.messages[0].Data)
	if !strings.Contains(raw, "agent-tools/pipelines/pipeline") || !strings.Contains(raw, "provider-call") {
		t.Fatal(raw)
	}
	caller := keyCaller(callbackAccount)
	receipt, p := fresh.PipelineReceipt(t.Context(), caller, "pipeline")
	if p != nil || receipt.Completion.CompletionID != "pipeline" {
		t.Fatal(receipt, p)
	}
	other := agents.CallbackCaller{Account: state.Account{ID: callbackAccount}, AppID: "other-app"}
	if _, p = fresh.PipelineReceipt(t.Context(), other, "pipeline"); p == nil || p.Status != 404 {
		t.Fatal("cross app access")
	}
	// The inbox message holds only a manifest reference, never a full result.
	var value map[string]any
	if json.Unmarshal(f.inbox.messages[0].Data, &value) != nil {
		t.Fatal(raw)
	}
}
func TestPipelineContinuationRevocationDropsPendingDelivery(t *testing.T) {
	f, _ := newToolFixture(t)
	feed := &pipelineFeed{link: state.ExecutionPipelineDelivery{JobID: "pipeline", AccountID: callbackAccount, Callback: &state.ExecutionTaskCallback{EndpointID: "ep-1", ThreadID: "thread", ToolCallID: "call", IssuedAt: f.h.Now()}, Completion: &api.ExecutionPipelineCompletion{CompletionID: "pipeline", Outcome: "cancelled"}}}
	f.h.PipelineCallbacks = feed
	caller := keyCaller(callbackAccount)
	f.revokeAs(t, caller, "ep-1", "thread")
	if n, err := f.h.DeliverPipelineCallbacks(t.Context()); n != 1 || err != nil || !feed.done || len(f.inbox.messages) != 0 {
		t.Fatal(n, err)
	}
	if _, p := f.h.PipelineReceipt(t.Context(), caller, "pipeline"); p == nil || p.Status != 404 {
		t.Fatal("revoked thread read receipt")
	}
}

func TestPipelineReplayConflictPreservesPendingContinuation(t *testing.T) {
	f, _ := newToolFixture(t)
	token, _ := f.issue(t, callbackAccount, api.CreateAgentCallbackRequest{EndpointID: "ep-1", ThreadID: "thread"})
	f.h.StartPipeline = func(context.Context, state.Account, api.CreateManagedExecutionWorkflowRequest, map[string][]string, *state.ExecutionTaskCallback) (state.ExecutionWorkflowJob, bool, error) {
		return state.ExecutionWorkflowJob{}, false, api.NewProblem(http.StatusConflict, api.CodeConflict, "Changed intent", "same call has a different pipeline")
	}
	inv := invocation(token, "thread", "call", map[string]any{"batch": map[string]any{"name": "report", "version": 1, "items": []any{map[string]any{"label": "one"}}}})
	inv["operation"] = "gregale_pipeline"
	caller := keyCaller(callbackAccount)
	if code := f.invokeAs(t, inv, &caller); code != http.StatusConflict || len(f.inbox.messages) != 0 {
		t.Fatal("conflict completed original call", code, len(f.inbox.messages))
	}
}
