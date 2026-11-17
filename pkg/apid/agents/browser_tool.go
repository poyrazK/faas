package agents

// gregale_browser (ADR-20261117065818): a stateless agent drives a live
// browser session one action per call and wakes with each action's page
// snapshot.
//
// open admits a browser session for the thread and records its first
// action; the other actions name the session, which must be the thread's
// own and still live. Each action is a sandbox_actions row that agentd runs
// on the session's VM; apid publishes its end into the thread as the call's
// tool result, shortened to fit an inbox message, with result_url reading
// it in full. close asks the session to close and answers at once.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
)

const (
	toolBrowser = "gregale_browser"
	// browserResultElements is how many elements a shortened tool result
	// keeps before it drops more.
	browserResultElements = 40
)

// BrowserOpener admits a gregale_browser session; the sandboxes handler's
// AdmitBrowserSession is one.
type BrowserOpener func(ctx context.Context, acct state.Account, url string, maxChars int,
	call agentstore.SandboxTaskCall) (agentstore.Sandbox, bool, *api.Problem)

// BrowserSessionStore records gregale_browser actions and settles ended
// ones; *agentstore.Store implements it.
type BrowserSessionStore interface {
	BrowserSessionThread(ctx context.Context, accountID, id string) (agentstore.SandboxThread, error)
	CreateSandboxAction(ctx context.Context, accountID, sessionID, kind string, args agentstore.SandboxActionArgs,
		call agentstore.SandboxTaskCall) (agentstore.SandboxAction, bool, error)
	CloseSandbox(ctx context.Context, accountID, id string) (agentstore.Sandbox, error)
	UndeliveredSandboxActions(ctx context.Context, limit int) ([]agentstore.SandboxActionEnd, error)
	MarkSandboxActionDelivered(ctx context.Context, id string) error
}

var errBrowserNeedsCaller = api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized, "App credential required",
	"gregale_browser drives a browser sandbox, so its invocation needs the app's gregale:agents workload token or an API key with deploy:write")

// browserActions are gregale_browser's actions besides open and close, as
// agentd runs them.
var browserActions = map[string]bool{"navigate": true, "click": true, "type": true, "press": true, "scroll": true, "back": true, "snapshot": true}

// browserKeys are the keys press takes, as gregale-browser's ACT_KEYS.
var browserKeys = []string{"Enter", "Tab", "Escape", "Backspace", "ArrowUp", "ArrowDown", "ArrowLeft", "ArrowRight", "PageUp", "PageDown",
	"Home", "End"}

type browserArguments struct {
	Action    string  `json:"action"`
	SessionID string  `json:"session_id"`
	URL       string  `json:"url"`
	Ref       int     `json:"ref"`
	Text      *string `json:"text"`
	Submit    bool    `json:"submit"`
	Key       string  `json:"key"`
	Direction string  `json:"direction"`
	MaxChars  int     `json:"max_chars"`
}

func browserTool() rapTool {
	return rapTool{
		Name: toolBrowser,
		Description: "Drive a headless browser on public web pages, one action per call; each result is the page's snapshot: " +
			"url, title, its visible interactive elements with refs, and its text. open starts a session on a url and returns " +
			"its session_id; navigate, click (ref), type (ref, text, optional submit), press (key), scroll (direction), back " +
			"and snapshot act on that session; close ends it. A ref is valid until the next action. Results are shortened; " +
			"result_url reads one in full. The session closes by itself when idle. The invocation needs the app's credential.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{"type": "string", "enum": []string{"open", "navigate", "click", "type", "press", "scroll", "back",
					"snapshot", "close"}},
				"session_id": map[string]any{"type": "string", "description": "The session open returned; every action but open needs it"},
				"url":        map[string]any{"type": "string", "format": "uri", "description": "open's and navigate's http or https URL"},
				"ref":        map[string]any{"type": "integer", "minimum": 1, "description": "click's and type's element, from the last snapshot"},
				"text": map[string]any{"type": "string", "maxLength": api.SandboxBrowserTypeMaxChars,
					"description": "What type enters, replacing the element's value"},
				"submit":    map[string]any{"type": "boolean", "description": "type presses Enter after the text"},
				"key":       map[string]any{"type": "string", "enum": browserKeys, "description": "press's key"},
				"direction": map[string]any{"type": "string", "enum": []string{"up", "down"}, "description": "scroll's direction"},
				"max_chars": map[string]any{"type": "integer", "minimum": 1, "maximum": api.SandboxBrowseMaxChars,
					"description": fmt.Sprintf("The most page text the snapshot keeps; %d when omitted", api.SandboxBrowserSnapshotDefaultChars)},
			},
			"required":             []string{"action"},
			"additionalProperties": false,
		},
		DisplayScript: `if args.action == "open" || args.action == "navigate" { "Browsing " + args.url } else { "Browser: " + args.action }`,
	}
}

// browserArgs reads and checks a gregale_browser call's arguments; a
// refusal is the text the agent gets back.
func browserArgs(raw json.RawMessage) (browserArguments, agentstore.SandboxActionArgs, string) {
	var args browserArguments
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(&args) != nil {
		return args, agentstore.SandboxActionArgs{}, "arguments must be an object with an action and that action's fields"
	}
	act := agentstore.SandboxActionArgs{URL: args.URL, Ref: args.Ref, Submit: args.Submit, Key: args.Key, Direction: args.Direction,
		MaxChars: args.MaxChars}
	if args.Text != nil {
		act.Text = *args.Text
	}
	if act.MaxChars == 0 {
		act.MaxChars = api.SandboxBrowserSnapshotDefaultChars
	}
	if act.MaxChars < 1 || act.MaxChars > api.SandboxBrowseMaxChars {
		return args, act, fmt.Sprintf("max_chars must be 1-%d", api.SandboxBrowseMaxChars)
	}
	if args.Action != "open" && args.Action != "close" && !browserActions[args.Action] {
		return args, act, "action must be open, navigate, click, type, press, scroll, back, snapshot or close"
	}
	if args.Action != "open" && args.SessionID == "" {
		return args, act, args.Action + " needs the session_id open returned"
	}
	switch args.Action {
	case "open", "navigate":
		if !api.ValidSandboxBrowseURL(args.URL) {
			return args, act, "url must be an http or https URL of a public host on port 80 or 443"
		}
	case "click":
		if args.Ref < 1 {
			return args, act, "click needs the ref of an element from the last snapshot"
		}
	case "type":
		if args.Ref < 1 || args.Text == nil {
			return args, act, "type needs a ref from the last snapshot and the text to enter"
		}
		if len([]rune(act.Text)) > api.SandboxBrowserTypeMaxChars {
			return args, act, fmt.Sprintf("text is longer than %d characters", api.SandboxBrowserTypeMaxChars)
		}
	case "press":
		if !slices.Contains(browserKeys, args.Key) {
			return args, act, "key must be one of the keys press takes"
		}
	case "scroll":
		if args.Direction != "up" && args.Direction != "down" {
			return args, act, "direction must be up or down"
		}
	}
	return args, act, ""
}

// startBrowser runs one gregale_browser call. Refusals the agent can act on
// are its tool result; a store failure is a 5xx, which the runtime retries.
func (h *Handler) startBrowser(ctx context.Context, grant CallbackToken, inv rapInvocation, caller *CallbackCaller) *api.Problem {
	if caller == nil {
		return errBrowserNeedsCaller
	}
	if h.BrowserOpen == nil || h.BrowserSessions == nil {
		return h.answerTool(ctx, grant, inv, toolBrowser+": browser sessions are not on for this control plane yet")
	}
	args, act, refusal := browserArgs(inv.Arguments)
	if refusal != "" {
		return h.answerTool(ctx, grant, inv, toolBrowser+": "+refusal)
	}
	call := agentstore.SandboxTaskCall{EndpointID: grant.EndpointID, ThreadID: grant.ThreadID, ToolCallID: inv.ID, IssuedAt: grant.IssuedAt}
	if args.Action == "open" {
		sb, existing, problem := h.BrowserOpen(ctx, caller.Account, args.URL, act.MaxChars, call)
		if problem != nil {
			if problem.Status < http.StatusInternalServerError {
				return h.answerTool(ctx, grant, inv, fmt.Sprintf("%s: %s: %s", toolBrowser, problem.Title, problem.Detail))
			}
			return problem
		}
		if !existing && h.Audit != nil {
			h.Audit(ctx, "agent_browser.opened", caller.Account.ID, map[string]any{"sandbox_id": sb.ID, "tool": toolBrowser,
				"thread_id": grant.ThreadID})
		}
		return nil
	}
	th, err := h.BrowserSessions.BrowserSessionThread(ctx, caller.Account.ID, args.SessionID)
	if errors.Is(err, agentstore.ErrNotFound) || err == nil && (th.EndpointID != grant.EndpointID || th.ThreadID != grant.ThreadID) {
		return h.answerTool(ctx, grant, inv, toolBrowser+": no browser session "+strconv.Quote(args.SessionID)+" in this thread; open one")
	}
	if err != nil {
		return h.problem(caller.Account, "browser session", err)
	}
	if browserSessionEnded(th.State) {
		return h.answerTool(ctx, grant, inv, toolBrowser+": the session ended; open a new one")
	}
	if args.Action == "close" {
		if _, err := h.BrowserSessions.CloseSandbox(ctx, caller.Account.ID, args.SessionID); err != nil && !errors.Is(err, agentstore.ErrNotFound) {
			return h.problem(caller.Account, "close browser session", err)
		}
		text, _ := json.Marshal(map[string]any{"session_id": args.SessionID, "action": "close", "closed": true})
		return h.answerTool(ctx, grant, inv, string(text))
	}
	if limit := api.SandboxBrowserActionsMax[caller.Account.Plan.PlanIndex()]; th.Actions >= limit {
		return h.answerTool(ctx, grant, inv, fmt.Sprintf("%s: the session ran its %d actions (SandboxBrowserActionsMax); open a new one",
			toolBrowser, limit))
	}
	if _, _, err := h.BrowserSessions.CreateSandboxAction(ctx, caller.Account.ID, args.SessionID, args.Action, act, call); err != nil {
		return h.problem(caller.Account, "record browser action", err)
	}
	return nil
}

func browserSessionEnded(st string) bool {
	return st == agentstore.SandboxClosed || st == agentstore.SandboxExpired || st == agentstore.SandboxFailed
}

// DeliverSandboxActions makes one delivery pass over ended gregale_browser
// actions and returns how many it settled. One whose delivery failed is
// retried on the next pass.
func (h *Handler) DeliverSandboxActions(ctx context.Context) (int, error) {
	if h.BrowserSessions == nil {
		return 0, nil
	}
	ends, err := h.BrowserSessions.UndeliveredSandboxActions(ctx, runCallbackBatch)
	if err != nil {
		return 0, err
	}
	settled := 0
	var errs []error
	for _, end := range ends {
		if err := h.deliverActionEnd(ctx, end); err != nil {
			errs = append(errs, fmt.Errorf("sandbox action %s: %w", end.ID, err))
			continue
		}
		settled++
	}
	return settled, errors.Join(errs...)
}

// deliverActionEnd publishes one action's end, or drops it when its thread
// can no longer receive it and closes the thread's session, and marks it
// settled either way.
func (h *Handler) deliverActionEnd(ctx context.Context, end agentstore.SandboxActionEnd) error {
	grant := CallbackToken{AccountID: end.AccountID, EndpointID: end.Call.EndpointID, ThreadID: end.Call.ThreadID,
		Kinds: []string{api.AgentCallbackKindToolResult}, IssuedAt: end.Call.IssuedAt}
	live, err := h.threadReceives(ctx, grant)
	if err != nil {
		return err
	}
	if live {
		post := h.browserEndPost(grant, rapInvocation{ID: end.Call.ToolCallID}, end)
		_, err := h.Inbox.AppendManagedRealtimeChannelMetadata(ctx, grant.EndpointID, api.AgentInboxChannel, post.Data, false,
			callbackIdempotencyKey(grant.ThreadID, post.MessageID), callbackMetadata(grant.ThreadID, post))
		if err != nil && !errors.Is(err, state.ErrNotFound) && !errors.Is(err, state.ErrConflict) {
			return fmt.Errorf("publish browser action: %w", err)
		}
	} else if !browserSessionEnded(end.SessionState) {
		// A thread that cannot hear its browser has no use for it.
		if _, err := h.BrowserSessions.CloseSandbox(ctx, end.AccountID, end.SessionID); err != nil && !errors.Is(err, agentstore.ErrNotFound) {
			return fmt.Errorf("close browser session: %w", err)
		}
	}
	return h.BrowserSessions.MarkSandboxActionDelivered(ctx, end.ID)
}

// browserToolResult is the text of an ended action's tool result: the
// page's snapshot, or why the action failed.
type browserToolResult struct {
	SessionID    string            `json:"session_id"`
	Action       string            `json:"action"`
	Seq          int               `json:"seq"`
	URL          string            `json:"url,omitempty"`
	Title        string            `json:"title,omitempty"`
	Status       int               `json:"status,omitempty"`
	Elements     []json.RawMessage `json:"elements,omitempty"`
	MoreElements bool              `json:"more_elements,omitempty"`
	Text         string            `json:"text,omitempty"`
	Truncated    bool              `json:"truncated,omitempty"`
	ResultURL    string            `json:"result_url,omitempty"`
	Error        string            `json:"error,omitempty"`
}

// browserSnapshot is gregale-browser act's output.
type browserSnapshot struct {
	URL          string            `json:"url"`
	Title        string            `json:"title"`
	Status       int               `json:"status"`
	Elements     []json.RawMessage `json:"elements"`
	MoreElements bool              `json:"more_elements"`
	Text         string            `json:"text"`
	Truncated    bool              `json:"truncated"`
}

// browserEndResult reads what the action left: the snapshot when the
// command exited 0 with its JSON, and otherwise why it failed.
func browserEndResult(end agentstore.SandboxActionEnd) browserToolResult {
	action := end.Kind
	if end.Seq == 1 {
		action = "open"
	}
	result := browserToolResult{SessionID: end.SessionID, Action: action, Seq: end.Seq}
	var snap browserSnapshot
	switch {
	case end.ExitCode != nil && *end.ExitCode == 0 && json.Unmarshal(end.Output, &snap) == nil:
		result.URL, result.Title, result.Status, result.Elements = snap.URL, snap.Title, snap.Status, snap.Elements
		result.MoreElements, result.Text, result.Truncated = snap.MoreElements, snap.Text, snap.Truncated
	case end.ExitCode == nil:
		result.Error = "the session ended before the action finished"
		if end.OutcomeCode != "" {
			result.Error += " (" + end.OutcomeCode + ")"
		}
	case len(end.Output) > 0:
		result.Error = string(bytes.ToValidUTF8(bytes.TrimSpace(end.Output), nil))
	default:
		result.Error = fmt.Sprintf("the action exited %d", *end.ExitCode)
	}
	return result
}

// browserEndPost is the action's end as the call's tool result, shortened
// until it fits the inbox: the text first, then the elements, then the
// title and error.
func (h *Handler) browserEndPost(grant CallbackToken, inv rapInvocation, end agentstore.SandboxActionEnd) api.AgentCallbackPost {
	result := browserEndResult(end)
	result.ResultURL = h.CallbackBaseURL + "/v1/sandboxes/" + end.SessionID + "/actions/" + strconv.Itoa(end.Seq)
	for {
		text, _ := json.Marshal(result)
		post, problem := toolResultPost(grant, inv, string(text))
		if problem == nil {
			return post
		}
		switch {
		case result.Text != "":
			result.Text, result.Truncated = shorten(result.Text), true
		case len(result.Elements) > browserResultElements:
			result.Elements, result.MoreElements = result.Elements[:browserResultElements], true
		case len(result.Elements) > 0:
			result.Elements, result.MoreElements = result.Elements[:len(result.Elements)/2], true
		case result.Title != "" || result.Error != "":
			result.Title, result.Error = shorten(result.Title), shorten(result.Error)
		default:
			return post
		}
	}
}
