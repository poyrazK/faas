package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/runtasks"
	"github.com/onebox-faas/faas/pkg/state"
)

const toolTask = "gregale_task"

type TaskCallbackStore interface {
	PendingExecutionTaskCallbacks(context.Context, string, int) ([]state.ExecutionTaskInvocation, error)
	MarkExecutionTaskCallbackDelivered(context.Context, string, string) error
	ExecutionTaskInvocationByID(context.Context, string, string) (state.ExecutionTaskInvocation, error)
}
type TaskExecutionReader interface {
	ExecutionByID(context.Context, string, string) (state.Execution, error)
}
type TaskStarter func(context.Context, state.Account, api.InvokeExecutionTaskRequest, string, *state.ExecutionTaskCallback) (state.Execution, bool, error)

func taskTool() rapTool {
	return rapTool{Name: toolTask, Description: "Invoke an explicitly versioned Run task. Its terminal receipt arrives as this call's result and wakes the thread. Requires the app credential and a task published for this endpoint.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{
			"name": map[string]any{"type": "string"}, "version": map[string]any{"type": "integer", "minimum": 1}, "input": map[string]any{},
			"file_grants": map[string]any{"type": "array", "maxItems": api.AgentExecutionFileGrantMaxCount,
				"items": map[string]any{"type": "string", "maxLength": api.AgentExecutionFileGrantMaxBytes}},
		}, "required": []string{"name", "version"}, "additionalProperties": false}}
}
func (h *Handler) startTask(ctx context.Context, grant CallbackToken, inv rapInvocation, caller *CallbackCaller) *api.Problem {
	if caller == nil {
		return api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized, "App credential required", "gregale_task requires the app's gregale:agents workload token or a deploy:write API key")
	}
	if h.StartTask == nil {
		return api.ErrCapacity("Run task admission is unavailable")
	}
	var args struct {
		Name       string          `json:"name"`
		Version    int             `json:"version"`
		Input      json.RawMessage `json:"input"`
		FileGrants []string        `json:"file_grants"`
	}
	dec := json.NewDecoder(bytes.NewReader(inv.Arguments))
	dec.DisallowUnknownFields()
	if dec.Decode(&args) != nil || len(args.FileGrants) > api.AgentExecutionFileGrantMaxCount {
		return h.answerTool(ctx, grant, inv, "gregale_task: expected name, version, optional JSON input and bounded file_grants")
	}
	callback := &state.ExecutionTaskCallback{EndpointID: grant.EndpointID, ThreadID: grant.ThreadID, ToolCallID: inv.ID, IssuedAt: grant.IssuedAt}
	if inv.CallID != nil {
		callback.ProviderCallID = *inv.CallID
	}
	var files []api.ExecutionFileInput
	for _, token := range args.FileGrants {
		if token == "" || len(token) > api.AgentExecutionFileGrantMaxBytes {
			return h.answerTool(ctx, grant, inv, "gregale_task: invalid file grant")
		}
		files = append(files, api.ExecutionFileInput{GrantToken: token})
	}
	_, _, err := h.StartTask(ctx, caller.Account, api.InvokeExecutionTaskRequest{Name: args.Name, Version: args.Version, Input: args.Input, InputFiles: files}, runDedupeKey(grant, inv.ID), callback)
	if err == nil {
		return nil
	}
	problem := runtasks.Problem(err)
	if errors.Is(err, state.ErrExecutionAdmissionConflict) || problem.Status >= 500 || problem.Code == "run_budget_exceeded" || problem.Code == "run_attribution_denied" {
		return problem
	}
	return h.answerTool(ctx, grant, inv, fmt.Sprintf("gregale_task: %s: %s", problem.Title, problem.Detail))
}

// DeliverTaskCallbacks replays committed terminal receipts into the existing
// durable inbox. Publishing precedes the delivered mark, so a lost acknowledgement
// reuses the same inbox idempotency key after a process restart.
func (h *Handler) DeliverTaskCallbacks(ctx context.Context) (int, error) {
	if h.TaskCallbacks == nil || h.TaskExecutions == nil || !h.callbacksConfigured() {
		return 0, nil
	}
	h.taskDeliveryMu.Lock()
	defer h.taskDeliveryMu.Unlock()
	rows, err := h.TaskCallbacks.PendingExecutionTaskCallbacks(ctx, h.taskCallbackCursor, api.ExecutionTaskCallbackBatch)
	if err == nil && len(rows) == 0 && h.taskCallbackCursor != "" {
		h.taskCallbackCursor = ""
		rows, err = h.TaskCallbacks.PendingExecutionTaskCallbacks(ctx, "", api.ExecutionTaskCallbackBatch)
	}
	if err != nil {
		return 0, err
	}
	settled := 0
	var errs []error
	for _, inv := range rows {
		h.taskCallbackCursor = inv.ExecutionID
		if h.AgentsEnabled == nil || !h.AgentsEnabled(ctx, inv.AccountID) {
			continue
		}
		if err := h.deliverTaskCallback(ctx, inv); err != nil {
			errs = append(errs, err)
		} else {
			settled++
		}
	}
	return settled, errors.Join(errs...)
}
func (h *Handler) deliverTaskCallback(ctx context.Context, link state.ExecutionTaskInvocation) error {
	c := link.Callback
	if c == nil {
		return errors.New("task continuation has no callback")
	}
	grant := CallbackToken{AccountID: link.AccountID, EndpointID: c.EndpointID, ThreadID: c.ThreadID, IssuedAt: c.IssuedAt, Kinds: []string{api.AgentCallbackKindToolResult}}
	live, err := h.threadExists(ctx, grant)
	if err != nil {
		return err
	}
	if live {
		row, err := h.TaskExecutions.ExecutionByID(ctx, link.AccountID, link.ExecutionID)
		if err != nil {
			return err
		}
		inv := rapInvocation{ID: c.ToolCallID}
		if c.ProviderCallID != "" {
			inv.CallID = &c.ProviderCallID
		}
		post, problem := taskEndPost(grant, inv, row, link.Ref)
		if problem != nil {
			return problem
		}
		_, err = h.Inbox.AppendManagedRealtimeChannelMetadata(ctx, grant.EndpointID, api.AgentInboxChannel, post.Data, false, callbackIdempotencyKey(grant.ThreadID, post.MessageID), callbackMetadata(grant.ThreadID, post))
		if err != nil && !errors.Is(err, state.ErrNotFound) && !errors.Is(err, state.ErrConflict) {
			return err
		}
	}
	return h.TaskCallbacks.MarkExecutionTaskCallbackDelivered(ctx, link.AccountID, link.ExecutionID)
}
func taskEndPost(grant CallbackToken, inv rapInvocation, row state.Execution, ref api.ExecutionTaskRef) (api.AgentCallbackPost, *api.Problem) {
	receipt := "/v1/agent-tools/runs/" + row.ID
	result := map[string]any{"execution_id": row.ID, "task": ref, "status": row.Status, "receipt": receipt, "result_validation": row.ResultValidation}
	if row.Status == api.ExecutionStatusSucceeded && (row.ResultValidation == nil || row.ResultValidation.Status != "invalid") {
		result["result"] = row.Result
	}
	if row.ResultValidation != nil && row.ResultValidation.Status == "invalid" {
		result["outcome"] = "result_schema_invalid"
	}
	if row.FailureCode != nil {
		result["error_code"] = *row.FailureCode
	}
	raw, _ := json.Marshal(result)
	post, p := toolResultPost(grant, inv, string(raw))
	if p != nil {
		delete(result, "result")
		result["result_omitted"] = true
		raw, _ = json.Marshal(result)
		post, p = toolResultPost(grant, inv, string(raw))
	}
	return post, p
}

// TaskReceipt authorizes an app only for task receipts linked to its endpoint.
func (h *Handler) TaskReceipt(ctx context.Context, caller CallbackCaller, id string) (state.Execution, *api.Problem) {
	if h.TaskCallbacks == nil || h.TaskExecutions == nil || h.Inbox == nil {
		return state.Execution{}, api.ErrCapacity("task receipts unavailable")
	}
	link, err := h.TaskCallbacks.ExecutionTaskInvocationByID(ctx, caller.Account.ID, id)
	if err != nil || link.Callback == nil {
		return h.pipelineRunReceipt(ctx, caller, id)
	}
	if _, p := caller.endpoint(ctx, h.Inbox, link.Callback.EndpointID); p != nil {
		return state.Execution{}, p
	}
	row, err := h.TaskExecutions.ExecutionByID(ctx, caller.Account.ID, id)
	if err != nil {
		return state.Execution{}, errRealtimeEndpointNotFound()
	}
	return row, nil
}

// TaskDeliveryReceipt requires a live original thread grant before converting
// a temporary output into app-owned data. Existing deliveries use storage
// authority instead and do not call this method again on replay.
func (h *Handler) TaskDeliveryReceipt(ctx context.Context, caller CallbackCaller, id string) (state.Execution, *api.Problem) {
	if h.TaskCallbacks == nil || h.TaskExecutions == nil || h.Inbox == nil || !h.callbacksConfigured() {
		return state.Execution{}, api.ErrCapacity("task delivery authorization unavailable")
	}
	link, err := h.TaskCallbacks.ExecutionTaskInvocationByID(ctx, caller.Account.ID, id)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return state.Execution{}, api.ErrCapacity("task delivery authorization unavailable")
	}
	if errors.Is(err, state.ErrNotFound) || link.Callback == nil {
		return h.pipelineRunReceipt(ctx, caller, id)
	}
	c := link.Callback
	if _, p := caller.endpoint(ctx, h.Inbox, c.EndpointID); p != nil {
		return state.Execution{}, p
	}
	live, err := h.threadExists(ctx, CallbackToken{AccountID: link.AccountID, EndpointID: c.EndpointID, ThreadID: c.ThreadID, IssuedAt: c.IssuedAt, Kinds: []string{api.AgentCallbackKindToolResult}})
	if err != nil {
		return state.Execution{}, api.ErrCapacity("task delivery authorization unavailable")
	}
	if !live {
		return state.Execution{}, errRealtimeEndpointNotFound()
	}
	return h.TaskReceipt(ctx, caller, id)
}
