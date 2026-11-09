package agents

// gregale_run (ADR-20261030125501 §4): a stateless agent hands sandboxed
// work to an agent run (ADR-20261027090010) and wakes when the run ends.
//
// A callback URL is not enough to start one. Tool servers hold the thread's
// callback URLs, and a run spends the account's compute with its agents'
// integrations, so the invocation must also carry the app's credential (its
// gregale:agents workload token, or an API key with deploy:write) and that
// caller must own the callback's endpoint. The run is admitted with a dedupe
// key from the call, so a retried invocation finds its run, and linked to the
// thread in the admitting transaction. When the run finishes, apid publishes
// its end into the thread as the call's RAP tool result: the run's state,
// outcome and, for a run that succeeded, its result.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
)

// RunCallbackStore is the state run-end delivery needs; *agentstore.Store
// implements it.
type RunCallbackStore interface {
	UndeliveredRunCallbacks(ctx context.Context, limit int) ([]agentstore.RunCallbackEnd, error)
	MarkRunCallbackDelivered(ctx context.Context, runID string) error
}

const (
	toolRun = "gregale_run"
	// runCallbackBatch and runCallbackInterval pace delivery: a finished run
	// reaches its thread within about one interval.
	runCallbackBatch    = 64
	runCallbackInterval = 5 * time.Second
	// runOutcomeTextMax keeps a long outcome message from crowding out the
	// rest of the tool result.
	runOutcomeTextMax = 1024
)

// errRunNeedsCaller refuses a gregale_run invocation that carries only the
// callback URL.
var errRunNeedsCaller = api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized, "App credential required",
	"gregale_run starts an agent run, so its invocation needs the app's gregale:agents workload token or an API key with deploy:write")

// runArguments are gregale_run's arguments: the run request of
// POST /v1/agents/{agent}/runs without its deliveries.
type runArguments struct {
	Agent   string          `json:"agent"`
	Version int             `json:"version"`
	Prompt  *string         `json:"prompt"`
	Inputs  json.RawMessage `json:"inputs"`
	Tenant  string          `json:"tenant"`
}

// runToolResult is the text of a finished run's tool result.
type runToolResult struct {
	RunID         string               `json:"run_id"`
	State         string               `json:"state"`
	Outcome       *api.AgentRunOutcome `json:"outcome,omitempty"`
	Result        json.RawMessage      `json:"result,omitempty"`
	ResultOmitted bool                 `json:"result_omitted,omitempty"`
}

func runTool() rapTool {
	return rapTool{
		Name: toolRun,
		Description: "Start an agent run, a harness in its own microVM, and wake this thread when it ends. " +
			"The result arrives as this tool's result: the run's state, outcome and, if it succeeded, its result. " +
			"The invocation needs the app's credential.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"agent":   map[string]any{"type": "string", "description": "The agent to run, by name"},
				"version": map[string]any{"type": "integer", "minimum": 1, "description": "The agent version; the latest when omitted"},
				"prompt":  map[string]any{"type": "string", "description": "The run's prompt"},
				"inputs":  map[string]any{"type": "object", "description": "The run's inputs"},
				"tenant":  map[string]any{"type": "string", "description": "A platform tenant ID, for a run on a tenant's behalf"},
			},
			"required":             []string{"agent"},
			"additionalProperties": false,
		},
		DisplayScript: `"Running agent " + args.agent`,
	}
}

// startRun admits the call's run. Refusals the agent can act on, such as an
// unknown agent or a plan limit, are its tool result; a store failure is a
// 5xx, which the runtime retries.
func (h *Handler) startRun(ctx context.Context, grant CallbackToken, inv rapInvocation, caller *CallbackCaller) *api.Problem {
	// InvokeTool has checked that a caller owns the callback's endpoint.
	if caller == nil {
		return errRunNeedsCaller
	}
	if h.Store == nil {
		return api.ErrCapacity("the agent API is unavailable on this control-plane host")
	}
	var args runArguments
	dec := json.NewDecoder(bytes.NewReader(inv.Arguments))
	dec.DisallowUnknownFields()
	if dec.Decode(&args) != nil || args.Agent == "" {
		return h.answerTool(ctx, grant, inv, toolRun+": arguments must be an object with an agent, and optionally version, prompt, inputs and tenant")
	}
	admission, problem := runRequest(api.CreateAgentRunRequest{Tenant: args.Tenant, Version: args.Version, Prompt: args.Prompt,
		Inputs: args.Inputs, DedupeKey: runDedupeKey(grant, inv.ID)})
	if problem != nil {
		return h.answerTool(ctx, grant, inv, toolRun+": "+problem.Detail)
	}
	admission.AccountID, admission.Agent = caller.Account.ID, args.Agent
	admission.Limits = agentstore.PlanLimits(caller.Account.Plan)
	admission.Callback = &agentstore.RunCallback{EndpointID: grant.EndpointID, ThreadID: grant.ThreadID, ToolCallID: inv.ID,
		IssuedAt: grant.IssuedAt}
	if inv.CallID != nil {
		admission.Callback.ProviderCallID = *inv.CallID
	}
	run, existing, err := h.Store.AdmitRun(ctx, admission)
	if err != nil {
		if problem := h.problem(caller.Account, "agent_run.create", err); problem.Status < http.StatusInternalServerError {
			return h.answerTool(ctx, grant, inv, fmt.Sprintf("%s: %s: %s", toolRun, problem.Title, problem.Detail))
		}
		return h.problem(caller.Account, "agent_run.create", err)
	}
	if !existing && h.Audit != nil {
		h.Audit(ctx, "agent_run.created", caller.Account.ID, map[string]any{"run_id": run.ID, "agent_id": run.AgentID,
			"version": run.AgentVersion, "tenant_id": run.TenantID, "tool": toolRun, "thread_id": grant.ThreadID})
	}
	return nil
}

// runDedupeKey is one call's run: printable, without spaces, within the
// dedupe key limit, and no other thread's call can name it.
func runDedupeKey(grant CallbackToken, callID string) string {
	sum := sha256.Sum256([]byte(grant.EndpointID + "\x00" + grant.ThreadID + "\x00" + callID))
	return "gregale-run-" + hex.EncodeToString(sum[:16])
}

// RunRunCallbacks publishes finished gregale_run runs into their threads
// until ctx ends. Every apid runs it: the publish is idempotent, so two
// passes over one run publish it once.
func (h *Handler) RunRunCallbacks(ctx context.Context) {
	if h.RunCallbacks == nil || !h.callbacksConfigured() {
		return
	}
	ticker := time.NewTicker(runCallbackInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if _, err := h.DeliverRunCallbacks(ctx); err != nil && ctx.Err() == nil && h.Log != nil {
			h.Log.Warn("agent run callbacks", "err", err)
		}
	}
}

// DeliverRunCallbacks makes one delivery pass and returns how many runs it
// settled. A run whose delivery failed is retried on the next pass.
func (h *Handler) DeliverRunCallbacks(ctx context.Context) (int, error) {
	ends, err := h.RunCallbacks.UndeliveredRunCallbacks(ctx, runCallbackBatch)
	if err != nil {
		return 0, err
	}
	settled := 0
	var errs []error
	for _, end := range ends {
		if err := h.deliverRunEnd(ctx, end); err != nil {
			errs = append(errs, fmt.Errorf("run %s: %w", end.RunID, err))
			continue
		}
		settled++
	}
	return settled, errors.Join(errs...)
}

// deliverRunEnd publishes one run's end, or drops it when its thread can no
// longer receive it: revoked since the call's callback URL was issued, its
// endpoint gone, or its account without the agents capability. Either way
// the link is marked, so it is settled once.
func (h *Handler) deliverRunEnd(ctx context.Context, end agentstore.RunCallbackEnd) error {
	grant := CallbackToken{AccountID: end.AccountID, EndpointID: end.Callback.EndpointID, ThreadID: end.Callback.ThreadID,
		Kinds: []string{api.AgentCallbackKindToolResult}, IssuedAt: end.Callback.IssuedAt}
	live, err := h.threadReceives(ctx, grant)
	if err != nil {
		return err
	}
	if live {
		inv := rapInvocation{ID: end.Callback.ToolCallID}
		if end.Callback.ProviderCallID != "" {
			inv.CallID = &end.Callback.ProviderCallID
		}
		post := runEndPost(grant, inv, end)
		_, err := h.Inbox.AppendManagedRealtimeChannelMetadata(ctx, grant.EndpointID, api.AgentInboxChannel, post.Data, false,
			callbackIdempotencyKey(grant.ThreadID, post.MessageID), callbackMetadata(grant.ThreadID, post))
		// A conflict is another message under the call's ID: the call is
		// already answered.
		if err != nil && !errors.Is(err, state.ErrNotFound) && !errors.Is(err, state.ErrConflict) {
			return fmt.Errorf("publish run end: %w", err)
		}
	}
	return h.RunCallbacks.MarkRunCallbackDelivered(ctx, end.RunID)
}

// threadReceives reports whether a run's end may still reach its thread.
func (h *Handler) threadReceives(ctx context.Context, grant CallbackToken) (bool, error) {
	if h.AgentsEnabled == nil || !h.AgentsEnabled(ctx, grant.AccountID) {
		return false, nil
	}
	revokedAt, revoked, err := h.Revocations.CallbacksRevokedAt(ctx, grant.EndpointID, grant.ThreadID)
	if err != nil {
		return false, fmt.Errorf("check callback revocation: %w", err)
	}
	if revoked && !grant.IssuedAt.After(revokedAt) {
		return false, nil
	}
	endpoint, err := h.Inbox.ManagedRealtimeEndpointByID(ctx, grant.EndpointID)
	if errors.Is(err, state.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("load endpoint: %w", err)
	}
	return endpoint.AccountID == grant.AccountID, nil
}

// runEndPost is the run's end as the call's tool result. A result that
// would not fit one inbox message is left out, and result_omitted tells the
// agent to read the run's outputs.
func runEndPost(grant CallbackToken, inv rapInvocation, end agentstore.RunCallbackEnd) api.AgentCallbackPost {
	result := runToolResult{RunID: end.RunID, State: end.State, Result: end.Result}
	if end.OutcomeCode != "" {
		message := end.OutcomeMessage
		if len(message) > runOutcomeTextMax {
			message = strings.ToValidUTF8(message[:runOutcomeTextMax], "")
		}
		result.Outcome = &api.AgentRunOutcome{Code: end.OutcomeCode, Message: message}
	}
	post, problem := toolResultPost(grant, inv, runResultText(result))
	if problem != nil && result.Result != nil {
		result.Result, result.ResultOmitted = nil, true
		post, _ = toolResultPost(grant, inv, runResultText(result))
	}
	return post
}

func runResultText(result runToolResult) string {
	text, err := json.Marshal(result)
	if err != nil {
		// A result that is not valid JSON cannot be quoted as JSON.
		result.Result, result.ResultOmitted = nil, true
		text, _ = json.Marshal(result)
	}
	return string(text)
}
