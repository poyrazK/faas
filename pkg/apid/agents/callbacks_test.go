// adr: 20261030125501 §3 — a callback token publishes only into its own
// thread of its own endpoint's agent inbox, an expired, forged or disabled
// one is refused like an unknown route, and a repeated message_id lands once.
package agents_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/agents"
	"github.com/onebox-faas/faas/pkg/state"
)

// fakeInbox is the realtime history the callbacks publish into. A repeated
// idempotency key returns the first message, or ErrConflict when the data
// or metadata differ, as state.PgStore does.
type fakeInbox struct {
	mu        sync.Mutex
	endpoints map[string]state.ManagedRealtimeEndpoint
	messages  []state.ManagedRealtimeChannelMessage
}

func (f *fakeInbox) ManagedRealtimeEndpointByID(_ context.Context, id string) (state.ManagedRealtimeEndpoint, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.endpoints[id]
	if !ok {
		return state.ManagedRealtimeEndpoint{}, state.ErrNotFound
	}
	return e, nil
}

func (f *fakeInbox) AppendManagedRealtimeChannelMetadata(_ context.Context, ep, ch string, data []byte, _ bool, key string, md map[string]string) (state.ManagedRealtimeChannelMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, m := range f.messages {
		if m.EndpointID == ep && m.Channel == ch && m.IdempotencyKey == key {
			if !bytes.Equal(m.Data, data) || fmt.Sprint(m.Metadata) != fmt.Sprint(md) {
				return state.ManagedRealtimeChannelMessage{}, state.ErrConflict
			}
			return m, nil
		}
	}
	m := state.ManagedRealtimeChannelMessage{EndpointID: ep, Channel: ch, Data: data, IdempotencyKey: key, Metadata: md,
		Sequence: int64(len(f.messages) + 1)}
	f.messages = append(f.messages, m)
	return m, nil
}

// fakeRevocations keeps each thread's latest revocation, at the fixture's
// clock.
type fakeRevocations struct {
	now func() time.Time
	at  map[string]time.Time
}

func (f *fakeRevocations) RevokeCallbacks(_ context.Context, _, endpointID, threadID string) (time.Time, error) {
	t := f.now()
	f.at[endpointID+"/"+threadID] = t
	return t, nil
}

func (f *fakeRevocations) CallbacksRevokedAt(_ context.Context, endpointID, threadID string) (time.Time, bool, error) {
	t, ok := f.at[endpointID+"/"+threadID]
	return t, ok, nil
}

const (
	callbackAccount = "11111111-1111-1111-1111-111111111111"
	otherAccount    = "22222222-2222-2222-2222-222222222222"
)

type callbackFixture struct {
	h     *agents.Handler
	inbox *fakeInbox
	now   time.Time
	// enabled is the agents capability per account.
	enabled map[string]bool
}

func newCallbackFixture(t *testing.T) *callbackFixture {
	t.Helper()
	signer, err := agents.NewCallbackSigner(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	f := &callbackFixture{inbox: &fakeInbox{endpoints: map[string]state.ManagedRealtimeEndpoint{
		"ep-1": {ID: "ep-1", AccountID: callbackAccount, AppID: "app-1"}, "ep-2": {ID: "ep-2", AccountID: callbackAccount, AppID: "app-2"},
		"ep-other": {ID: "ep-other", AccountID: otherAccount, AppID: "app-other"},
	}}, now: time.Unix(1_800_000_000, 0), enabled: map[string]bool{callbackAccount: true, otherAccount: true}}
	clock := func() time.Time { return f.now }
	f.h = &agents.Handler{Callbacks: signer, Inbox: f.inbox, CallbackBaseURL: "https://api.example.test",
		AgentsEnabled: func(_ context.Context, id string) bool { return f.enabled[id] }, Now: clock,
		Revocations: &fakeRevocations{now: clock, at: map[string]time.Time{}}}
	return f
}

// issue asks for a callback URL with the account's API key and returns its
// token, or the status.
func (f *callbackFixture) issue(t *testing.T, accountID string, req api.CreateAgentCallbackRequest) (string, int) {
	t.Helper()
	return f.issueAs(t, keyCaller(accountID), req)
}

func keyCaller(accountID string) agents.CallbackCaller {
	return agents.CallbackCaller{Account: state.Account{ID: accountID, Plan: api.PlanPro}}
}

func (f *callbackFixture) issueAs(t *testing.T, caller agents.CallbackCaller, req api.CreateAgentCallbackRequest) (string, int) {
	t.Helper()
	body, _ := json.Marshal(req)
	w := httptest.NewRecorder()
	f.h.CreateCallback(w, httptest.NewRequest(http.MethodPost, "/v1/agent-callbacks", bytes.NewReader(body)), caller)
	if w.Code != http.StatusCreated {
		return "", w.Code
	}
	var out api.AgentCallbackResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	token, ok := strings.CutPrefix(out.URL, "https://api.example.test/v1/agent-callbacks/")
	if !ok {
		t.Fatalf("url %q", out.URL)
	}
	return token, w.Code
}

// post sends one callback and returns the status and receipt.
func (f *callbackFixture) post(t *testing.T, token string, post api.AgentCallbackPost) (int, api.AgentCallbackReceipt) {
	t.Helper()
	body, _ := json.Marshal(post)
	// A tampered token need not be a valid URL; the handler reads the path value.
	r := httptest.NewRequest(http.MethodPost, "/v1/agent-callbacks/token", bytes.NewReader(body))
	r.SetPathValue("token", token)
	w := httptest.NewRecorder()
	f.h.ReceiveCallback(w, r)
	var receipt api.AgentCallbackReceipt
	_ = json.Unmarshal(w.Body.Bytes(), &receipt)
	return w.Code, receipt
}

func toolResult(id string, data string) api.AgentCallbackPost {
	return api.AgentCallbackPost{MessageID: id, Kind: api.AgentCallbackKindToolResult, Data: json.RawMessage(data)}
}

func TestCallbackPublishesIntoItsThread(t *testing.T) {
	f := newCallbackFixture(t)
	token, code := f.issue(t, callbackAccount, api.CreateAgentCallbackRequest{EndpointID: "ep-1", ThreadID: "thread-7"})
	if code != http.StatusCreated {
		t.Fatalf("issue = %d", code)
	}
	status, receipt := f.post(t, token, toolResult("call-1", `{"ok":true}`))
	if status != http.StatusAccepted || receipt.Sequence != "1" || receipt.ThreadID != "thread-7" {
		t.Fatalf("post = %d %+v", status, receipt)
	}
	m := f.inbox.messages[0]
	if m.EndpointID != "ep-1" || m.Channel != api.AgentInboxChannel || m.Metadata["thread_id"] != "thread-7" ||
		m.Metadata["kind"] != api.AgentCallbackKindToolResult || m.Metadata["message_id"] != "call-1" || string(m.Data) != `{"ok":true}` {
		t.Fatalf("message %+v", m)
	}
}

func TestCallbackIssueRules(t *testing.T) {
	f := newCallbackFixture(t)
	for _, tc := range []struct {
		name    string
		account string
		req     api.CreateAgentCallbackRequest
		want    int
	}{
		{"another account's endpoint", callbackAccount, api.CreateAgentCallbackRequest{EndpointID: "ep-other", ThreadID: "t"}, http.StatusNotFound},
		{"unknown endpoint", callbackAccount, api.CreateAgentCallbackRequest{EndpointID: "ep-9", ThreadID: "t"}, http.StatusNotFound},
		{"bad thread", callbackAccount, api.CreateAgentCallbackRequest{EndpointID: "ep-1", ThreadID: "a b"}, http.StatusBadRequest},
		{"long thread", callbackAccount, api.CreateAgentCallbackRequest{EndpointID: "ep-1", ThreadID: strings.Repeat("t", api.AgentCallbackThreadIDMaxBytes+1)}, http.StatusBadRequest},
		{"unknown kind", callbackAccount, api.CreateAgentCallbackRequest{EndpointID: "ep-1", ThreadID: "t", Kinds: []string{"shell"}}, http.StatusBadRequest},
		{"repeated kind", callbackAccount, api.CreateAgentCallbackRequest{EndpointID: "ep-1", ThreadID: "t", Kinds: []string{"event", "event"}}, http.StatusBadRequest},
		{"lifetime past the limit", callbackAccount, api.CreateAgentCallbackRequest{EndpointID: "ep-1", ThreadID: "t", TTLSeconds: api.AgentCallbackTokenTTLSec + 1}, http.StatusBadRequest},
		{"accepted", callbackAccount, api.CreateAgentCallbackRequest{EndpointID: "ep-1", ThreadID: "t", Kinds: []string{"event"}, TTLSeconds: 60}, http.StatusCreated},
	} {
		if _, code := f.issue(t, tc.account, tc.req); code != tc.want {
			t.Errorf("%s: issue = %d, want %d", tc.name, code, tc.want)
		}
	}
}

func TestCallbackPostRules(t *testing.T) {
	f := newCallbackFixture(t)
	events, _ := f.issue(t, callbackAccount, api.CreateAgentCallbackRequest{EndpointID: "ep-1", ThreadID: "t", Kinds: []string{"event"}, TTLSeconds: 60})
	results, _ := f.issue(t, callbackAccount, api.CreateAgentCallbackRequest{EndpointID: "ep-1", ThreadID: "t"})
	for _, tc := range []struct {
		name  string
		token string
		post  api.AgentCallbackPost
		want  int
	}{
		{"a kind the URL does not carry", events, toolResult("m", `1`), http.StatusForbidden},
		{"bad message id", results, toolResult("m m", `1`), http.StatusBadRequest},
		{"no data", results, api.AgentCallbackPost{MessageID: "m", Kind: api.AgentCallbackKindToolResult}, http.StatusBadRequest},
		{"data too large", results, toolResult("m", `"`+strings.Repeat("x", api.AgentCallbackPayloadMaxBytes)+`"`), http.StatusRequestEntityTooLarge},
		{"not a token", "agcb1.e30.AAAA", toolResult("m", `1`), http.StatusNotFound},
	} {
		if code, _ := f.post(t, tc.token, tc.post); code != tc.want {
			t.Errorf("%s: post = %d, want %d", tc.name, code, tc.want)
		}
	}
	if len(f.inbox.messages) != 0 {
		t.Fatalf("a refused post published %+v", f.inbox.messages)
	}
}

// Expiry, the capability and the endpoint are checked when the tool posts,
// not only when the URL was issued.
func TestCallbackRefusedAfterIssue(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*callbackFixture)
	}{
		{"expired", func(f *callbackFixture) { f.now = f.now.Add(61 * time.Second) }},
		{"capability off", func(f *callbackFixture) { f.enabled[callbackAccount] = false }},
		{"endpoint deleted", func(f *callbackFixture) { delete(f.inbox.endpoints, "ep-1") }},
		{"endpoint moved", func(f *callbackFixture) {
			f.inbox.endpoints["ep-1"] = state.ManagedRealtimeEndpoint{ID: "ep-1", AccountID: otherAccount}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCallbackFixture(t)
			token, _ := f.issue(t, callbackAccount, api.CreateAgentCallbackRequest{EndpointID: "ep-1", ThreadID: "t", TTLSeconds: 60})
			tc.change(f)
			if code, _ := f.post(t, token, toolResult("m", `1`)); code != http.StatusNotFound || len(f.inbox.messages) != 0 {
				t.Fatalf("post = %d with %d messages, want 404 and none", code, len(f.inbox.messages))
			}
		})
	}
}

// A retried post lands once and gets the first post's sequence; the same
// message_id with other data is a conflict.
func TestCallbackMessageLandsOnce(t *testing.T) {
	f := newCallbackFixture(t)
	token, _ := f.issue(t, callbackAccount, api.CreateAgentCallbackRequest{EndpointID: "ep-1", ThreadID: "t"})
	first, a := f.post(t, token, toolResult("call-1", `{"n":1}`))
	again, b := f.post(t, token, toolResult("call-1", `{"n":1}`))
	if first != http.StatusAccepted || again != http.StatusAccepted || a.Sequence != b.Sequence || len(f.inbox.messages) != 1 {
		t.Fatalf("posts = %d %d, sequences %q %q, %d messages", first, again, a.Sequence, b.Sequence, len(f.inbox.messages))
	}
	if code, _ := f.post(t, token, toolResult("call-1", `{"n":2}`)); code != http.StatusConflict {
		t.Fatalf("other data under the same message_id = %d, want 409", code)
	}
}

// Over random grants, posts and tampered tokens, every published message
// lands in the inbox of its token's endpoint, under its token's thread, and a
// token changed in any byte publishes nothing.
func TestPropertyCallbackStaysInItsThread(t *testing.T) {
	r := rand.New(rand.NewPCG(20261030, 125501))
	f := newCallbackFixture(t)
	type grant struct{ token, endpoint, thread string }
	var grants []grant
	for i := 0; i < 40; i++ {
		ep := []string{"ep-1", "ep-2"}[r.IntN(2)]
		thread := fmt.Sprintf("thread-%d", r.IntN(10))
		token, code := f.issue(t, callbackAccount, api.CreateAgentCallbackRequest{EndpointID: ep, ThreadID: thread})
		if code != http.StatusCreated {
			t.Fatalf("issue = %d", code)
		}
		grants = append(grants, grant{token, ep, thread})
	}
	for i := 0; i < 400; i++ {
		g := grants[r.IntN(len(grants))]
		token := g.token
		tampered := r.IntN(4) == 0
		if tampered {
			b := []byte(token)
			at := len("agcb1.") + r.IntN(len(b)-len("agcb1."))
			b[at] ^= byte(1 + r.IntN(255))
			token = string(b)
		}
		before := len(f.inbox.messages)
		code, _ := f.post(t, token, toolResult(fmt.Sprintf("m-%d", i), fmt.Sprintf(`{"i":%d}`, i)))
		if tampered && token != g.token {
			if code != http.StatusNotFound || len(f.inbox.messages) != before {
				t.Fatalf("a tampered token answered %d and published %d messages", code, len(f.inbox.messages)-before)
			}
			continue
		}
		if code != http.StatusAccepted {
			t.Fatalf("post = %d", code)
		}
		m := f.inbox.messages[len(f.inbox.messages)-1]
		if m.EndpointID != g.endpoint || m.Channel != api.AgentInboxChannel || m.Metadata["thread_id"] != g.thread ||
			!strings.HasPrefix(m.IdempotencyKey, "callback:"+g.thread+":") {
			t.Fatalf("token for %s/%s published %+v", g.endpoint, g.thread, m)
		}
	}
}

// revoke closes a thread with the account's API key and returns the status.
func (f *callbackFixture) revoke(t *testing.T, accountID, endpointID, threadID string) int {
	t.Helper()
	return f.revokeAs(t, keyCaller(accountID), endpointID, threadID)
}

func (f *callbackFixture) revokeAs(t *testing.T, caller agents.CallbackCaller, endpointID, threadID string) int {
	t.Helper()
	body, _ := json.Marshal(api.RevokeAgentCallbacksRequest{EndpointID: endpointID, ThreadID: threadID})
	w := httptest.NewRecorder()
	f.h.RevokeCallbacks(w, httptest.NewRequest(http.MethodPost, "/v1/agent-callbacks/revocations", bytes.NewReader(body)), caller)
	return w.Code
}

// An app's workload caller issues and revokes callback URLs for its own
// app's endpoints only; an API key reaches every endpoint of its account
// that its organization check allows (TestCallbackCallerAuthorize).
func TestCallbackAppCallerOwnsOnlyItsEndpoints(t *testing.T) {
	f := newCallbackFixture(t)
	app := agents.CallbackCaller{Account: state.Account{ID: callbackAccount, Plan: api.PlanPro}, AppID: "app-1"}
	for _, tc := range []struct {
		endpoint string
		want     int
	}{{"ep-1", http.StatusCreated}, {"ep-2", http.StatusNotFound}, {"ep-other", http.StatusNotFound}} {
		if _, code := f.issueAs(t, app, api.CreateAgentCallbackRequest{EndpointID: tc.endpoint, ThreadID: "t"}); code != tc.want {
			t.Errorf("issue on %s = %d, want %d", tc.endpoint, code, tc.want)
		}
		revokeWant := http.StatusOK
		if tc.want != http.StatusCreated {
			revokeWant = http.StatusNotFound
		}
		if code := f.revokeAs(t, app, tc.endpoint, "t"); code != revokeWant {
			t.Errorf("revoke on %s = %d, want %d", tc.endpoint, code, revokeWant)
		}
	}
	if _, code := f.issue(t, callbackAccount, api.CreateAgentCallbackRequest{EndpointID: "ep-2", ThreadID: "t"}); code != http.StatusCreated {
		t.Errorf("API key issue on ep-2 = %d", code)
	}
}

// An API key's caller carries the organization check of the endpoint's app:
// the check sees the endpoint the request names, only after the account
// owns it; a refusal answers as it is, and one that hides the app answers
// like a missing endpoint. Nothing is issued or revoked past a refusal.
func TestCallbackCallerAuthorize(t *testing.T) {
	f := newCallbackFixture(t)
	var seen []string
	callerWith := func(problem *api.Problem) agents.CallbackCaller {
		return agents.CallbackCaller{Account: state.Account{ID: callbackAccount, Plan: api.PlanPro},
			Authorize: func(endpoint state.ManagedRealtimeEndpoint) *api.Problem {
				seen = append(seen, endpoint.ID)
				return problem
			}}
	}
	for _, tc := range []struct {
		thread        string
		problem       *api.Problem
		issue, revoke int
	}{
		{"allowed", nil, http.StatusCreated, http.StatusOK},
		{"role-refused", api.ErrOrgRoleForbidden("issue callback URLs"), http.StatusForbidden, http.StatusForbidden},
		{"app-hidden", api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no such app"), http.StatusNotFound, http.StatusNotFound},
	} {
		caller := callerWith(tc.problem)
		if _, code := f.issueAs(t, caller, api.CreateAgentCallbackRequest{EndpointID: "ep-2", ThreadID: tc.thread}); code != tc.issue {
			t.Errorf("%s: issue = %d, want %d", tc.thread, code, tc.issue)
		}
		if code := f.revokeAs(t, caller, "ep-2", tc.thread); code != tc.revoke {
			t.Errorf("%s: revoke = %d, want %d", tc.thread, code, tc.revoke)
		}
	}
	if _, code := f.issueAs(t, callerWith(nil), api.CreateAgentCallbackRequest{EndpointID: "ep-other", ThreadID: "t"}); code != http.StatusNotFound {
		t.Errorf("another account's endpoint = %d", code)
	}
	if want := []string{"ep-2", "ep-2", "ep-2", "ep-2", "ep-2", "ep-2"}; !slices.Equal(seen, want) {
		t.Errorf("checked endpoints %v, want %v", seen, want)
	}
	revocations := f.h.Revocations.(*fakeRevocations)
	for _, thread := range []string{"role-refused", "app-hidden"} {
		if _, revoked := revocations.at["ep-2/"+thread]; revoked {
			t.Errorf("a refused revocation recorded %q", thread)
		}
	}
}

// A revocation stops every URL issued for the thread until then, and only
// for that thread; a URL issued afterwards works.
func TestCallbackRevocation(t *testing.T) {
	f := newCallbackFixture(t)
	before, _ := f.issue(t, callbackAccount, api.CreateAgentCallbackRequest{EndpointID: "ep-1", ThreadID: "t"})
	sibling, _ := f.issue(t, callbackAccount, api.CreateAgentCallbackRequest{EndpointID: "ep-1", ThreadID: "u"})
	f.now = f.now.Add(time.Millisecond)
	if code := f.revoke(t, callbackAccount, "ep-1", "t"); code != http.StatusOK {
		t.Fatalf("revoke = %d", code)
	}
	if code, _ := f.post(t, before, toolResult("m1", `1`)); code != http.StatusNotFound {
		t.Fatalf("a revoked URL answered %d", code)
	}
	if code, _ := f.post(t, sibling, toolResult("m2", `1`)); code != http.StatusAccepted {
		t.Fatalf("another thread's URL answered %d", code)
	}
	f.now = f.now.Add(time.Millisecond)
	after, _ := f.issue(t, callbackAccount, api.CreateAgentCallbackRequest{EndpointID: "ep-1", ThreadID: "t"})
	if code, _ := f.post(t, after, toolResult("m3", `1`)); code != http.StatusAccepted {
		t.Fatalf("a URL issued after the revocation answered %d", code)
	}
	if code := f.revoke(t, callbackAccount, "ep-other", "t"); code != http.StatusNotFound {
		t.Fatalf("revoking another account's thread = %d", code)
	}
	if code := f.revoke(t, callbackAccount, "ep-1", "a b"); code != http.StatusBadRequest {
		t.Fatalf("revoking a bad thread = %d", code)
	}
}

// A thread takes AgentCallbacksPerMinutePerThread posts a minute; the next
// is refused with the limit, and the next minute starts over.
func TestCallbackRateLimit(t *testing.T) {
	f := newCallbackFixture(t)
	token, _ := f.issue(t, callbackAccount, api.CreateAgentCallbackRequest{EndpointID: "ep-1", ThreadID: "t"})
	other, _ := f.issue(t, callbackAccount, api.CreateAgentCallbackRequest{EndpointID: "ep-1", ThreadID: "u"})
	for i := 0; i < api.AgentCallbacksPerMinutePerThread; i++ {
		if code, _ := f.post(t, token, toolResult(fmt.Sprintf("m%d", i), `1`)); code != http.StatusAccepted {
			t.Fatalf("post %d = %d", i, code)
		}
	}
	if code, _ := f.post(t, token, toolResult("over", `1`)); code != http.StatusTooManyRequests {
		t.Fatalf("the post past the limit = %d", code)
	}
	if code, _ := f.post(t, other, toolResult("other", `1`)); code != http.StatusAccepted {
		t.Fatalf("another thread was limited: %d", code)
	}
	f.now = f.now.Add(time.Minute)
	if code, _ := f.post(t, token, toolResult("next", `1`)); code != http.StatusAccepted {
		t.Fatalf("the next minute's post = %d", code)
	}
}

// Over random issues, revocations and posts, a URL publishes exactly when it
// was issued after its thread's latest revocation.
func TestPropertyRevokedCallbackNeverPublishes(t *testing.T) {
	r := rand.New(rand.NewPCG(20261101, 3))
	f := newCallbackFixture(t)
	type grant struct {
		token, thread string
		issued        time.Time
	}
	var grants []grant
	revoked := map[string]time.Time{}
	for i := 0; i < 600; i++ {
		f.now = f.now.Add(time.Duration(1+r.IntN(5)) * time.Millisecond)
		thread := fmt.Sprintf("t%d", r.IntN(4))
		switch op := r.IntN(10); {
		case op < 3 || len(grants) == 0:
			token, code := f.issue(t, callbackAccount, api.CreateAgentCallbackRequest{EndpointID: "ep-1", ThreadID: thread})
			if code != http.StatusCreated {
				t.Fatalf("issue = %d", code)
			}
			grants = append(grants, grant{token, thread, f.now})
		case op < 5:
			if code := f.revoke(t, callbackAccount, "ep-1", thread); code != http.StatusOK {
				t.Fatalf("revoke = %d", code)
			}
			revoked[thread] = f.now
		default:
			g := grants[r.IntN(len(grants))]
			at, wasRevoked := revoked[g.thread]
			want := http.StatusAccepted
			if wasRevoked && !g.issued.After(at) {
				want = http.StatusNotFound
			}
			if code, _ := f.post(t, g.token, toolResult(fmt.Sprintf("m%d", i), `1`)); code != want {
				t.Fatalf("step %d: a URL issued %v, thread revoked %v (%v), answered %d; want %d", i, g.issued, at, wasRevoked, code, want)
			}
		}
	}
}
