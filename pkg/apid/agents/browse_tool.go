package agents

// gregale_browse (ADR-20261116182615 §4): a stateless agent reads a web page
// through a browser sandbox task and wakes when the fetch ends.
//
// Like gregale_run it spends the account's compute, so the invocation needs
// the app's credential and that caller must own the callback's endpoint. The
// task is admitted like a browser session on web egress and linked to the
// thread and call in the admitting transaction, so a retried invocation
// finds its task. When the task ends, apid publishes the fetch into the
// thread as the call's tool result. An inbox message holds at most
// AgentCallbackPayloadMaxBytes, so a long page's text is shortened there and
// result_url reads it in full.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
)

const toolBrowse = "gregale_browse"

// BrowseAdmitter admits a gregale_browse task; the sandboxes handler's
// AdmitBrowse is one.
type BrowseAdmitter func(ctx context.Context, acct state.Account, url string, maxChars int,
	call agentstore.SandboxTaskCall) (agentstore.Sandbox, bool, *api.Problem)

// SandboxTaskStore lists and settles ended gregale_browse tasks;
// *agentstore.Store implements it.
type SandboxTaskStore interface {
	UndeliveredSandboxTasks(ctx context.Context, limit int) ([]agentstore.SandboxTaskEnd, error)
	MarkSandboxTaskDelivered(ctx context.Context, sandboxID string) error
}

var errBrowseNeedsCaller = api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized, "App credential required",
	"gregale_browse starts a browser sandbox, so its invocation needs the app's gregale:agents workload token or an API key with deploy:write")

type browseArguments struct {
	URL      string `json:"url"`
	MaxChars int    `json:"max_chars"`
}

// browseToolResult is the text of an ended browse task's tool result: the
// page as gregale-browser fetch read it, or why it could not.
type browseToolResult struct {
	SandboxID string               `json:"sandbox_id"`
	URL       string               `json:"url"`
	FinalURL  string               `json:"final_url,omitempty"`
	Status    int                  `json:"status,omitempty"`
	Title     string               `json:"title,omitempty"`
	Text      string               `json:"text,omitempty"`
	Truncated bool                 `json:"truncated,omitempty"`
	ResultURL string               `json:"result_url,omitempty"`
	Error     string               `json:"error,omitempty"`
	Outcome   *api.AgentRunOutcome `json:"outcome,omitempty"`
}

// browseFetch is gregale-browser fetch's output.
type browseFetch struct {
	URL       string `json:"url"`
	FinalURL  string `json:"final_url"`
	Status    int    `json:"status"`
	Title     string `json:"title"`
	Text      string `json:"text"`
	Truncated bool   `json:"truncated"`
}

func browseTool() rapTool {
	return rapTool{
		Name: toolBrowse,
		Description: "Load a public web page in a headless browser and wake this thread with its title and text. " +
			"Long pages are shortened in the result; result_url reads the whole fetch. The invocation needs the app's credential.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url": map[string]any{"type": "string", "format": "uri", "description": "The http or https URL to load"},
				"max_chars": map[string]any{"type": "integer", "minimum": 1, "maximum": api.SandboxBrowseMaxChars,
					"description": fmt.Sprintf("The most page text to keep; %d when omitted", api.SandboxBrowseDefaultChars)},
			},
			"required":             []string{"url"},
			"additionalProperties": false,
		},
		DisplayScript: `"Browsing " + args.url`,
	}
}

// startBrowse admits the call's task. Refusals the agent can act on are its
// tool result; a store failure is a 5xx, which the runtime retries.
func (h *Handler) startBrowse(ctx context.Context, grant CallbackToken, inv rapInvocation, caller *CallbackCaller) *api.Problem {
	if caller == nil {
		return errBrowseNeedsCaller
	}
	if h.Browse == nil {
		return h.answerTool(ctx, grant, inv, toolBrowse+": browser sandboxes are not on for this control plane yet")
	}
	var args browseArguments
	dec := json.NewDecoder(bytes.NewReader(inv.Arguments))
	dec.DisallowUnknownFields()
	if dec.Decode(&args) != nil || args.URL == "" {
		return h.answerTool(ctx, grant, inv, toolBrowse+": arguments must be an object with a url, and optionally max_chars")
	}
	sb, existing, problem := h.Browse(ctx, caller.Account, args.URL, args.MaxChars, agentstore.SandboxTaskCall{
		EndpointID: grant.EndpointID, ThreadID: grant.ThreadID, ToolCallID: inv.ID, IssuedAt: grant.IssuedAt})
	if problem != nil {
		if problem.Status < http.StatusInternalServerError {
			return h.answerTool(ctx, grant, inv, fmt.Sprintf("%s: %s: %s", toolBrowse, problem.Title, problem.Detail))
		}
		return problem
	}
	if !existing && h.Audit != nil {
		h.Audit(ctx, "agent_browse.started", caller.Account.ID, map[string]any{"sandbox_id": sb.ID, "tool": toolBrowse,
			"thread_id": grant.ThreadID})
	}
	return nil
}

// DeliverSandboxTasks makes one delivery pass over ended browse tasks and
// returns how many it settled. A task whose delivery failed is retried on
// the next pass.
func (h *Handler) DeliverSandboxTasks(ctx context.Context) (int, error) {
	if h.SandboxTasks == nil {
		return 0, nil
	}
	ends, err := h.SandboxTasks.UndeliveredSandboxTasks(ctx, runCallbackBatch)
	if err != nil {
		return 0, err
	}
	settled := 0
	var errs []error
	for _, end := range ends {
		if err := h.deliverTaskEnd(ctx, end); err != nil {
			errs = append(errs, fmt.Errorf("sandbox task %s: %w", end.SandboxID, err))
			continue
		}
		settled++
	}
	return settled, errors.Join(errs...)
}

// deliverTaskEnd publishes one task's end, or drops it when its thread can
// no longer receive it, and marks it settled either way.
func (h *Handler) deliverTaskEnd(ctx context.Context, end agentstore.SandboxTaskEnd) error {
	grant := CallbackToken{AccountID: end.AccountID, EndpointID: end.Call.EndpointID, ThreadID: end.Call.ThreadID,
		Kinds: []string{api.AgentCallbackKindToolResult}, IssuedAt: end.Call.IssuedAt}
	live, err := h.threadReceives(ctx, grant)
	if err != nil {
		return err
	}
	if live {
		post := h.browseEndPost(grant, rapInvocation{ID: end.Call.ToolCallID}, end)
		_, err := h.Inbox.AppendManagedRealtimeChannelMetadata(ctx, grant.EndpointID, api.AgentInboxChannel, post.Data, false,
			callbackIdempotencyKey(grant.ThreadID, post.MessageID), callbackMetadata(grant.ThreadID, post))
		if err != nil && !errors.Is(err, state.ErrNotFound) && !errors.Is(err, state.ErrConflict) {
			return fmt.Errorf("publish browse end: %w", err)
		}
	}
	return h.SandboxTasks.MarkSandboxTaskDelivered(ctx, end.SandboxID)
}

// browseEndPost is the task's end as the call's tool result, its page text
// shortened until the message fits the inbox.
func (h *Handler) browseEndPost(grant CallbackToken, inv rapInvocation, end agentstore.SandboxTaskEnd) api.AgentCallbackPost {
	result := browseEndResult(end)
	result.ResultURL = h.CallbackBaseURL + "/v1/sandboxes/" + end.SandboxID + "/task"
	for {
		post, problem := toolResultPost(grant, inv, browseResultText(result))
		if problem == nil || result.Text == "" && result.Error == "" && result.Title == "" {
			return post
		}
		// Each pass keeps three quarters, so a 256 KiB output fits in a
		// few dozen passes. The title goes last.
		if result.Text == "" && result.Error == "" {
			result.Title = shorten(result.Title)
		}
		result.Text, result.Error = shorten(result.Text), shorten(result.Error)
		result.Truncated = true
	}
}

// browseEndResult reads what the task left: the fetch when the command
// exited 0 with its JSON, and otherwise the command's output as the error,
// with how the session ended.
func browseEndResult(end agentstore.SandboxTaskEnd) browseToolResult {
	result := browseToolResult{SandboxID: end.SandboxID, URL: end.URL}
	var fetch browseFetch
	if end.ExitCode != nil && *end.ExitCode == 0 && json.Unmarshal(end.Output, &fetch) == nil {
		result.FinalURL, result.Status, result.Title, result.Text, result.Truncated = fetch.FinalURL, fetch.Status, fetch.Title,
			fetch.Text, fetch.Truncated
		return result
	}
	switch {
	case end.ExitCode == nil:
		result.Error = "the browser did not finish the fetch"
	case len(end.Output) > 0:
		result.Error = string(bytes.ToValidUTF8(bytes.TrimSpace(end.Output), nil))
	default:
		result.Error = fmt.Sprintf("the fetch exited %d", *end.ExitCode)
	}
	if end.OutcomeCode != "" {
		message := end.OutcomeMessage
		if len(message) > runOutcomeTextMax {
			message = shortenTo(message, runOutcomeTextMax)
		}
		result.Outcome = &api.AgentRunOutcome{Code: end.OutcomeCode, Message: message}
	}
	return result
}

func browseResultText(result browseToolResult) string {
	text, _ := json.Marshal(result)
	return string(text)
}

// shorten keeps the first three quarters of s, on a rune boundary.
func shorten(s string) string {
	return shortenTo(s, len(s)*3/4)
}

func shortenTo(s string, n int) string {
	if n >= len(s) {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
