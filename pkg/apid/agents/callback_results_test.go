// adr: 20261030125501 §3 amendment 2026-11-22 — a tool result larger than
// one inbox message is stored, the inbox message carries a preview and the
// result's URL, only the endpoint's app reads it, a retry lands once, the
// endpoint's live bytes are bounded and expired results are deleted.
package agents_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/agents"
	"github.com/onebox-faas/faas/pkg/state"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
	"github.com/onebox-faas/faas/pkg/storage"
)

// fakeResults mirrors agentstore.Store's stored results: one per endpoint,
// thread and message, and the endpoint's live bytes bounded.
type fakeResults struct {
	mu   sync.Mutex
	rows map[string]agentstore.CallbackResult
	next int
}

func (f *fakeResults) ReserveCallbackResult(_ context.Context, r agentstore.CallbackResult, now time.Time, maxBytes int64) (agentstore.CallbackResult, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var live int64
	for _, row := range f.rows {
		if row.EndpointID == r.EndpointID && row.ThreadID == r.ThreadID && row.MessageID == r.MessageID {
			return row, true, nil
		}
		if row.EndpointID == r.EndpointID && row.ExpiresAt.After(now) {
			live += int64(row.SizeBytes)
		}
	}
	if live+int64(r.SizeBytes) > maxBytes {
		return agentstore.CallbackResult{}, false, &agentstore.LimitError{Limit: "AgentCallbackResultsPerEndpointMaxBytes", Max: int(maxBytes), Observed: int(live) + r.SizeBytes}
	}
	f.next++
	r.ID = "00000000-0000-0000-0000-" + strconv.FormatInt(int64(100000000000+f.next), 10)
	f.rows[r.ID] = r
	return r, false, nil
}

func (f *fakeResults) CallbackResultByID(_ context.Context, accountID, id string) (agentstore.CallbackResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.rows[id]
	if !ok || row.AccountID != accountID {
		return agentstore.CallbackResult{}, agentstore.ErrNotFound
	}
	return row, nil
}

func (f *fakeResults) DeleteCallbackResult(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.rows, id)
	return nil
}

func (f *fakeResults) ExpiredCallbackResults(_ context.Context, before time.Time, limit int) ([]agentstore.CallbackResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []agentstore.CallbackResult
	for _, row := range f.rows {
		if row.ExpiresAt.Before(before) && len(out) < limit {
			out = append(out, row)
		}
	}
	return out, nil
}

// memFiles is an artifact store in memory; failPut fails the next puts.
type memFiles struct {
	mu      sync.Mutex
	objects map[string][]byte
	failPut int
}

func (m *memFiles) Put(_ context.Context, key string, r io.Reader) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failPut > 0 {
		m.failPut--
		return errors.New("store unavailable")
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	m.objects[key] = b
	return nil
}

func (m *memFiles) Get(_ context.Context, key string) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.objects[key]
	if !ok {
		return nil, storage.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func (m *memFiles) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.objects, key)
	return nil
}

func withStoredResults(f *callbackFixture) (*fakeResults, *memFiles) {
	results := &fakeResults{rows: map[string]agentstore.CallbackResult{}}
	files := &memFiles{objects: map[string][]byte{}}
	f.h.CallbackResults, f.h.Files = results, files
	return results, files
}

// stub reads the inbox message that stands for a stored result.
type stub struct {
	Type      string `json:"type"`
	GroupID   string `json:"group_id"`
	ID        string `json:"id"`
	CallID    string `json:"call_id"`
	Text      string `json:"text"`
	Preview   string `json:"preview"`
	Truncated bool   `json:"truncated"`
	URL       string `json:"result_url"`
	Bytes     int    `json:"result_bytes"`
	SHA256    string `json:"result_sha256"`
}

func readStub(t *testing.T, m state.ManagedRealtimeChannelMessage) stub {
	t.Helper()
	if len(m.Data) > api.AgentCallbackPayloadMaxBytes {
		t.Fatalf("inbox message is %d bytes", len(m.Data))
	}
	var s stub
	if err := json.Unmarshal(m.Data, &s); err != nil {
		t.Fatalf("stub %s: %v", m.Data, err)
	}
	return s
}

// getResult reads a stored result through the route's handler.
func (f *callbackFixture) getResult(t *testing.T, caller agents.CallbackCaller, url string) (int, []byte) {
	t.Helper()
	id := url[strings.LastIndex(url, "/")+1:]
	r := httptest.NewRequest(http.MethodGet, "/v1/agent-callbacks/results/"+id, nil)
	r.SetPathValue("id", id)
	w := httptest.NewRecorder()
	f.h.GetCallbackResult(w, r, caller)
	return w.Code, w.Body.Bytes()
}

func bigJSON(field string, n int) string {
	return `{"` + field + `":"` + strings.Repeat("é", n/2) + `"}`
}

func TestLargeCallbackIsStoredAndOnlyItsAppReadsIt(t *testing.T) {
	f := newCallbackFixture(t)
	results, files := withStoredResults(f)
	token, _ := f.issue(t, callbackAccount, api.CreateAgentCallbackRequest{EndpointID: "ep-1", ThreadID: "t-1"})
	data := bigJSON("answer", 20_000)
	if code, _ := f.post(t, token, toolResult("r-1", data)); code != http.StatusAccepted {
		t.Fatalf("post = %d", code)
	}
	if len(f.inbox.messages) != 1 || len(results.rows) != 1 || len(files.objects) != 1 {
		t.Fatalf("inbox %d, results %d, objects %d", len(f.inbox.messages), len(results.rows), len(files.objects))
	}
	m := f.inbox.messages[0]
	if m.Metadata["message_id"] != "r-1" || m.Metadata["kind"] != api.AgentCallbackKindToolResult || m.Metadata["thread_id"] != "t-1" {
		t.Fatalf("metadata %v", m.Metadata)
	}
	s := readStub(t, m)
	sum := sha256.Sum256([]byte(data))
	if !s.Truncated || s.Bytes != len(data) || s.SHA256 != hex.EncodeToString(sum[:]) ||
		!strings.HasPrefix(s.URL, "https://api.example.test/v1/agent-callbacks/results/") {
		t.Fatalf("stub %+v", s)
	}
	if !strings.HasPrefix(s.Preview, `{"answer":"é`) || len(s.Preview) > api.AgentCallbackResultPreviewMaxBytes || !utf8.ValidString(s.Preview) {
		t.Fatalf("preview %q", s.Preview)
	}
	for key := range files.objects {
		if !strings.HasPrefix(key, "agents/callback-results/"+callbackAccount+"/ep-1/") {
			t.Fatalf("object key %s", key)
		}
	}

	if code, body := f.getResult(t, keyCaller(callbackAccount), s.URL); code != http.StatusOK || string(body) != data {
		t.Fatalf("API key read = %d, %d bytes", code, len(body))
	}
	app := agents.CallbackCaller{Account: state.Account{ID: callbackAccount, Plan: api.PlanPro}, AppID: "app-1"}
	if code, body := f.getResult(t, app, s.URL); code != http.StatusOK || string(body) != data {
		t.Fatalf("the endpoint's app read = %d", code)
	}
	otherApp := agents.CallbackCaller{Account: state.Account{ID: callbackAccount, Plan: api.PlanPro}, AppID: "app-2"}
	if code, _ := f.getResult(t, otherApp, s.URL); code != http.StatusNotFound {
		t.Fatalf("another app of the account read = %d, want 404", code)
	}
	if code, _ := f.getResult(t, keyCaller(otherAccount), s.URL); code != http.StatusNotFound {
		t.Fatalf("another account read = %d, want 404", code)
	}
}

func TestLargeRAPToolResultKeepsItsRouting(t *testing.T) {
	f := newCallbackFixture(t)
	withStoredResults(f)
	token, _ := f.issue(t, callbackAccount, api.CreateAgentCallbackRequest{EndpointID: "ep-1", ThreadID: "t-1"})
	text := strings.Repeat("line of a long page\n", 2000)
	body, _ := json.Marshal(map[string]any{"type": "tool_result", "group_id": "t-1", "id": "call-9", "call_id": "toolu_9", "text": text})
	if code, _ := f.postRaw(t, token, string(body)); code != http.StatusAccepted {
		t.Fatalf("post = %d", code)
	}
	m := f.inbox.messages[0]
	s := readStub(t, m)
	if m.Metadata["message_id"] != "rap-tool-result-call-9" || s.Type != "tool_result" || s.GroupID != "t-1" || s.ID != "call-9" ||
		s.CallID != "toolu_9" || !strings.HasPrefix(text, s.Text) || s.Text == "" || s.Bytes != len(body) {
		t.Fatalf("stub %+v metadata %v", s, m.Metadata)
	}
	if code, got := f.getResult(t, keyCaller(callbackAccount), s.URL); code != http.StatusOK || !bytes.Equal(got, body) {
		t.Fatalf("read = %d", code)
	}
}

func TestLargeCallbackRetryLandsOnce(t *testing.T) {
	f := newCallbackFixture(t)
	results, files := withStoredResults(f)
	token, _ := f.issue(t, callbackAccount, api.CreateAgentCallbackRequest{EndpointID: "ep-1", ThreadID: "t-1"})
	data := bigJSON("a", 10_000)
	for range 2 {
		if code, _ := f.post(t, token, toolResult("r-1", data)); code != http.StatusAccepted {
			t.Fatalf("post = %d", code)
		}
	}
	if len(f.inbox.messages) != 1 || len(results.rows) != 1 || len(files.objects) != 1 {
		t.Fatalf("a retry stored again: inbox %d, results %d", len(f.inbox.messages), len(results.rows))
	}
	if code, _ := f.post(t, token, toolResult("r-1", bigJSON("b", 10_000))); code != http.StatusConflict {
		t.Fatalf("the same message with other data = %d, want 409", code)
	}
}

func TestLargeCallbackLimits(t *testing.T) {
	f := newCallbackFixture(t)
	results, _ := withStoredResults(f)
	token, _ := f.issue(t, callbackAccount, api.CreateAgentCallbackRequest{EndpointID: "ep-1", ThreadID: "t-1"})
	tooBig := `"` + strings.Repeat("x", api.AgentCallbackResultMaxBytes) + `"`
	if code, _ := f.post(t, token, toolResult("r-big", tooBig)); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("over the stored-result limit = %d, want 413", code)
	}
	// Fill the endpoint to just under its limit.
	results.rows["full"] = agentstore.CallbackResult{ID: "full", AccountID: callbackAccount, EndpointID: "ep-1", ThreadID: "other",
		MessageID: "m", SizeBytes: api.AgentCallbackResultsPerEndpointMaxBytes - 100, ExpiresAt: f.now.Add(time.Hour)}
	r := httptest.NewRequest(http.MethodPost, "/v1/agent-callbacks/token", strings.NewReader(mustJSON(toolResult("r-2", bigJSON("a", 10_000)))))
	r.SetPathValue("token", token)
	w := httptest.NewRecorder()
	f.h.ReceiveCallback(w, r)
	if w.Code != http.StatusRequestEntityTooLarge || !strings.Contains(w.Body.String(), api.CodeAgentCallbackResultsFull) {
		t.Fatalf("past the endpoint's live bytes = %d %s", w.Code, w.Body)
	}
	if len(f.inbox.messages) != 0 {
		t.Fatal("a refused result reached the inbox")
	}
	// A small result still fits one message and needs no storage.
	if code, _ := f.post(t, token, toolResult("r-3", `{"ok":true}`)); code != http.StatusAccepted {
		t.Fatalf("small result = %d", code)
	}
}

func TestLargeCallbackWithoutStorageIsRefused(t *testing.T) {
	f := newCallbackFixture(t)
	token, _ := f.issue(t, callbackAccount, api.CreateAgentCallbackRequest{EndpointID: "ep-1", ThreadID: "t-1"})
	// As before stored results: a body past one message is refused, 400 when
	// it is past the decoder's bound, 413 when only its data is too large.
	if code, _ := f.post(t, token, toolResult("r-1", bigJSON("a", 10_000))); code != http.StatusBadRequest && code != http.StatusRequestEntityTooLarge {
		t.Fatalf("no artifact store: post = %d, want 400 or 413", code)
	}
	if code, _ := f.post(t, token, toolResult("r-2", `"`+strings.Repeat("x", api.AgentCallbackPayloadMaxBytes)+`"`)); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("no artifact store, data just past one message: post = %d, want 413", code)
	}
	if len(f.inbox.messages) != 0 {
		t.Fatal("a refused result reached the inbox")
	}
}

func TestLargeCallbackBodyFailureLeavesNothingBehind(t *testing.T) {
	f := newCallbackFixture(t)
	results, files := withStoredResults(f)
	files.failPut = 1
	token, _ := f.issue(t, callbackAccount, api.CreateAgentCallbackRequest{EndpointID: "ep-1", ThreadID: "t-1"})
	data := bigJSON("a", 10_000)
	if code, _ := f.post(t, token, toolResult("r-1", data)); code < 500 {
		t.Fatalf("a failed body store = %d, want 5xx so the tool retries", code)
	}
	if len(results.rows) != 0 || len(f.inbox.messages) != 0 {
		t.Fatalf("results %d inbox %d after a failed store", len(results.rows), len(f.inbox.messages))
	}
	if code, _ := f.post(t, token, toolResult("r-1", data)); code != http.StatusAccepted || len(f.inbox.messages) != 1 {
		t.Fatalf("retry = %d", code)
	}
}

func TestExpiredCallbackResultsAreDeleted(t *testing.T) {
	f := newCallbackFixture(t)
	results, files := withStoredResults(f)
	token, _ := f.issue(t, callbackAccount, api.CreateAgentCallbackRequest{EndpointID: "ep-1", ThreadID: "t-1"})
	if code, _ := f.post(t, token, toolResult("r-1", bigJSON("a", 10_000))); code != http.StatusAccepted {
		t.Fatalf("post = %d", code)
	}
	url := readStub(t, f.inbox.messages[0]).URL
	f.now = f.now.Add(api.AgentCallbackResultRetentionSec*time.Second + time.Second)
	if code, _ := f.getResult(t, keyCaller(callbackAccount), url); code != http.StatusNotFound {
		t.Fatalf("an expired result read = %d, want 404", code)
	}
	agents.SweepCallbackResultsForTest(f.h, context.Background())
	if len(results.rows) != 0 || len(files.objects) != 0 {
		t.Fatalf("after the sweep: results %d, objects %d", len(results.rows), len(files.objects))
	}
}

// Whatever the result, its stub fits one inbox message and its preview is
// valid UTF-8 cut from the result's text.
func TestPropertyResultStubFitsOneMessage(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 11))
	runes := []string{"a", "é", "漢", "😀", "\"", "\\", "\n"}
	for i := range 300 {
		var b strings.Builder
		for range rng.IntN(20_000) + api.AgentCallbackPayloadMaxBytes {
			b.WriteString(runes[rng.IntN(len(runes))])
		}
		var data []byte
		switch i % 3 {
		case 0:
			data, _ = json.Marshal(map[string]any{"type": "tool_result", "group_id": strings.Repeat("g", 64), "id": strings.Repeat("i", 500), "text": b.String()})
		case 1:
			data, _ = json.Marshal(map[string]any{"text": b.String(), "n": i})
		default:
			data, _ = json.Marshal(b.String())
		}
		out, err := agents.ResultStubForTest(data, "https://api.example.test/v1/agent-callbacks/results/"+strings.Repeat("0", 36), len(data), strings.Repeat("a", 64))
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		if len(out) > api.AgentCallbackPayloadMaxBytes {
			t.Fatalf("case %d: stub is %d bytes", i, len(out))
		}
		var s stub
		if err := json.Unmarshal(out, &s); err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		preview := s.Text + s.Preview
		if !utf8.ValidString(preview) || len(preview) > api.AgentCallbackResultPreviewMaxBytes {
			t.Fatalf("case %d: preview %d bytes, valid %v", i, len(preview), utf8.ValidString(preview))
		}
	}
}

// A run's result too large for one message is stored rather than left out.
func TestRunEndStoresALargeResult(t *testing.T) {
	f, _, runs := newRunFixture(t)
	withStoredResults(f)
	big := runEnd("run-big", "t-3", "call_3", f.now)
	big.Result = json.RawMessage(`{"report":"` + strings.Repeat("y", 50_000) + `"}`)
	runs.ends = []agentstore.RunCallbackEnd{big}
	if settled, err := f.h.DeliverRunCallbacks(context.Background()); err != nil || settled != 1 {
		t.Fatalf("deliver = %d, %v", settled, err)
	}
	s := readStub(t, f.inbox.messages[0])
	if s.Type != "tool_result" || s.CallID != "prov-call_3" || !s.Truncated || !strings.HasPrefix(s.Text, `{"run_id":"run-big"`) {
		t.Fatalf("stub %+v", s)
	}
	code, body := f.getResult(t, keyCaller(callbackAccount), s.URL)
	var msg struct {
		Text string `json:"text"`
	}
	if code != http.StatusOK || json.Unmarshal(body, &msg) != nil || !strings.Contains(msg.Text, strings.Repeat("y", 50_000)) {
		t.Fatalf("read = %d", code)
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
