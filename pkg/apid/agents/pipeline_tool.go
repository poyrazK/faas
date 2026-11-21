package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/runtasks"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
)

const toolPipeline = "gregale_pipeline"

type PipelineStarter func(context.Context, state.Account, api.CreateManagedExecutionWorkflowRequest, map[string][]string, *state.ExecutionTaskCallback) (state.ExecutionWorkflowJob, bool, error)

func (h *Handler) startPipeline(ctx context.Context, grant CallbackToken, inv rapInvocation, caller *CallbackCaller) *api.Problem {
	if caller == nil {
		return api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized, "App credential required", "gregale_pipeline requires the app's gregale:agents workload token or a deploy:write API key")
	}
	if h.StartPipeline == nil {
		return api.ErrCapacity("Run pipeline admission unavailable")
	}
	request, fileGrants, err := pipelineArguments(inv.Arguments)
	if err != nil {
		return h.answerTool(ctx, grant, inv, "gregale_pipeline: expected delegated task steps or batch; use step-scoped file_grants for uploaded inputs")
	}
	request.WorkflowID = "pipeline-" + runDedupeKey(grant, inv.ID)
	request.Version = "v1"
	callback := &state.ExecutionTaskCallback{EndpointID: grant.EndpointID, ThreadID: grant.ThreadID, ToolCallID: inv.ID, IssuedAt: grant.IssuedAt}
	if inv.CallID != nil {
		callback.ProviderCallID = *inv.CallID
	}
	_, _, err = h.StartPipeline(ctx, caller.Account, request, fileGrants, callback)
	if err == nil {
		return nil
	}
	problem := runtasks.Problem(err)
	if problem.Status == http.StatusConflict || problem.Status >= 500 || problem.Code == "run_budget_exceeded" || problem.Code == "run_attribution_denied" {
		return problem
	}
	return h.answerTool(ctx, grant, inv, fmt.Sprintf("gregale_pipeline: %s: %s", problem.Title, problem.Detail))
}

// DeliverPipelineCallbacks publishes before acknowledging, using the same RAP
// inbox identity on retries and across process restarts. No source/result bytes
// or unscoped artifact capabilities enter the notification.
func (h *Handler) DeliverPipelineCallbacks(ctx context.Context) (int, error) {
	if h.PipelineCallbacks == nil || !h.callbacksConfigured() {
		return 0, nil
	}
	h.pipelineDeliveryMu.Lock()
	defer h.pipelineDeliveryMu.Unlock()
	rows, err := h.PipelineCallbacks.PendingExecutionPipelineCallbacks(ctx, h.pipelineCallbackCursor, api.ExecutionTaskCallbackBatch)
	if err == nil && len(rows) == 0 && h.pipelineCallbackCursor != "" {
		h.pipelineCallbackCursor = ""
		rows, err = h.PipelineCallbacks.PendingExecutionPipelineCallbacks(ctx, "", api.ExecutionTaskCallbackBatch)
	}
	if err != nil {
		return 0, err
	}
	settled := 0
	var errs []error
	for _, link := range rows {
		h.pipelineCallbackCursor = link.JobID
		if h.AgentsEnabled == nil || !h.AgentsEnabled(ctx, link.AccountID) {
			continue
		}
		if err := h.deliverPipelineCallback(ctx, link); err != nil {
			errs = append(errs, err)
		} else {
			settled++
		}
	}
	return settled, errors.Join(errs...)
}
func pipelineGrant(link state.ExecutionPipelineDelivery) CallbackToken {
	c := link.Callback
	return CallbackToken{AccountID: link.AccountID, EndpointID: c.EndpointID, ThreadID: c.ThreadID, IssuedAt: c.IssuedAt, Kinds: []string{api.AgentCallbackKindToolResult}}
}
func (h *Handler) deliverPipelineCallback(ctx context.Context, link state.ExecutionPipelineDelivery) error {
	if link.Callback == nil || link.Completion == nil {
		return errors.New("pipeline continuation missing completion or callback")
	}
	grant := pipelineGrant(link)
	live, err := h.threadExists(ctx, grant)
	if err != nil {
		return err
	}
	if live {
		inv := rapInvocation{ID: link.Callback.ToolCallID}
		if link.Callback.ProviderCallID != "" {
			inv.CallID = &link.Callback.ProviderCallID
		}
		raw, _ := json.Marshal(map[string]any{"completion_id": link.JobID, "workflow_id": link.Completion.WorkflowID, "outcome": link.Completion.Outcome, "receipt": "/v1/agent-tools/pipelines/" + link.JobID})
		post, p := toolResultPost(grant, inv, string(raw))
		if p != nil {
			return p
		}
		_, err = h.Inbox.AppendManagedRealtimeChannelMetadata(ctx, grant.EndpointID, api.AgentInboxChannel, post.Data, false, callbackIdempotencyKey(grant.ThreadID, post.MessageID), callbackMetadata(grant.ThreadID, post))
		if err != nil && !errors.Is(err, state.ErrNotFound) && !errors.Is(err, state.ErrConflict) {
			return err
		}
	}
	return h.PipelineCallbacks.MarkExecutionPipelineCallbackDelivered(ctx, link.AccountID, link.JobID)
}
func (h *Handler) authorizePipeline(ctx context.Context, caller CallbackCaller, link state.ExecutionPipelineDelivery) *api.Problem {
	if link.Callback == nil {
		return errRealtimeEndpointNotFound()
	}
	if _, p := caller.endpoint(ctx, h.Inbox, link.Callback.EndpointID); p != nil {
		return p
	}
	live, err := h.threadExists(ctx, pipelineGrant(link))
	if err != nil {
		return api.ErrCapacity("pipeline receipt authorization unavailable")
	}
	if !live {
		return errRealtimeEndpointNotFound()
	}
	return nil
}
func (h *Handler) PipelineReceipt(ctx context.Context, caller CallbackCaller, id string) (api.ExecutionPipelineReceipt, *api.Problem) {
	if h.PipelineCallbacks == nil || h.Inbox == nil || !h.callbacksConfigured() {
		return api.ExecutionPipelineReceipt{}, api.ErrCapacity("pipeline receipts unavailable")
	}
	link, err := h.PipelineCallbacks.ExecutionPipelineDeliveryByID(ctx, caller.Account.ID, id)
	if err != nil {
		return api.ExecutionPipelineReceipt{}, errRealtimeEndpointNotFound()
	}
	if p := h.authorizePipeline(ctx, caller, link); p != nil {
		return api.ExecutionPipelineReceipt{}, p
	}
	return api.ExecutionPipelineReceipt{ID: link.JobID, Completion: link.Completion}, nil
}
func (h *Handler) pipelineRunReceipt(ctx context.Context, caller CallbackCaller, id string) (state.Execution, *api.Problem) {
	if h.PipelineCallbacks == nil || !h.callbacksConfigured() {
		return state.Execution{}, errRealtimeEndpointNotFound()
	}
	link, err := h.PipelineCallbacks.ExecutionPipelineDeliveryForRun(ctx, caller.Account.ID, id)
	if err != nil {
		return state.Execution{}, errRealtimeEndpointNotFound()
	}
	if p := h.authorizePipeline(ctx, caller, link); p != nil {
		return state.Execution{}, p
	}
	row, err := h.TaskExecutions.ExecutionByID(ctx, caller.Account.ID, id)
	if err != nil {
		return state.Execution{}, errRealtimeEndpointNotFound()
	}
	return row, nil
}
