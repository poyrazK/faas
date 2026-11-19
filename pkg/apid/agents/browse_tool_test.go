// adr: 20261116182615 §4 — gregale_browse starts a browser task only for the
// app's own credential; the task answers only its own thread and call, once,
// with a result that fits one inbox message.
package agents_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
)

// fakeBrowse records gregale_browse admissions.
type fakeBrowse struct {
	url      string
	maxChars int
	call     agentstore.SandboxTaskCall
	calls    int
	existing bool
	problem  *api.Problem
}

func (f *fakeBrowse) admit(_ context.Context, _ state.Account, url string, maxChars int,
	call agentstore.SandboxTaskCall) (agentstore.Sandbox, bool, *api.Problem) {
	if f.problem != nil {
		return agentstore.Sandbox{}, false, f.problem
	}
	f.url, f.maxChars, f.call = url, maxChars, call
	f.calls++
	return agentstore.Sandbox{ID: "sbx-1"}, f.existing, nil
}

// fakeSandboxTasks is the ended-task feed: ends not yet marked delivered.
type fakeSandboxTasks struct {
	ends      []agentstore.SandboxTaskEnd
	delivered map[string]bool
}

func (f *fakeSandboxTasks) UndeliveredSandboxTasks(_ context.Context, limit int) ([]agentstore.SandboxTaskEnd, error) {
	var out []agentstore.SandboxTaskEnd
	for _, end := range f.ends {
		if !f.delivered[end.SandboxID] && len(out) < limit {
			out = append(out, end)
		}
	}
	return out, nil
}

func (f *fakeSandboxTasks) MarkSandboxTaskDelivered(_ context.Context, id string) error {
	f.delivered[id] = true
	return nil
}

func newBrowseFixture(t *testing.T) (*callbackFixture, *fakeBrowse, *fakeSandboxTasks, *[]string) {
	t.Helper()
	f, _ := newToolFixture(t)
	browse := &fakeBrowse{}
	tasks := &fakeSandboxTasks{delivered: map[string]bool{}}
	var audits []string
	f.h.Browse, f.h.SandboxTasks = browse.admit, tasks
	f.h.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	f.h.Audit = func(_ context.Context, action, _ string, _ map[string]any) { audits = append(audits, action) }
	return f, browse, tasks, &audits
}

func browseInvocation(token, thread, id string, args any) map[string]any {
	inv := invocation(token, thread, id, args)
	inv["operation"] = "gregale_browse"
	return inv
}

func TestBrowseIsAdmittedForItsCall(t *testing.T) {
	f, browse, _, audits := newBrowseFixture(t)
	issued := f.now
	token, _ := f.issue(t, callbackAccount, api.CreateAgentCallbackRequest{EndpointID: "ep-1", ThreadID: "t-1"})
	args := map[string]any{"url": "https://example.com/", "max_chars": 500}
	if code := f.invoke(t, browseInvocation(token, "t-1", "call_1", args)); code != http.StatusUnauthorized || browse.calls != 0 {
		t.Fatalf("the callback URL alone: invoke = %d, admitted %d", code, browse.calls)
	}
	caller := keyCaller(callbackAccount)
	if code := f.invokeAs(t, browseInvocation(token, "t-1", "call_1", args), &caller); code != http.StatusOK {
		t.Fatalf("invoke = %d", code)
	}
	if browse.url != "https://example.com/" || browse.maxChars != 500 || browse.call.EndpointID != "ep-1" || browse.call.ThreadID != "t-1" ||
		browse.call.ToolCallID != "call_1" || !browse.call.IssuedAt.Equal(issued) {
		t.Fatalf("admission %+v", browse)
	}
	if len(*audits) != 1 || (*audits)[0] != "agent_browse.started" || len(f.inbox.messages) != 0 {
		t.Fatalf("audits %v, inbox %+v", *audits, f.inbox.messages)
	}
	browse.existing = true
	if code := f.invokeAs(t, browseInvocation(token, "t-1", "call_1", args), &caller); code != http.StatusOK || len(*audits) != 1 {
		t.Fatalf("a retried invocation: %d, audits %v", code, *audits)
	}
}

func TestBrowseRefusalsAreToolResults(t *testing.T) {
	f, browse, _, _ := newBrowseFixture(t)
	token, _ := f.issue(t, callbackAccount, api.CreateAgentCallbackRequest{EndpointID: "ep-1", ThreadID: "t-1"})
	caller := keyCaller(callbackAccount)
	for i, tc := range []struct {
		name    string
		off     bool
		args    any
		problem *api.Problem
		want    string
	}{
		{"no url", false, map[string]any{"max_chars": 5}, nil, "gregale_browse: arguments must be an object with a url"},
		{"an unknown argument", false, map[string]any{"url": "https://e.com", "js": true}, nil, "gregale_browse: arguments must be"},
		{"web mode off", false, map[string]any{"url": "https://e.com"}, api.ErrSandboxUnsupported("network.mode", "web egress is not on"),
			"gregale_browse: Not supported for sandbox sessions: network.mode"},
		{"browsers off", true, map[string]any{"url": "https://e.com"}, nil, "gregale_browse: browser sandboxes are not on"},
	} {
		browse.problem = tc.problem
		if tc.off {
			f.h.Browse = nil
		}
		code := f.invokeAs(t, browseInvocation(token, "t-1", "c"+string(rune('0'+i)), tc.args), &caller)
		f.h.Browse = browse.admit
		if code != http.StatusOK || len(f.inbox.messages) != i+1 || !strings.HasPrefix(resultText(t, f.inbox.messages[i]), tc.want) {
			t.Errorf("%s: invoke = %d, inbox %d, want a result starting %q", tc.name, code, len(f.inbox.messages), tc.want)
		}
	}
	browse.problem = api.ErrCapacity("down")
	if code := f.invokeAs(t, browseInvocation(token, "t-1", "c9", map[string]any{"url": "https://e.com"}), &caller); code < 500 {
		t.Fatalf("a store failure: invoke = %d, want a 5xx so the runtime retries", code)
	}
}

func taskEnd(id, call string, issued time.Time, exit *int, output string) agentstore.SandboxTaskEnd {
	return agentstore.SandboxTaskEnd{SandboxID: id, AccountID: callbackAccount, State: "closed", Kind: "browse", URL: "https://e.com/",
		Call: agentstore.SandboxTaskCall{EndpointID: "ep-1", ThreadID: "t-1", ToolCallID: call, IssuedAt: issued}, ExitCode: exit,
		Output: []byte(output)}
}

func TestBrowseEndReachesItsThreadOnce(t *testing.T) {
	f, _, tasks, _ := newBrowseFixture(t)
	issued := f.now
	zero, one := 0, 1
	page, _ := json.Marshal(map[string]any{"url": "https://e.com/", "final_url": "https://e.com/x", "status": 200, "title": "E",
		"text": "hello", "truncated": false})
	long, _ := json.Marshal(map[string]any{"url": "https://e.com/", "status": 200, "title": "Long",
		"text": strings.Repeat("ü", 20_000)})
	failed := taskEnd("sbx-failed", "call_3", issued, &one, "could not load https://e.com/: net::ERR_NAME_NOT_RESOLVED\n")
	silent := taskEnd("sbx-silent", "call_4", issued, nil, "")
	silent.State, silent.OutcomeCode, silent.OutcomeMessage = "failed", "task_failed", "the task did not report"
	tasks.ends = []agentstore.SandboxTaskEnd{taskEnd("sbx-ok", "call_1", issued, &zero, string(page)),
		taskEnd("sbx-long", "call_2", issued, &zero, string(long)), failed, silent}
	if settled, err := f.h.DeliverSandboxTasks(context.Background()); err != nil || settled != 4 || len(f.inbox.messages) != 4 {
		t.Fatalf("deliver = %d, %v, inbox %d", settled, err, len(f.inbox.messages))
	}
	results := make([]map[string]any, 4)
	for i, m := range f.inbox.messages {
		if len(m.Data) > api.AgentCallbackPayloadMaxBytes {
			t.Fatalf("message %d is %d bytes", i, len(m.Data))
		}
		results[i] = runResult(t, m)
	}
	if r := results[0]; r["sandbox_id"] != "sbx-ok" || r["final_url"] != "https://e.com/x" || r["status"] != 200.0 || r["text"] != "hello" ||
		r["truncated"] != nil || r["result_url"] != "https://api.example.test/v1/sandboxes/sbx-ok/task" ||
		f.inbox.messages[0].Metadata["message_id"] != "rap-tool-result-call_1" {
		t.Fatalf("a fetched page %v", r)
	}
	if r := results[1]; r["truncated"] != true || r["title"] != "Long" || len(r["text"].(string)) == 0 || !strings.HasPrefix(strings.Repeat("ü", 20_000), r["text"].(string)) {
		t.Fatalf("a long page %v", r)
	}
	if r := results[2]; !strings.Contains(r["error"].(string), "ERR_NAME_NOT_RESOLVED") || r["text"] != nil {
		t.Fatalf("a failed fetch %v", r)
	}
	if r := results[3]; r["error"] != "the browser did not finish the fetch" || r["outcome"].(map[string]any)["code"] != "task_failed" {
		t.Fatalf("a task that never reported %v", r)
	}
	if settled, _ := f.h.DeliverSandboxTasks(context.Background()); settled != 0 || len(f.inbox.messages) != 4 {
		t.Fatal("a delivered task was published again")
	}
	f.h.Inbox = failingInbox{f.inbox}
	tasks.ends = append(tasks.ends, taskEnd("sbx-later", "call_5", issued, &zero, string(page)))
	if _, err := f.h.DeliverSandboxTasks(context.Background()); err == nil || tasks.delivered["sbx-later"] {
		t.Fatal("a failed publish was settled")
	}
}
