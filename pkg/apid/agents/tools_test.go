// adr: 20261030125501 §4 — gregale_sleep wakes only the thread that called
// it, through a callback URL apid issued for that thread; a revoked thread
// never wakes; every outcome reaches the agent as the call's tool result.
package agents_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// fakeSleeps keeps realtime schedules as state.PgStore does for the calls a
// sleep makes: a repeated creation returns the first row, a different one is
// ErrConflict, and a cancel needs the row's current version.
type fakeSleeps struct {
	mu    sync.Mutex
	rows  []state.ManagedRealtimeSchedule
	limit int
	// onPut runs after a row is created. moveOnce moves the next row a
	// cancel touches to a new version first, as a failed publish that
	// rescheduled it would.
	onPut    func()
	moveOnce bool
}

func (f *fakeSleeps) PutManagedRealtimeSchedule(_ context.Context, row state.ManagedRealtimeSchedule) (state.ManagedRealtimeSchedule, error) {
	f.mu.Lock()
	for _, old := range f.rows {
		if old.EndpointID == row.EndpointID && old.Channel == row.Channel && old.ID == row.ID {
			f.mu.Unlock()
			if bytes.Equal(old.Data, row.Data) && old.DeliverAt.Equal(row.DeliverAt) && fmt.Sprint(old.Metadata) == fmt.Sprint(row.Metadata) {
				return old, nil
			}
			return row, state.ErrConflict
		}
	}
	if f.limit > 0 && len(f.rows) >= f.limit {
		f.mu.Unlock()
		return row, state.ErrManagedRealtimeScheduleLimit
	}
	row.Version, row.Status = 1, "pending"
	f.rows = append(f.rows, row)
	onPut := f.onPut
	f.mu.Unlock()
	if onPut != nil {
		onPut()
	}
	return row, nil
}

func (f *fakeSleeps) ListManagedRealtimeSchedules(_ context.Context, ep, ch string) ([]state.ManagedRealtimeSchedule, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []state.ManagedRealtimeSchedule
	for _, row := range f.rows {
		if row.EndpointID == ep && row.Channel == ch {
			out = append(out, row)
		}
	}
	return out, nil
}

func (f *fakeSleeps) UpdateManagedRealtimeSchedule(_ context.Context, ep, ch, id string, version int64, deadline *time.Time) (state.ManagedRealtimeSchedule, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, row := range f.rows {
		if row.EndpointID != ep || row.Channel != ch || row.ID != id {
			continue
		}
		if f.moveOnce {
			f.moveOnce = false
			f.rows[i].Version++
		}
		if deadline != nil || f.rows[i].Version != version {
			return row, state.ErrConflict
		}
		f.rows[i].Status = "canceled"
		f.rows[i].Version++
		return f.rows[i], nil
	}
	return state.ManagedRealtimeSchedule{}, state.ErrNotFound
}

func (f *fakeSleeps) pending(thread string) []state.ManagedRealtimeSchedule {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []state.ManagedRealtimeSchedule
	for _, row := range f.rows {
		if row.Status == "pending" && row.Metadata["thread_id"] == thread {
			out = append(out, row)
		}
	}
	return out
}

func newToolFixture(t *testing.T) (*callbackFixture, *fakeSleeps) {
	t.Helper()
	f := newCallbackFixture(t)
	sleeps := &fakeSleeps{}
	f.h.Sleeps = sleeps
	return f, sleeps
}

// invocation is a RAP tool invocation of gregale_sleep, with every field
// RAP sends.
func invocation(token, thread, id string, args any) map[string]any {
	return map[string]any{"operation": "gregale_sleep", "arguments": args, "id": id, "call_id": nil,
		"callback_url": "https://api.example.test/v1/agent-callbacks/" + token, "group_id": thread,
		"thread_ancestors": []string{"root"}, "user_id": "user-1"}
}

func (f *callbackFixture) invoke(t *testing.T, inv map[string]any) int {
	t.Helper()
	body, _ := json.Marshal(inv)
	w := httptest.NewRecorder()
	f.h.InvokeTool(w, httptest.NewRequest(http.MethodPost, "/v1/agent-tools/invoke", bytes.NewReader(body)))
	return w.Code
}

// resultText is the text of a published RAP tool result.
func resultText(t *testing.T, m state.ManagedRealtimeChannelMessage) string {
	t.Helper()
	var result struct {
		Type, GroupID, ID, Text string
	}
	if err := json.Unmarshal(m.Data, &result); err != nil || result.Type != "tool_result" {
		t.Fatalf("not a RAP tool result: %s", m.Data)
	}
	return result.Text
}

func TestToolsetDescribesGregaleSleep(t *testing.T) {
	f, _ := newToolFixture(t)
	w := httptest.NewRecorder()
	f.h.Toolset(w, httptest.NewRequest(http.MethodGet, "/v1/agent-tools/.well-known/rap-toolset", nil))
	var toolset struct {
		Name, Endpoint string
		Tools          []struct {
			Name        string
			Description string
			InputSchema map[string]any `json:"inputSchema"`
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &toolset); err != nil || w.Code != http.StatusOK {
		t.Fatalf("toolset = %d %s", w.Code, w.Body)
	}
	if toolset.Name != "gregale" || toolset.Endpoint != "https://api.example.test/v1/agent-tools/invoke" || len(toolset.Tools) != 1 ||
		toolset.Tools[0].Name != "gregale_sleep" || toolset.Tools[0].InputSchema["type"] != "object" {
		t.Fatalf("toolset %+v", toolset)
	}

	f.h.Sleeps = nil
	w = httptest.NewRecorder()
	f.h.Toolset(w, httptest.NewRequest(http.MethodGet, "/v1/agent-tools/.well-known/rap-toolset", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("toolset without schedules = %d", w.Code)
	}
}

// A sleep is the call's tool result, scheduled on the thread's lane of the
// inbox: the message a RAP tool posting the same result would publish.
func TestSleepSchedulesTheToolResult(t *testing.T) {
	f, sleeps := newToolFixture(t)
	token, _ := f.issue(t, callbackAccount, api.CreateAgentCallbackRequest{EndpointID: "ep-1", ThreadID: "t-1"})
	if code := f.invoke(t, invocation(token, "t-1", "call_1", map[string]any{"seconds": 60, "reason": "wait for CI"})); code != http.StatusOK {
		t.Fatalf("invoke = %d", code)
	}
	rows := sleeps.pending("t-1")
	if len(rows) != 1 || len(f.inbox.messages) != 0 {
		t.Fatalf("sleeps %+v, inbox %+v", rows, f.inbox.messages)
	}
	row := rows[0]
	want := `{"type":"tool_result","group_id":"t-1","id":"call_1","text":"Slept for 60 seconds."}`
	if row.EndpointID != "ep-1" || row.Channel != api.AgentInboxChannel || !strings.HasPrefix(row.ID, "agent-sleep-") ||
		!row.DeliverAt.Equal(f.now.Add(time.Minute)) || string(row.Data) != want || row.MaxAttempts < 2 {
		t.Fatalf("sleep %+v", row)
	}
	if code, _ := f.postRaw(t, token, want); code != http.StatusAccepted {
		t.Fatalf("RAP post = %d", code)
	}
	if got := f.inbox.messages[0]; string(got.Data) != string(row.Data) || fmt.Sprint(got.Metadata) != fmt.Sprint(row.Metadata) {
		t.Fatalf("a RAP tool's post %s %v; the sleep's %s %v", got.Data, got.Metadata, row.Data, row.Metadata)
	}

	// The runtime retries an invocation it did not see acknowledged, later.
	f.now = f.now.Add(5 * time.Second)
	if code := f.invoke(t, invocation(token, "t-1", "call_1", map[string]any{"seconds": 60})); code != http.StatusOK {
		t.Fatalf("retried invoke = %d", code)
	}
	if rows := sleeps.pending("t-1"); len(rows) != 1 || !rows[0].DeliverAt.Equal(row.DeliverAt) {
		t.Fatalf("a retry moved or doubled the sleep: %+v", rows)
	}
}

func TestSleepUntil(t *testing.T) {
	f, sleeps := newToolFixture(t)
	token, _ := f.issue(t, callbackAccount, api.CreateAgentCallbackRequest{EndpointID: "ep-1", ThreadID: "t-1"})
	at := f.now.Add(time.Hour).UTC().Format(time.RFC3339)
	if code := f.invoke(t, invocation(token, "t-1", "call_1", map[string]any{"until": at})); code != http.StatusOK {
		t.Fatalf("invoke = %d", code)
	}
	if rows := sleeps.pending("t-1"); len(rows) != 1 || !rows[0].DeliverAt.Equal(f.now.Add(time.Hour)) ||
		!strings.Contains(string(rows[0].Data), "Slept until "+at) {
		t.Fatalf("sleeps %+v", rows)
	}

	past := f.now.Add(-time.Minute).UTC().Format(time.RFC3339)
	if code := f.invoke(t, invocation(token, "t-1", "call_2", map[string]any{"until": past})); code != http.StatusOK {
		t.Fatalf("invoke = %d", code)
	}
	if len(f.inbox.messages) != 1 || resultText(t, f.inbox.messages[0]) != past+" has passed; woke at once." ||
		f.inbox.messages[0].Metadata["message_id"] != "rap-tool-result-call_2" || len(sleeps.pending("t-1")) != 1 {
		t.Fatalf("a passed time: inbox %+v", f.inbox.messages)
	}
}

// RAP delivers an invocation's errors as its tool result, so the agent can
// reason about them; the invocation itself is acknowledged.
func TestSleepErrorsAreToolResults(t *testing.T) {
	f, sleeps := newToolFixture(t)
	token, _ := f.issue(t, callbackAccount, api.CreateAgentCallbackRequest{EndpointID: "ep-1", ThreadID: "t-1"})
	unknown := invocation(token, "t-1", "call_op", map[string]any{"seconds": 5})
	unknown["operation"] = "gregale_nap"
	for i, tc := range []struct {
		name string
		inv  map[string]any
		want string
	}{
		{"no arguments", invocation(token, "t-1", "c0", nil), "gregale_sleep: arguments must be an object"},
		{"empty arguments", invocation(token, "t-1", "c1", map[string]any{}), "gregale_sleep: give exactly one of seconds and until"},
		{"both", invocation(token, "t-1", "c2", map[string]any{"seconds": 5, "until": "2027-01-01T00:00:00Z"}), "gregale_sleep: give exactly one"},
		{"zero seconds", invocation(token, "t-1", "c3", map[string]any{"seconds": 0}), "gregale_sleep: seconds must be 1-2592000"},
		{"past the limit", invocation(token, "t-1", "c4", map[string]any{"seconds": api.AgentSleepMaxSeconds + 1}), "gregale_sleep: seconds must be"},
		{"not a time", invocation(token, "t-1", "c5", map[string]any{"until": "tomorrow"}), "gregale_sleep: until must be an RFC 3339 time"},
		{"too far ahead", invocation(token, "t-1", "c6", map[string]any{"until": f.now.Add(31 * 24 * time.Hour).Format(time.RFC3339)}), "gregale_sleep: until must be at most"},
		{"unknown argument", invocation(token, "t-1", "c7", map[string]any{"secs": 5}), "gregale_sleep: arguments must be an object"},
		{"unknown operation", unknown, `unknown operation "gregale_nap"`},
	} {
		if code := f.invoke(t, tc.inv); code != http.StatusOK {
			t.Errorf("%s: invoke = %d", tc.name, code)
			continue
		}
		if len(f.inbox.messages) != i+1 || !strings.HasPrefix(resultText(t, f.inbox.messages[i]), tc.want) {
			t.Errorf("%s: inbox %d, want a result starting %q", tc.name, len(f.inbox.messages), tc.want)
		}
	}
	if len(sleeps.rows) != 0 {
		t.Fatalf("a refused sleep was scheduled: %+v", sleeps.rows)
	}

	sleeps.limit = 1
	sleeps.rows = []state.ManagedRealtimeSchedule{{EndpointID: "ep-1", Channel: "jobs", ID: "app-own"}}
	if code := f.invoke(t, invocation(token, "t-1", "c9", map[string]any{"seconds": 5})); code != http.StatusOK {
		t.Fatalf("invoke at the schedule limit = %d", code)
	}
	if last := f.inbox.messages[len(f.inbox.messages)-1]; !strings.Contains(resultText(t, last), "256 realtime schedules") {
		t.Fatalf("at the schedule limit: %s", last.Data)
	}
}

// An invocation that apid cannot answer through a callback URL of its own
// thread is refused, and schedules and publishes nothing.
func TestSleepInvocationRefusals(t *testing.T) {
	f, sleeps := newToolFixture(t)
	token, _ := f.issue(t, callbackAccount, api.CreateAgentCallbackRequest{EndpointID: "ep-1", ThreadID: "t-1"})
	events, _ := f.issue(t, callbackAccount, api.CreateAgentCallbackRequest{EndpointID: "ep-1", ThreadID: "t-1", Kinds: []string{"event"}})
	closed, _ := f.issue(t, callbackAccount, api.CreateAgentCallbackRequest{EndpointID: "ep-1", ThreadID: "t-closed"})
	f.now = f.now.Add(time.Second)
	if code := f.revoke(t, callbackAccount, "ep-1", "t-closed"); code != http.StatusOK {
		t.Fatalf("revoke = %d", code)
	}
	disabled, _ := f.issue(t, otherAccount, api.CreateAgentCallbackRequest{EndpointID: "ep-other", ThreadID: "t-1"})
	f.enabled[otherAccount] = false
	args := map[string]any{"seconds": 5}
	elsewhere := invocation(token, "t-1", "c", args)
	elsewhere["callback_url"] = "https://agent.example.com/v1/agent-callbacks/" + token
	suffixed := invocation(token+"/x", "t-1", "c", args)
	noID := invocation(token, "t-1", "", args)
	for _, tc := range []struct {
		name string
		inv  map[string]any
		want int
	}{
		{"another host's callback URL", elsewhere, http.StatusNotFound},
		{"a path below the token", suffixed, http.StatusNotFound},
		{"a forged token", invocation("agcb1.e30.AAAA", "t-1", "c", args), http.StatusNotFound},
		{"a revoked thread", invocation(closed, "t-closed", "c", args), http.StatusNotFound},
		{"an account without agents", invocation(disabled, "t-1", "c", args), http.StatusNotFound},
		{"another thread's group_id", invocation(token, "t-2", "c", args), http.StatusForbidden},
		{"a URL without tool results", invocation(events, "t-1", "c", args), http.StatusForbidden},
		{"no call id", noID, http.StatusBadRequest},
	} {
		if code := f.invoke(t, tc.inv); code != tc.want {
			t.Errorf("%s: invoke = %d, want %d", tc.name, code, tc.want)
		}
	}
	w := httptest.NewRecorder()
	f.h.InvokeTool(w, httptest.NewRequest(http.MethodPost, "/v1/agent-tools/invoke", strings.NewReader("not json")))
	if w.Code != http.StatusBadRequest {
		t.Errorf("not JSON: invoke = %d", w.Code)
	}
	if len(sleeps.rows) != 0 || len(f.inbox.messages) != 0 {
		t.Fatalf("a refused invocation left sleeps %+v or messages %+v", sleeps.rows, f.inbox.messages)
	}
}

// Revoking a thread cancels its sleeps and no other thread's, also when a
// sleep's version moves under the cancel.
func TestRevocationCancelsSleeps(t *testing.T) {
	f, sleeps := newToolFixture(t)
	for _, thread := range []string{"t-1", "t-2"} {
		token, _ := f.issue(t, callbackAccount, api.CreateAgentCallbackRequest{EndpointID: "ep-1", ThreadID: thread})
		for _, id := range []string{"a", "b"} {
			if code := f.invoke(t, invocation(token, thread, id, map[string]any{"seconds": 60})); code != http.StatusOK {
				t.Fatalf("invoke = %d", code)
			}
		}
	}
	sleeps.moveOnce = true
	f.now = f.now.Add(time.Second)
	if code := f.revoke(t, callbackAccount, "ep-1", "t-1"); code != http.StatusOK {
		t.Fatalf("revoke = %d", code)
	}
	if got, other := sleeps.pending("t-1"), sleeps.pending("t-2"); len(got) != 0 || len(other) != 2 {
		t.Fatalf("after revoking t-1: t-1 %+v, t-2 %d pending", got, len(other))
	}
}

// A revocation that lists the thread's sleeps just before a new one is
// created cannot cancel it; the sleep sees the revocation and cancels
// itself.
func TestSleepRacingARevocationCancelsItself(t *testing.T) {
	f, sleeps := newToolFixture(t)
	token, _ := f.issue(t, callbackAccount, api.CreateAgentCallbackRequest{EndpointID: "ep-1", ThreadID: "t-1"})
	f.now = f.now.Add(time.Second)
	sleeps.onPut = func() {
		sleeps.onPut = nil
		if _, err := f.h.Revocations.RevokeCallbacks(context.Background(), callbackAccount, "ep-1", "t-1"); err != nil {
			t.Error(err)
		}
	}
	if code := f.invoke(t, invocation(token, "t-1", "c", map[string]any{"seconds": 60})); code != http.StatusNotFound {
		t.Fatalf("invoke racing a revocation = %d", code)
	}
	if got := sleeps.pending("t-1"); len(got) != 0 {
		t.Fatalf("a sleep survived its thread's revocation: %+v", got)
	}
}

// Over random issues, sleeps and revocations, no sleep is pending for a
// thread unless the URL it came through was issued after the thread's
// latest revocation.
func TestPropertyRevokedThreadNeverWakes(t *testing.T) {
	r := rand.New(rand.NewPCG(20261102, 4))
	f, sleeps := newToolFixture(t)
	type grant struct {
		token, thread string
		issued        time.Time
	}
	var grants []grant
	revoked := map[string]time.Time{}
	issuedBy := map[string]time.Time{}
	for i := 0; i < 400; i++ {
		f.now = f.now.Add(time.Duration(1+r.IntN(5)) * time.Millisecond)
		thread := fmt.Sprintf("t%d", r.IntN(3))
		switch op := r.IntN(10); {
		case op < 3 || len(grants) == 0:
			token, _ := f.issue(t, callbackAccount, api.CreateAgentCallbackRequest{EndpointID: "ep-1", ThreadID: thread})
			grants = append(grants, grant{token, thread, f.now})
		case op < 5:
			if code := f.revoke(t, callbackAccount, "ep-1", thread); code != http.StatusOK {
				t.Fatalf("revoke = %d", code)
			}
			revoked[thread] = f.now
		default:
			g := grants[r.IntN(len(grants))]
			id := fmt.Sprintf("c%d", i)
			if code := f.invoke(t, invocation(g.token, g.thread, id, map[string]any{"seconds": 1 + r.IntN(600)})); code == http.StatusOK {
				issuedBy[id] = g.issued
			}
		}
		for thread, at := range revoked {
			for _, row := range sleeps.pending(thread) {
				var result struct{ ID string }
				_ = json.Unmarshal(row.Data, &result)
				if !issuedBy[result.ID].After(at) {
					t.Fatalf("step %d: %s sleeps through a URL issued %v, before its revocation %v", i, thread, issuedBy[result.ID], at)
				}
			}
		}
	}
}

// The sleep's schedule passes the real realtime store's checks, and a
// revocation cancels it there.
func TestSleepOnTheRealtimeStore(t *testing.T) {
	ctx := context.Background()
	f := newCallbackFixture(t)
	f.now = time.Now()
	store := state.NewMemStore()
	app, err := store.CreateApp(ctx, state.App{AccountID: callbackAccount, Slug: "agent", Type: state.AppTypeApp, Runtime: "node22", RAMMB: 128, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	endpoint, err := store.CreateManagedRealtimeEndpointIfUnderQuota(ctx, state.ManagedRealtimeEndpoint{
		ID: "ep-mem", AccountID: callbackAccount, AppID: app.ID, Enabled: true}, 10, 10)
	if err != nil {
		t.Fatal(err)
	}
	f.inbox.endpoints[endpoint.ID] = endpoint
	f.h.Sleeps = store
	token, _ := f.issue(t, callbackAccount, api.CreateAgentCallbackRequest{EndpointID: endpoint.ID, ThreadID: "t-1"})
	if code := f.invoke(t, invocation(token, "t-1", "call_1", map[string]any{"seconds": 90})); code != http.StatusOK {
		t.Fatalf("invoke = %d", code)
	}
	rows, err := store.ListManagedRealtimeSchedules(ctx, endpoint.ID, api.AgentInboxChannel)
	if err != nil || len(rows) != 1 || rows[0].Status != "pending" || rows[0].Metadata["message_id"] != "rap-tool-result-call_1" {
		t.Fatalf("schedules %+v, %v", rows, err)
	}
	f.now = time.Now()
	if code := f.revoke(t, callbackAccount, endpoint.ID, "t-1"); code != http.StatusOK {
		t.Fatalf("revoke = %d", code)
	}
	if rows, err = store.ListManagedRealtimeSchedules(ctx, endpoint.ID, api.AgentInboxChannel); err != nil || rows[0].Status != "canceled" {
		t.Fatalf("after revocation %+v, %v", rows, err)
	}
}

func TestCloseThreadIsAcknowledged(t *testing.T) {
	f, _ := newToolFixture(t)
	w := httptest.NewRecorder()
	f.h.CloseThread(w, httptest.NewRequest(http.MethodPost, "/v1/agent-tools/close_thread", strings.NewReader(`{"thread_id":"t-1"}`)))
	if w.Code != http.StatusOK || w.Body.Len() != 0 {
		t.Fatalf("close_thread = %d %q", w.Code, w.Body)
	}
}
