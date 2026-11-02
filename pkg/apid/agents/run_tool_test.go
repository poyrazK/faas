// adr: 20261030125501 §4 — gregale_run starts an agent run only for the
// app's own credential, never for a callback URL alone; the run answers
// only its own thread and call, once, and a revoked thread receives nothing.
package agents_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/agents"
	"github.com/onebox-faas/faas/pkg/state"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
)

// fakeRunCallbacks is the finished-run feed: ends not yet marked delivered.
type fakeRunCallbacks struct {
	ends      []agentstore.RunCallbackEnd
	delivered map[string]bool
	markErr   error
}

func (f *fakeRunCallbacks) UndeliveredRunCallbacks(_ context.Context, limit int) ([]agentstore.RunCallbackEnd, error) {
	var out []agentstore.RunCallbackEnd
	for _, end := range f.ends {
		if !f.delivered[end.RunID] && len(out) < limit {
			out = append(out, end)
		}
	}
	return out, nil
}

func (f *fakeRunCallbacks) MarkRunCallbackDelivered(_ context.Context, runID string) error {
	if f.markErr != nil {
		return f.markErr
	}
	f.delivered[runID] = true
	return nil
}

// failingInbox fails every publish, as a database outage would.
type failingInbox struct{ *fakeInbox }

func (failingInbox) AppendManagedRealtimeChannelMetadata(context.Context, string, string, []byte, bool, string, map[string]string) (state.ManagedRealtimeChannelMessage, error) {
	return state.ManagedRealtimeChannelMessage{}, errors.New("connection refused")
}

func newRunFixture(t *testing.T) (*callbackFixture, *fakeStore, *fakeRunCallbacks) {
	t.Helper()
	f, _ := newToolFixture(t)
	store := &fakeStore{}
	runs := &fakeRunCallbacks{delivered: map[string]bool{}}
	f.h.Store, f.h.RunCallbacks = store, runs
	f.h.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	f.h.Audit = func(_ context.Context, action, _ string, _ map[string]any) {
		store.audits = append(store.audits, action)
	}
	return f, store, runs
}

func runInvocation(token, thread, id string, args any) map[string]any {
	inv := invocation(token, thread, id, args)
	inv["operation"] = "gregale_run"
	inv["call_id"] = "prov-" + id
	return inv
}

// The callback URL names the thread; the app's credential starts the run.
// Tool servers hold callback URLs, so one alone starts nothing.
func TestRunNeedsTheAppsOwnCredential(t *testing.T) {
	f, store, _ := newRunFixture(t)
	token, _ := f.issue(t, callbackAccount, api.CreateAgentCallbackRequest{EndpointID: "ep-1", ThreadID: "t-1"})
	args := map[string]any{"agent": "reviewer", "prompt": "review the diff"}
	app := agents.CallbackCaller{Account: state.Account{ID: callbackAccount, Plan: api.PlanPro}, AppID: "app-1"}
	otherApp := agents.CallbackCaller{Account: state.Account{ID: callbackAccount, Plan: api.PlanPro}, AppID: "app-2"}
	otherAccount := keyCaller(otherAccount)
	for _, tc := range []struct {
		name   string
		caller *agents.CallbackCaller
		want   int
	}{
		{"the callback URL alone", nil, http.StatusUnauthorized},
		{"another app's workload token", &otherApp, http.StatusNotFound},
		{"another account's key", &otherAccount, http.StatusNotFound},
	} {
		if code := f.invokeAs(t, runInvocation(token, "t-1", "c", args), tc.caller); code != tc.want {
			t.Errorf("%s: invoke = %d, want %d", tc.name, code, tc.want)
		}
	}
	if store.admission.Agent != "" || len(f.inbox.messages) != 0 {
		t.Fatalf("a refused invocation admitted %+v or published %+v", store.admission, f.inbox.messages)
	}
	if code := f.invokeAs(t, runInvocation(token, "t-1", "c", args), &app); code != http.StatusOK {
		t.Fatalf("the app's own workload token: invoke = %d", code)
	}
}

func TestRunIsAdmittedForItsCall(t *testing.T) {
	f, store, _ := newRunFixture(t)
	issued := f.now
	token, _ := f.issue(t, callbackAccount, api.CreateAgentCallbackRequest{EndpointID: "ep-1", ThreadID: "t-1"})
	f.now = f.now.Add(time.Minute)
	caller := keyCaller(callbackAccount)
	args := map[string]any{"agent": "reviewer", "version": 2, "prompt": "review the diff", "inputs": map[string]any{"pr": 7}}
	if code := f.invokeAs(t, runInvocation(token, "t-1", "call_1", args), &caller); code != http.StatusOK {
		t.Fatalf("invoke = %d", code)
	}
	a := store.admission
	if a.AccountID != callbackAccount || a.Agent != "reviewer" || a.Version != 2 || a.Prompt == nil || *a.Prompt != "review the diff" ||
		string(a.Inputs) != `{"pr":7}` || a.Trigger != "api" || !strings.HasPrefix(a.DedupeKey, "gregale-run-") ||
		a.Limits != agentstore.PlanLimits(api.PlanPro) {
		t.Fatalf("admission %+v", a)
	}
	if c := a.Callback; c == nil || c.EndpointID != "ep-1" || c.ThreadID != "t-1" || c.ToolCallID != "call_1" ||
		c.ProviderCallID != "prov-call_1" || !c.IssuedAt.Equal(issued) {
		t.Fatalf("callback link %+v", a.Callback)
	}
	if len(store.audits) != 1 || store.audits[0] != "agent_run.created" || len(f.inbox.messages) != 0 {
		t.Fatalf("audits %v, inbox %+v", store.audits, f.inbox.messages)
	}

	first := a.DedupeKey
	store.existing = true
	if code := f.invokeAs(t, runInvocation(token, "t-1", "call_1", args), &caller); code != http.StatusOK || store.admission.DedupeKey != first {
		t.Fatalf("a retried invocation: %d, dedupe key %q, want %q", code, store.admission.DedupeKey, first)
	}
	if len(store.audits) != 1 {
		t.Fatalf("a retry audited a second run: %v", store.audits)
	}
	other, _ := f.issue(t, callbackAccount, api.CreateAgentCallbackRequest{EndpointID: "ep-1", ThreadID: "t-2"})
	if f.invokeAs(t, runInvocation(other, "t-2", "call_1", args), &caller); store.admission.DedupeKey == first {
		t.Fatal("another thread's call of the same ID shares the run")
	}
}

// Refusals the agent can act on reach it as the call's result; a store
// failure is a 5xx, which the runtime retries.
func TestRunRefusalsAreToolResults(t *testing.T) {
	f, store, _ := newRunFixture(t)
	token, _ := f.issue(t, callbackAccount, api.CreateAgentCallbackRequest{EndpointID: "ep-1", ThreadID: "t-1"})
	caller := keyCaller(callbackAccount)
	for i, tc := range []struct {
		name string
		args any
		err  error
		want string
	}{
		{"no agent", map[string]any{"prompt": "x"}, nil, "gregale_run: arguments must be an object with an agent"},
		{"an unknown argument", map[string]any{"agent": "a", "checkout": map[string]any{}}, nil, "gregale_run: arguments must be"},
		{"inputs that are not an object", map[string]any{"agent": "a", "inputs": []int{1}}, nil, "gregale_run: inputs must be a JSON object"},
		{"an unknown agent", map[string]any{"agent": "nobody"}, agentstore.ErrNotFound, "gregale_run: Agent not found"},
	} {
		store.err = tc.err
		if code := f.invokeAs(t, runInvocation(token, "t-1", "c"+string(rune('0'+i)), tc.args), &caller); code != http.StatusOK {
			t.Errorf("%s: invoke = %d", tc.name, code)
			continue
		}
		if len(f.inbox.messages) != i+1 || !strings.HasPrefix(resultText(t, f.inbox.messages[i]), tc.want) {
			t.Errorf("%s: inbox %d, want a result starting %q", tc.name, len(f.inbox.messages), tc.want)
		}
	}
	store.err = errors.New("connection refused")
	if code := f.invokeAs(t, runInvocation(token, "t-1", "c9", map[string]any{"agent": "a"}), &caller); code != http.StatusInternalServerError {
		t.Fatalf("a store failure: invoke = %d, want 500 so the runtime retries", code)
	}
}

func runEnd(runID, thread, call string, issued time.Time) agentstore.RunCallbackEnd {
	return agentstore.RunCallbackEnd{RunID: runID, AccountID: callbackAccount, State: agentstore.StateSucceeded,
		Callback: agentstore.RunCallback{EndpointID: "ep-1", ThreadID: thread, ToolCallID: call, ProviderCallID: "prov-" + call, IssuedAt: issued}}
}

// runResult is the JSON text of a delivered run's tool result.
func runResult(t *testing.T, m state.ManagedRealtimeChannelMessage) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(resultText(t, m)), &out); err != nil {
		t.Fatalf("run result text is not JSON: %s", m.Data)
	}
	return out
}

// A finished run reaches its thread once, as its call's RAP tool result.
func TestRunEndReachesItsThreadOnce(t *testing.T) {
	f, _, runs := newRunFixture(t)
	issued := f.now
	ok := runEnd("run-ok", "t-1", "call_1", issued)
	ok.Result = json.RawMessage(`{"approved":true}`)
	failed := runEnd("run-failed", "t-2", "call_2", issued)
	failed.State, failed.OutcomeCode, failed.OutcomeMessage = agentstore.StateFailed, "timeout", strings.Repeat("x", 5000)
	big := runEnd("run-big", "t-3", "call_3", issued)
	big.Result = json.RawMessage(`"` + strings.Repeat("y", api.AgentCallbackPayloadMaxBytes) + `"`)
	runs.ends = []agentstore.RunCallbackEnd{ok, failed, big}
	if settled, err := f.h.DeliverRunCallbacks(context.Background()); err != nil || settled != 3 {
		t.Fatalf("deliver = %d, %v", settled, err)
	}
	if len(f.inbox.messages) != 3 {
		t.Fatalf("inbox %+v", f.inbox.messages)
	}
	m := f.inbox.messages[0]
	if m.Metadata["thread_id"] != "t-1" || m.Metadata["kind"] != api.AgentCallbackKindToolResult || m.Metadata["message_id"] != "rap-tool-result-call_1" ||
		!strings.Contains(string(m.Data), `"call_id":"prov-call_1"`) {
		t.Fatalf("message %+v %s", m.Metadata, m.Data)
	}
	if r := runResult(t, m); r["run_id"] != "run-ok" || r["state"] != "succeeded" || r["result"].(map[string]any)["approved"] != true {
		t.Fatalf("succeeded run %v", r)
	}
	if r := runResult(t, f.inbox.messages[1]); r["state"] != "failed" || r["result"] != nil ||
		r["outcome"].(map[string]any)["code"] != "timeout" || len(r["outcome"].(map[string]any)["message"].(string)) > 1024 {
		t.Fatalf("failed run %v", r)
	}
	if r := runResult(t, f.inbox.messages[2]); r["result"] != nil || r["result_omitted"] != true {
		t.Fatalf("a result too large for one message %v", r)
	}

	// A pass that published and then failed to mark publishes again: the
	// message lands once.
	again := runEnd("run-again", "t-4", "call_4", issued)
	runs.ends, runs.markErr = []agentstore.RunCallbackEnd{again}, errors.New("connection reset")
	if _, err := f.h.DeliverRunCallbacks(context.Background()); err == nil {
		t.Fatal("a failed mark was not reported")
	}
	runs.markErr = nil
	if settled, err := f.h.DeliverRunCallbacks(context.Background()); err != nil || settled != 1 || len(f.inbox.messages) != 4 || !runs.delivered["run-again"] {
		t.Fatalf("redelivery: %d, %v, inbox %d", settled, err, len(f.inbox.messages))
	}
}

// A run's end is dropped, and settled, when its thread can no longer take
// it; a failed publish is kept and retried.
func TestRunEndDroppedOrRetried(t *testing.T) {
	f, _, runs := newRunFixture(t)
	issued := f.now
	f.now = f.now.Add(time.Second)
	if code := f.revoke(t, callbackAccount, "ep-1", "t-closed"); code != http.StatusOK {
		t.Fatalf("revoke = %d", code)
	}
	gone := runEnd("run-gone", "t-1", "c", issued)
	gone.Callback.EndpointID = "ep-deleted"
	disabled := runEnd("run-disabled", "t-1", "c", issued)
	disabled.AccountID = otherAccount
	disabled.Callback.EndpointID = "ep-other"
	f.enabled[otherAccount] = false
	runs.ends = []agentstore.RunCallbackEnd{runEnd("run-revoked", "t-closed", "c", issued), gone, disabled}
	if settled, err := f.h.DeliverRunCallbacks(context.Background()); err != nil || settled != 3 || len(f.inbox.messages) != 0 {
		t.Fatalf("deliver = %d, %v, inbox %+v", settled, err, f.inbox.messages)
	}

	runs.ends = []agentstore.RunCallbackEnd{runEnd("run-later", "t-1", "c", issued)}
	f.h.Inbox = failingInbox{f.inbox}
	if settled, err := f.h.DeliverRunCallbacks(context.Background()); err == nil || settled != 0 || runs.delivered["run-later"] {
		t.Fatalf("a failed publish: %d, %v, delivered %v", settled, err, runs.delivered["run-later"])
	}
	f.h.Inbox = f.inbox
	if settled, err := f.h.DeliverRunCallbacks(context.Background()); err != nil || settled != 1 || len(f.inbox.messages) != 1 {
		t.Fatalf("the retry: %d, %v, inbox %d", settled, err, len(f.inbox.messages))
	}
}
