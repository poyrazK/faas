// adr: 20261117065818 §1–§3 — gregale_browser opens a session only for the
// app's own credential, acts only on a live session of the calling thread,
// and answers each action once with a snapshot that fits one inbox message.
package agents_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
)

// fakeBrowserSessions is the gregale_browser store: sessions by ID, the
// actions recorded and the ends not yet delivered.
type fakeBrowserSessions struct {
	threads   map[string]agentstore.SandboxThread
	actions   []agentstore.SandboxAction
	calls     []agentstore.SandboxTaskCall
	closed    []string
	ends      []agentstore.SandboxActionEnd
	delivered map[string]bool
}

func (f *fakeBrowserSessions) BrowserSessionThread(_ context.Context, _, id string) (agentstore.SandboxThread, error) {
	th, ok := f.threads[id]
	if !ok {
		return th, agentstore.ErrNotFound
	}
	return th, nil
}

func (f *fakeBrowserSessions) CreateSandboxAction(_ context.Context, _, sessionID, kind string, args agentstore.SandboxActionArgs,
	call agentstore.SandboxTaskCall) (agentstore.SandboxAction, bool, error) {
	a := agentstore.SandboxAction{ID: fmt.Sprintf("act-%d", len(f.actions)+1), SessionID: sessionID, Kind: kind, Args: args}
	f.actions, f.calls = append(f.actions, a), append(f.calls, call)
	return a, false, nil
}

func (f *fakeBrowserSessions) CloseSandbox(_ context.Context, _, id string) (agentstore.Sandbox, error) {
	f.closed = append(f.closed, id)
	return agentstore.Sandbox{ID: id}, nil
}

func (f *fakeBrowserSessions) UndeliveredSandboxActions(_ context.Context, limit int) ([]agentstore.SandboxActionEnd, error) {
	var out []agentstore.SandboxActionEnd
	for _, end := range f.ends {
		if !f.delivered[end.ID] && len(out) < limit {
			out = append(out, end)
		}
	}
	return out, nil
}

func (f *fakeBrowserSessions) MarkSandboxActionDelivered(_ context.Context, id string) error {
	f.delivered[id] = true
	return nil
}

func newBrowserFixture(t *testing.T) (*callbackFixture, *fakeBrowse, *fakeBrowserSessions) {
	t.Helper()
	f, _ := newToolFixture(t)
	open := &fakeBrowse{}
	sessions := &fakeBrowserSessions{delivered: map[string]bool{}, threads: map[string]agentstore.SandboxThread{
		"sbx-1":     {State: "ready", EndpointID: "ep-1", ThreadID: "t-1", Actions: 1},
		"sbx-other": {State: "ready", EndpointID: "ep-1", ThreadID: "t-2", Actions: 1},
		"sbx-ended": {State: "expired", EndpointID: "ep-1", ThreadID: "t-1", Actions: 3},
		"sbx-full":  {State: "ready", EndpointID: "ep-1", ThreadID: "t-1", Actions: api.SandboxBrowserActionsMax[api.PlanPro.PlanIndex()]},
	}}
	f.h.BrowserOpen, f.h.BrowserSessions = open.admit, sessions
	f.h.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	f.h.Audit = func(context.Context, string, string, map[string]any) {}
	return f, open, sessions
}

func browserInvocation(token, thread, id string, args any) map[string]any {
	inv := invocation(token, thread, id, args)
	inv["operation"] = "gregale_browser"
	return inv
}

func TestBrowserOpensAndActsForItsThread(t *testing.T) {
	f, open, sessions := newBrowserFixture(t)
	token, _ := f.issue(t, callbackAccount, api.CreateAgentCallbackRequest{EndpointID: "ep-1", ThreadID: "t-1"})
	openArgs := map[string]any{"action": "open", "url": "https://example.com/"}
	if code := f.invoke(t, browserInvocation(token, "t-1", "call_1", openArgs)); code != http.StatusUnauthorized || open.calls != 0 {
		t.Fatalf("the callback URL alone: invoke = %d, opened %d", code, open.calls)
	}
	caller := keyCaller(callbackAccount)
	if code := f.invokeAs(t, browserInvocation(token, "t-1", "call_1", openArgs), &caller); code != http.StatusOK {
		t.Fatalf("open = %d", code)
	}
	if open.url != "https://example.com/" || open.maxChars != api.SandboxBrowserSnapshotDefaultChars || open.call.ToolCallID != "call_1" ||
		open.call.ThreadID != "t-1" || len(f.inbox.messages) != 0 {
		t.Fatalf("open %+v, inbox %d", open, len(f.inbox.messages))
	}
	text := "x"
	if code := f.invokeAs(t, browserInvocation(token, "t-1", "call_2", map[string]any{"action": "type", "session_id": "sbx-1", "ref": 3,
		"text": text, "submit": true, "max_chars": 900}), &caller); code != http.StatusOK {
		t.Fatalf("type = %d", code)
	}
	if len(sessions.actions) != 1 || sessions.actions[0].SessionID != "sbx-1" || sessions.actions[0].Kind != "type" ||
		sessions.actions[0].Args != (agentstore.SandboxActionArgs{Ref: 3, Text: "x", Submit: true, MaxChars: 900}) ||
		sessions.calls[0].ToolCallID != "call_2" || len(f.inbox.messages) != 0 {
		t.Fatalf("actions %+v, calls %+v", sessions.actions, sessions.calls)
	}
	if code := f.invokeAs(t, browserInvocation(token, "t-1", "call_3", map[string]any{"action": "close", "session_id": "sbx-1"}), &caller); code != http.StatusOK {
		t.Fatalf("close = %d", code)
	}
	if len(sessions.closed) != 1 || sessions.closed[0] != "sbx-1" || len(f.inbox.messages) != 1 ||
		!strings.Contains(resultText(t, f.inbox.messages[0]), `"closed":true`) {
		t.Fatalf("closed %v, inbox %d", sessions.closed, len(f.inbox.messages))
	}
}

func TestBrowserRefusalsAreToolResults(t *testing.T) {
	f, _, sessions := newBrowserFixture(t)
	token, _ := f.issue(t, callbackAccount, api.CreateAgentCallbackRequest{EndpointID: "ep-1", ThreadID: "t-1"})
	caller := keyCaller(callbackAccount)
	for i, tc := range []struct {
		name string
		args map[string]any
		want string
	}{
		{"no action", map[string]any{"url": "https://e.com"}, "gregale_browser: action must be"},
		{"an unknown argument", map[string]any{"action": "open", "url": "https://e.com", "js": "x"}, "gregale_browser: arguments must be"},
		{"a private url", map[string]any{"action": "open", "url": "http://169.254.169.254/"}, "gregale_browser: url must be"},
		{"no session", map[string]any{"action": "snapshot"}, "gregale_browser: snapshot needs the session_id"},
		{"a click without ref", map[string]any{"action": "click", "session_id": "sbx-1"}, "gregale_browser: click needs the ref"},
		{"a type without text", map[string]any{"action": "type", "session_id": "sbx-1", "ref": 2}, "gregale_browser: type needs"},
		{"a key it does not take", map[string]any{"action": "press", "session_id": "sbx-1", "key": "F12"}, "gregale_browser: key must be"},
		{"a sideways scroll", map[string]any{"action": "scroll", "session_id": "sbx-1", "direction": "left"}, "gregale_browser: direction"},
		{"another thread's session", map[string]any{"action": "snapshot", "session_id": "sbx-other"}, "gregale_browser: no browser session"},
		{"an unknown session", map[string]any{"action": "back", "session_id": "sbx-none"}, "gregale_browser: no browser session"},
		{"an ended session", map[string]any{"action": "snapshot", "session_id": "sbx-ended"}, "gregale_browser: the session ended"},
		{"a full session", map[string]any{"action": "snapshot", "session_id": "sbx-full"}, "gregale_browser: the session ran its"},
		{"too much text", map[string]any{"action": "type", "session_id": "sbx-1", "ref": 1,
			"text": strings.Repeat("y", api.SandboxBrowserTypeMaxChars+1)}, "gregale_browser: text is longer"},
	} {
		code := f.invokeAs(t, browserInvocation(token, "t-1", fmt.Sprintf("c%d", i), tc.args), &caller)
		if code != http.StatusOK || len(f.inbox.messages) != i+1 || !strings.HasPrefix(resultText(t, f.inbox.messages[i]), tc.want) {
			t.Errorf("%s: invoke = %d, inbox %d, want a result starting %q", tc.name, code, len(f.inbox.messages), tc.want)
		}
	}
	if len(sessions.actions) != 0 || len(sessions.closed) != 0 {
		t.Fatalf("a refused call acted: %+v %v", sessions.actions, sessions.closed)
	}
	f.h.BrowserOpen = nil
	f.invokeAs(t, browserInvocation(token, "t-1", "c-off", map[string]any{"action": "open", "url": "https://e.com"}), &caller)
	if got := resultText(t, f.inbox.messages[len(f.inbox.messages)-1]); !strings.HasPrefix(got, "gregale_browser: browser sessions are not on") {
		t.Fatalf("browsers off: %q", got)
	}
}

func actionEnd(id, session, call, kind string, seq int, exit *int, output string) agentstore.SandboxActionEnd {
	return agentstore.SandboxActionEnd{SandboxAction: agentstore.SandboxAction{ID: id, SessionID: session, Seq: seq, Kind: kind},
		AccountID: callbackAccount, Call: agentstore.SandboxTaskCall{EndpointID: "ep-1", ThreadID: "t-1", ToolCallID: call},
		ExitCode: exit, Output: []byte(output), SessionState: "ready"}
}

func TestBrowserActionEndsReachTheirThreadOnceAndFit(t *testing.T) {
	f, _, sessions := newBrowserFixture(t)
	issued := f.now
	zero, one := 0, 1
	var elements []map[string]any
	for i := 1; i <= 150; i++ {
		elements = append(elements, map[string]any{"ref": i, "role": "link", "name": strings.Repeat("n", 40)})
	}
	page, _ := json.Marshal(map[string]any{"url": "https://e.com/", "title": "E", "status": 200, "elements": elements[:2], "text": "hello"})
	big, _ := json.Marshal(map[string]any{"url": "https://e.com/", "title": "Big", "status": 200, "elements": elements,
		"text": strings.Repeat("ü", 4000), "truncated": false})
	ended := actionEnd("a-4", "sbx-1", "call_4", "click", 4, nil, "")
	ended.SessionState, ended.OutcomeCode = "expired", "idle"
	sessions.ends = []agentstore.SandboxActionEnd{actionEnd("a-1", "sbx-1", "call_1", "navigate", 1, &zero, string(page)),
		actionEnd("a-2", "sbx-1", "call_2", "scroll", 2, &zero, string(big)),
		actionEnd("a-3", "sbx-1", "call_3", "click", 3, &one, "ref 9 is not on the page; take a snapshot\n"), ended}
	for i := range sessions.ends {
		sessions.ends[i].Call.IssuedAt = issued
	}
	if settled, err := f.h.DeliverSandboxActions(context.Background()); err != nil || settled != 4 || len(f.inbox.messages) != 4 {
		t.Fatalf("deliver = %d, %v, inbox %d", settled, err, len(f.inbox.messages))
	}
	results := make([]map[string]any, 4)
	for i, m := range f.inbox.messages {
		if len(m.Data) > api.AgentCallbackPayloadMaxBytes {
			t.Fatalf("message %d is %d bytes", i, len(m.Data))
		}
		results[i] = runResult(t, m)
	}
	if r := results[0]; r["action"] != "open" || r["session_id"] != "sbx-1" || r["title"] != "E" || r["text"] != "hello" ||
		len(r["elements"].([]any)) != 2 || r["result_url"] != "https://api.example.test/v1/sandboxes/sbx-1/actions/1" ||
		f.inbox.messages[0].Metadata["message_id"] != "rap-tool-result-call_1" {
		t.Fatalf("an open %v", r)
	}
	if r := results[1]; r["action"] != "scroll" || r["truncated"] != true || r["more_elements"] != true || r["title"] != "Big" ||
		len(r["elements"].([]any)) == 0 || len(r["elements"].([]any)) > 40 {
		t.Fatalf("a large snapshot %v", r)
	}
	if r := results[2]; !strings.HasPrefix(r["error"].(string), "ref 9 is not on the page") || r["elements"] != nil {
		t.Fatalf("a failed click %v", r)
	}
	if r := results[3]; r["error"] != "the session ended before the action finished (idle)" {
		t.Fatalf("an action the session's end cut short %v", r)
	}
	if settled, _ := f.h.DeliverSandboxActions(context.Background()); settled != 0 || len(f.inbox.messages) != 4 {
		t.Fatal("a delivered action was published again")
	}
	f.h.Inbox = failingInbox{f.inbox}
	sessions.ends = append(sessions.ends, actionEnd("a-5", "sbx-1", "call_5", "back", 5, &zero, string(page)))
	if _, err := f.h.DeliverSandboxActions(context.Background()); err == nil || sessions.delivered["a-5"] {
		t.Fatal("a failed publish was settled")
	}
}
