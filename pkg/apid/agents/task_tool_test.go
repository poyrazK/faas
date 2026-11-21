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

type taskFeed struct {
	link    state.ExecutionTaskInvocation
	row     state.Execution
	markErr error
	done    bool
}

func (f *taskFeed) PendingExecutionTaskCallbacks(context.Context, string, int) ([]state.ExecutionTaskInvocation, error) {
	if f.done {
		return nil, nil
	}
	return []state.ExecutionTaskInvocation{f.link}, nil
}
func (f *taskFeed) MarkExecutionTaskCallbackDelivered(context.Context, string, string) error {
	if f.markErr != nil {
		return f.markErr
	}
	f.done = true
	return nil
}
func (f *taskFeed) ExecutionTaskInvocationByID(_ context.Context, account, id string) (state.ExecutionTaskInvocation, error) {
	if account != f.link.AccountID || id != f.row.ID {
		return state.ExecutionTaskInvocation{}, state.ErrNotFound
	}
	return f.link, nil
}
func (f *taskFeed) ExecutionByID(context.Context, string, string) (state.Execution, error) {
	return f.row, nil
}
func TestTaskToolRequiresOwnedAppAndForbidsDynamicAuthority(t *testing.T) {
	f, _ := newToolFixture(t)
	token, _ := f.issue(t, callbackAccount, api.CreateAgentCallbackRequest{EndpointID: "ep-1", ThreadID: "thread"})
	calls := 0
	f.h.StartTask = func(_ context.Context, _ state.Account, r api.InvokeExecutionTaskRequest, _ string, c *state.ExecutionTaskCallback) (state.Execution, bool, error) {
		calls++
		if r.Name != "report" || c.ThreadID != "thread" || c.ToolCallID != "call" {
			t.Fatal("incorrect task binding")
		}
		return state.Execution{}, false, nil
	}
	inv := invocation(token, "thread", "call", map[string]any{"name": "report", "version": 1, "input": map[string]any{"rows": 3}})
	inv["operation"] = "gregale_task"
	if code := f.invokeAs(t, inv, nil); code != 401 {
		t.Fatal("callback-only task", code)
	}
	other := agents.CallbackCaller{Account: state.Account{ID: callbackAccount}, AppID: "other-app"}
	if code := f.invokeAs(t, inv, &other); code != 404 {
		t.Fatal("wrong app", code)
	}
	caller := keyCaller(callbackAccount)
	if code := f.invokeAs(t, inv, &caller); code != 200 || calls != 1 {
		t.Fatal("valid task", code, calls)
	}
	inv["arguments"] = map[string]any{"name": "report", "version": 1, "source": "arbitrary code"}
	if code := f.invokeAs(t, inv, &caller); code != 200 || calls != 1 {
		t.Fatal("dynamic source reached task admission", code, calls)
	}
}

func TestTaskBudgetRefusalKeepsCallRetryableWithoutInboxResult(t *testing.T) {
	f, _ := newToolFixture(t)
	token, _ := f.issue(t, callbackAccount, api.CreateAgentCallbackRequest{EndpointID: "ep-1", ThreadID: "thread"})
	refuse := true
	f.h.StartTask = func(context.Context, state.Account, api.InvokeExecutionTaskRequest, string, *state.ExecutionTaskCallback) (state.Execution, bool, error) {
		if refuse {
			return state.Execution{}, false, &state.RunBudgetError{Kind: "customer", Ref: "customer-42", Metric: "concurrent", Maximum: 1, Observed: 2}
		}
		return state.Execution{}, false, nil
	}
	inv := invocation(token, "thread", "budget-call", map[string]any{"name": "report", "version": 1})
	inv["operation"] = "gregale_task"
	caller := keyCaller(callbackAccount)
	if code := f.invokeAs(t, inv, &caller); code != 429 || len(f.inbox.messages) != 0 {
		t.Fatal("budget refusal completed the unadmitted call", code, len(f.inbox.messages))
	}
	refuse = false
	if code := f.invokeAs(t, inv, &caller); code != 200 || len(f.inbox.messages) != 0 {
		t.Fatal("same call cannot retry after capacity returns", code, len(f.inbox.messages))
	}
}
func TestTaskContinuationSurvivesLostAckRestartAndDisabledAgents(t *testing.T) {
	f, _ := newToolFixture(t)
	feed := &taskFeed{link: state.ExecutionTaskInvocation{AccountID: callbackAccount, ExecutionID: "run", Ref: api.ExecutionTaskRef{ID: "version", Name: "report", Version: 1}, Callback: &state.ExecutionTaskCallback{EndpointID: "ep-1", ThreadID: "thread", ToolCallID: "call", IssuedAt: f.h.Now()}}, row: state.Execution{ID: "run", Status: api.ExecutionStatusSucceeded, Result: json.RawMessage(`{"rows":3}`)}, markErr: errors.New("lost commit")}
	f.h.TaskCallbacks = feed
	f.h.TaskExecutions = feed
	f.h.AgentsEnabled = func(context.Context, string) bool { return false }
	if n, err := f.h.DeliverTaskCallbacks(t.Context()); err != nil || n != 0 || feed.done || len(f.inbox.messages) != 0 {
		t.Fatal("capability disable lost obligation", n, err)
	}
	f.h.AgentsEnabled = func(context.Context, string) bool { return true }
	if _, err := f.h.DeliverTaskCallbacks(t.Context()); err == nil || len(f.inbox.messages) != 1 {
		t.Fatal("lost acknowledgement not exercised", err)
	}
	fresh := &agents.Handler{Callbacks: f.h.Callbacks, Inbox: f.h.Inbox, Revocations: f.h.Revocations, AgentsEnabled: f.h.AgentsEnabled, TaskCallbacks: feed, TaskExecutions: feed}
	feed.markErr = nil
	if n, err := fresh.DeliverTaskCallbacks(t.Context()); err != nil || n != 1 || len(f.inbox.messages) != 1 || !feed.done {
		t.Fatal("restart duplicated continuation", n, err, len(f.inbox.messages))
	}
	if !strings.Contains(string(f.inbox.messages[0].Data), "agent-tools/runs/run") {
		t.Fatal("no scoped receipt reference")
	}
	caller := agents.CallbackCaller{Account: state.Account{ID: callbackAccount}, AppID: "other-app"}
	if _, p := fresh.TaskReceipt(t.Context(), caller, "run"); p == nil || p.Status != http.StatusNotFound {
		t.Fatal("wrong app read task receipt")
	}
}

func TestTaskContinuationBoundsResultAndSignalsInvalidContract(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		t.Run(map[bool]string{false: "oversized", true: "invalid_contract"}[invalid], func(t *testing.T) {
			f, _ := newToolFixture(t)
			feed := &taskFeed{link: state.ExecutionTaskInvocation{AccountID: callbackAccount, ExecutionID: "run", Ref: api.ExecutionTaskRef{Name: "report", Version: 1}, Callback: &state.ExecutionTaskCallback{EndpointID: "ep-1", ThreadID: "thread", ToolCallID: "call", IssuedAt: f.h.Now()}}, row: state.Execution{ID: "run", Status: api.ExecutionStatusSucceeded, Result: json.RawMessage(`"` + strings.Repeat("private-result", 1024) + `"`)}}
			if invalid {
				feed.row.ResultValidation = &api.ExecutionResultValidation{Status: "invalid", Message: "Run result does not match result_schema"}
			}
			f.h.TaskCallbacks = feed
			f.h.TaskExecutions = feed
			if n, err := f.h.DeliverTaskCallbacks(t.Context()); err != nil || n != 1 || len(f.inbox.messages) != 1 {
				t.Fatal("delivery", n, err)
			}
			var rap struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(f.inbox.messages[0].Data, &rap); err != nil {
				t.Fatal(err)
			}
			var result map[string]any
			if err := json.Unmarshal([]byte(rap.Text), &result); err != nil {
				t.Fatal(err)
			}
			if _, ok := result["result"]; ok {
				t.Fatal("oversized or invalid result leaked inline")
			}
			if invalid {
				if result["outcome"] != "result_schema_invalid" {
					t.Fatal("missing contract outcome")
				}
			} else if result["result_omitted"] != true {
				t.Fatal("missing result reference fallback")
			}
		})
	}
}

type pagedTaskFeed struct {
	links     []state.ExecutionTaskInvocation
	delivered map[string]bool
	row       state.Execution
}

func (f *pagedTaskFeed) PendingExecutionTaskCallbacks(_ context.Context, after string, limit int) ([]state.ExecutionTaskInvocation, error) {
	out := []state.ExecutionTaskInvocation{}
	for _, l := range f.links {
		if l.ExecutionID > after && !f.delivered[l.ExecutionID] {
			out = append(out, l)
			if len(out) == limit {
				break
			}
		}
	}
	return out, nil
}
func (f *pagedTaskFeed) MarkExecutionTaskCallbackDelivered(_ context.Context, _ string, id string) error {
	f.delivered[id] = true
	return nil
}
func (f *pagedTaskFeed) ExecutionTaskInvocationByID(context.Context, string, string) (state.ExecutionTaskInvocation, error) {
	return state.ExecutionTaskInvocation{}, state.ErrNotFound
}
func (f *pagedTaskFeed) ExecutionByID(context.Context, string, string) (state.Execution, error) {
	return f.row, nil
}
func TestTaskContinuationDisabledAccountsCannotStarveNextBatch(t *testing.T) {
	f, _ := newToolFixture(t)
	feed := &pagedTaskFeed{delivered: map[string]bool{}}
	for i := 0; i < api.ExecutionTaskCallbackBatch+1; i++ {
		account := "disabled"
		if i == api.ExecutionTaskCallbackBatch {
			account = callbackAccount
		}
		id := fmt.Sprintf("%036d", i)
		feed.links = append(feed.links, state.ExecutionTaskInvocation{ExecutionID: id, AccountID: account, Ref: api.ExecutionTaskRef{Name: "report", Version: 1}, Callback: &state.ExecutionTaskCallback{EndpointID: "ep-1", ThreadID: "thread", ToolCallID: "call", IssuedAt: f.h.Now()}})
		feed.row = state.Execution{ID: id, Status: api.ExecutionStatusSucceeded, Result: json.RawMessage(`42`)}
	}
	f.h.TaskCallbacks = feed
	f.h.TaskExecutions = feed
	f.h.AgentsEnabled = func(_ context.Context, account string) bool { return account == callbackAccount }
	if n, err := f.h.DeliverTaskCallbacks(t.Context()); n != 0 || err != nil {
		t.Fatal(n, err)
	}
	if n, err := f.h.DeliverTaskCallbacks(t.Context()); n != 1 || err != nil || len(f.inbox.messages) != 1 {
		t.Fatal("disabled accounts blocked the next page", n, err)
	}
}
