// synth_pure_extra_test.go — fill pkg/gateway/synth.go coverage of
// the tiny pure helpers and the SynthServer happy-path handle*
// methods (handleHealthz / handleSynthesize / handleInvocationDispatch
// / handleInvocationDispatchBatch / dispatchBatchRecord). Targets
// parseBatchFailures, containsString, jsonOrEmpty, base64Decode,
// and the JSON-shape round-trips of batchDispatchRequest +
// batchDispatchResponse + batchDispatchResult.
package gateway

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// --- parseBatchFailures ------------------------------------------

func TestParseBatchFailures_EmptyBody(t *testing.T) {
	got, err := parseBatchFailures(nil)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("nil: got %v, want []", got)
	}
}

func TestParseBatchFailures_EmptySliceBody(t *testing.T) {
	got, err := parseBatchFailures([]byte{})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("empty: got %v, want []", got)
	}
}

func TestParseBatchFailures_MalformedJSON(t *testing.T) {
	if _, err := parseBatchFailures([]byte("not json")); err == nil {
		t.Error("malformed: got nil err, want error")
	}
}

func TestParseBatchFailures_SkipsEmptyIdentifiers(t *testing.T) {
	body := []byte(`{"batchItemFailures":[{"itemIdentifier":"k1"},{"itemIdentifier":""},{"itemIdentifier":"k2"}]}`)
	got, err := parseBatchFailures(body)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(got) != 2 || got[0] != "k1" || got[1] != "k2" {
		t.Errorf("got %v, want [k1 k2]", got)
	}
}

// --- containsString ----------------------------------------------

func TestContainsString_AllBranches(t *testing.T) {
	haystack := []string{"a", "b", "c"}
	if !containsString(haystack, "b") {
		t.Error("present: got false, want true")
	}
	if containsString(haystack, "z") {
		t.Error("absent: got true, want false")
	}
	if containsString(nil, "a") {
		t.Error("nil: got true, want false")
	}
	if containsString([]string{}, "a") {
		t.Error("empty: got true, want false")
	}
}

// --- jsonOrEmpty -------------------------------------------------

func TestJsonOrEmpty_NilMap(t *testing.T) {
	got := jsonOrEmpty(nil)
	if string(got) != "{}" {
		t.Errorf("nil: got %q, want {}", got)
	}
}

func TestJsonOrEmpty_EmptyMap(t *testing.T) {
	got := jsonOrEmpty(map[string]string{})
	if string(got) != "{}" {
		t.Errorf("empty: got %q, want {}", got)
	}
}

func TestJsonOrEmpty_Populated(t *testing.T) {
	got := jsonOrEmpty(map[string]string{"k": "v"})
	if !strings.Contains(string(got), `"k":"v"`) {
		t.Errorf("got %q", got)
	}
}

// --- base64Decode ------------------------------------------------

func TestBase64Decode_Valid(t *testing.T) {
	in := base64.StdEncoding.EncodeToString([]byte("hello"))
	got, err := base64Decode(in)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("got %q", got)
	}
}

func TestBase64Decode_Invalid(t *testing.T) {
	if _, err := base64Decode("not-base64-!@#"); err == nil {
		t.Error("invalid: got nil err, want error")
	}
}

// --- batchDispatch* wire-shape golden round-trips ----------------

func TestBatchDispatchRequest_RoundTrip(t *testing.T) {
	in := batchDispatchRequest{
		InvocationID: "inv-1",
		AppID:        "app-1",
		Source:       "esm",
		TriggerID:    "trig-1",
		Records: []batchDispatchRecord{
			{ItemIdentifier: "r1", PayloadB64: "aGVsbG8=", Headers: map[string]string{"X-T": "v"}, Metadata: map[string]any{"stream": "s"}},
		},
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out batchDispatchRequest
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.AppID != in.AppID || out.InvocationID != in.InvocationID {
		t.Errorf("got %+v", out)
	}
	if len(out.Records) != 1 || out.Records[0].ItemIdentifier != "r1" {
		t.Errorf("records mismatch: %+v", out.Records)
	}
}

func TestBatchDispatchResponse_OmitEmpty(t *testing.T) {
	// Error + Code are omitempty — confirm they vanish on the
	// success path.
	res := batchDispatchResponse{Results: []batchDispatchResult{
		{ItemIdentifier: "r1", Status: "succeeded"},
	}}
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(b), "error") {
		t.Errorf("success path leaked error field: %s", b)
	}
	if strings.Contains(string(b), "code") {
		t.Errorf("success path leaked code field: %s", b)
	}
}

// --- fake dispatcher --------------------------------------------

type fakeSynthDispatcher struct {
	wakes   []string
	invs    []state.Invocation
	targets []Target
}

type statusAwareSynthDispatcher struct{ fakeSynthDispatcher }

func (f *statusAwareSynthDispatcher) InvokeWithStatus(_ context.Context, _ string, inv state.Invocation) (state.Invocation, int, error) {
	f.invs = append(f.invs, inv)
	inv.State = state.InvocationDispatching
	inv.Result = json.RawMessage(`{"ok":true}`)
	return inv, http.StatusCreated, nil
}

func (f *fakeSynthDispatcher) Wake(_ context.Context, appID string) error {
	f.wakes = append(f.wakes, appID)
	return nil
}

func (f *fakeSynthDispatcher) Invoke(_ context.Context, _ string, inv state.Invocation) (state.Invocation, error) {
	f.invs = append(f.invs, inv)
	// Cast to the canonical succeeded state the handler
	// recognizes (the handler compares against
	// batchDispatchStatusSucceeded below).
	inv.State = state.InvocationState(batchDispatchStatusSucceeded)
	return inv, nil
}

func (f *fakeSynthDispatcher) InvokeWithTarget(_ context.Context, _ string, inv state.Invocation, target Target) (state.Invocation, error) {
	f.targets = append(f.targets, target)
	inv.State = state.InvocationState(batchDispatchStatusSucceeded)
	return inv, nil
}

func newSynthServer(t *testing.T) (*SynthServer, *fakeSynthDispatcher) {
	t.Helper()
	d := &fakeSynthDispatcher{}
	srv := NewSynthServer("/tmp/faas-synth-extra-test.sock", d, nil)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Stop(ctx)
	})
	return srv, d
}

// --- handleHealthz ----------------------------------------------

func TestHandleHealthz_Returns200(t *testing.T) {
	srv, _ := newSynthServer(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	srv.handleHealthz(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("code = %d, want 200", w.Code)
	}
}

// --- handleSynthesize -------------------------------------------

func TestHandleSynthesize_RequiresPostMethod(t *testing.T) {
	srv, d := newSynthServer(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/v1/synthesize", nil)
	srv.handleSynthesize(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: code = %d, want 405", w.Code)
	}
	if len(d.wakes) != 0 {
		t.Errorf("GET: dispatcher called %d times", len(d.wakes))
	}
}

func TestHandleSynthesize_MalformedJSONReturns400(t *testing.T) {
	srv, d := newSynthServer(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/synthesize",
		strings.NewReader("not json"))
	r.Header.Set("Content-Type", "application/json")
	srv.handleSynthesize(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("code = %d, want 400", w.Code)
	}
	if len(d.wakes) != 0 {
		t.Errorf("malformed: dispatcher called %d times", len(d.wakes))
	}
}

func TestHandleSynthesize_MissingAppIDReturns400(t *testing.T) {
	srv, d := newSynthServer(t)
	w := httptest.NewRecorder()
	// Missing Path → bad-request per handler's "app_id + path
	// required" check.
	body := `{"app_id":"app-1"}`
	r := httptest.NewRequest(http.MethodPost, "/v1/synthesize", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	srv.handleSynthesize(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("code = %d, want 400", w.Code)
	}
	if len(d.wakes) != 0 {
		t.Errorf("missing fields: dispatcher called %d times", len(d.wakes))
	}
}

func TestHandleSynthesize_WakesOnValidRequest(t *testing.T) {
	srv, d := newSynthServer(t)
	w := httptest.NewRecorder()
	body := `{"app_id":"app-1","path":"/foo","method":"GET"}`
	r := httptest.NewRequest(http.MethodPost, "/v1/synthesize", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	srv.handleSynthesize(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("code = %d, want 200", w.Code)
	}
	if len(d.wakes) != 1 {
		t.Fatalf("wakes = %d, want 1", len(d.wakes))
	}
	if d.wakes[0] != "app-1" {
		t.Errorf("wake appID = %q", d.wakes[0])
	}
}

// --- handleInvocationDispatch (single-record) ------------------

func TestHandleInvocationDispatch_RequiresPost(t *testing.T) {
	srv, d := newSynthServer(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/v1/invocations:dispatch", nil)
	srv.handleInvocationDispatch(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: code = %d, want 405", w.Code)
	}
	if len(d.invs) != 0 {
		t.Errorf("GET: dispatcher called %d times", len(d.invs))
	}
}

func TestHandleInvocationDispatch_DispatchesOnValidRequest(t *testing.T) {
	srv, d := newSynthServer(t)
	w := httptest.NewRecorder()
	body := `{"invocation_id":"inv-2","app_id":"app-2"}`
	r := httptest.NewRequest(http.MethodPost, "/v1/invocations:dispatch", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	srv.handleInvocationDispatch(w, r)
	if len(d.invs) != 1 {
		t.Fatalf("invocations = %d, want 1", len(d.invs))
	}
	if w.Code != http.StatusOK {
		t.Errorf("code = %d, want 200", w.Code)
	}
}

func TestHandleInvocationDispatch_UsesOptionalDownstreamStatus(t *testing.T) {
	d := &statusAwareSynthDispatcher{}
	srv := NewSynthServer("/tmp/faas-synth-status-test.sock", d, nil)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/invocations:dispatch", strings.NewReader(`{"invocation_id":"inv-status","app_id":"app-status"}`))
	srv.handleInvocationDispatch(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200 transport response", w.Code)
	}
	var response struct {
		StatusCode int `json:"status_code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("status_code = %d, want 201", response.StatusCode)
	}
}

func TestHandleInvocationDispatch_MalformedReturns400(t *testing.T) {
	srv, d := newSynthServer(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/invocations:dispatch", strings.NewReader("garbage"))
	r.Header.Set("Content-Type", "application/json")
	srv.handleInvocationDispatch(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("code = %d, want 400", w.Code)
	}
	if len(d.invs) != 0 {
		t.Errorf("malformed: dispatcher called %d times", len(d.invs))
	}
}

func TestHandleInvocationDispatch_InvalidBodyB64Returns400(t *testing.T) {
	srv, d := newSynthServer(t)
	w := httptest.NewRecorder()
	body := `{"invocation_id":"inv-3","app_id":"app-3","body_b64":"!!!not-base64!!!"}`
	r := httptest.NewRequest(http.MethodPost, "/v1/invocations:dispatch", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	srv.handleInvocationDispatch(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("code = %d, want 400", w.Code)
	}
	if len(d.invs) != 0 {
		t.Errorf("invalid body_b64: dispatcher called %d times", len(d.invs))
	}
}

// --- handleInvocationDispatchBatch -----------------------------

func TestHandleInvocationDispatchBatch_RequiresPost(t *testing.T) {
	srv, d := newSynthServer(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/v1/invocations:dispatch_batch", nil)
	srv.handleInvocationDispatchBatch(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: code = %d, want 405", w.Code)
	}
	if len(d.invs) != 0 {
		t.Errorf("GET: dispatcher called %d times", len(d.invs))
	}
}

func TestHandleInvocationDispatchBatch_DispatchesBatch(t *testing.T) {
	// Shrink the per-record timeout so a missing dispatcher
	// response doesn't make the test hang.
	srv, d := newSynthServer(t)
	batchDispatchPerRecordTimeout = 100 * time.Millisecond
	t.Cleanup(func() { batchDispatchPerRecordTimeout = 30 * time.Second })

	w := httptest.NewRecorder()
	body := `{
		"invocation_id": "inv-batch",
		"app_id": "app-batch",
		"source": "esm",
		"trigger_id": "trig-1",
		"records": [
			{"item_identifier": "k1", "payload_b64": "aGVsbG8=", "headers": {"X-T": "v"}},
			{"item_identifier": "k2", "payload_b64": "d29ybGQ="}
		]
	}`
	r := httptest.NewRequest(http.MethodPost, "/v1/invocations:dispatch_batch", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	srv.handleInvocationDispatchBatch(w, r)
	// Each record becomes one Invoke call.
	if len(d.invs) != 2 {
		t.Fatalf("invocations = %d, want 2", len(d.invs))
	}
	if w.Code != http.StatusOK {
		t.Errorf("code = %d, want 200", w.Code)
	}
	// Body must be the per-record status array.
	var resp batchDispatchResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response unmarshal: %v", err)
	}
	if len(resp.Results) != 2 {
		t.Errorf("results count = %d, want 2", len(resp.Results))
	}
	for _, res := range resp.Results {
		if res.Status != "succeeded" {
			t.Errorf("status = %q, want succeeded", res.Status)
		}
	}
}

func TestHandleInvocationDispatchBatch_EmptyRecords(t *testing.T) {
	// Empty records slice → handler still returns 200 with an empty
	// results array (no invocations).
	srv, d := newSynthServer(t)
	batchDispatchPerRecordTimeout = 100 * time.Millisecond
	t.Cleanup(func() { batchDispatchPerRecordTimeout = 30 * time.Second })

	w := httptest.NewRecorder()
	body := `{"invocation_id":"inv-bad","app_id":"app-1","records":[]}`
	r := httptest.NewRequest(http.MethodPost, "/v1/invocations:dispatch_batch", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	srv.handleInvocationDispatchBatch(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("code = %d, want 200", w.Code)
	}
	if len(d.invs) != 0 {
		t.Errorf("empty records: dispatcher called %d times", len(d.invs))
	}
	var resp batchDispatchResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response unmarshal: %v", err)
	}
	if len(resp.Results) != 0 {
		t.Errorf("results count = %d, want 0", len(resp.Results))
	}
}

func TestHandleInvocationDispatchBatch_InvalidBatchPayload(t *testing.T) {
	// Records with bad PayloadB64 → per-record 400 status, the
	// rest of the batch still proceeds.
	srv, d := newSynthServer(t)
	batchDispatchPerRecordTimeout = 100 * time.Millisecond
	t.Cleanup(func() { batchDispatchPerRecordTimeout = 30 * time.Second })

	w := httptest.NewRecorder()
	body := `{
		"invocation_id": "inv-batch",
		"app_id": "app-batch",
		"records": [
			{"item_identifier": "k1", "payload_b64": "!!!not-base64!!!"},
			{"item_identifier": "k2", "payload_b64": "d29ybGQ="}
		]
	}`
	r := httptest.NewRequest(http.MethodPost, "/v1/invocations:dispatch_batch", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	srv.handleInvocationDispatchBatch(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("code = %d, want 200 (per-record 400 is in the response body, not the HTTP status)", w.Code)
	}
	// Only the well-formed record was dispatched.
	if len(d.invs) != 1 {
		t.Errorf("invocations = %d, want 1 (the bad-b64 record was rejected)", len(d.invs))
	}
}

func TestHandleInvocationDispatch_UsesPrewokenTarget(t *testing.T) {
	srv, d := newSynthServer(t)
	w := httptest.NewRecorder()
	body := `{
		"invocation_id":"inv-target",
		"app_id":"app-1",
		"method":"POST",
		"path":"/e2e",
		"instance_id":"inst-1",
		"node_id":"node-1",
		"deployment_id":"dep-1",
		"wake_id":"wake-1",
		"port":8081
	}`
	r := httptest.NewRequest(http.MethodPost, "/v1/invocations:dispatch", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	srv.handleInvocationDispatch(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200: %s", w.Code, w.Body.String())
	}
	if len(d.targets) != 1 {
		t.Fatalf("target dispatches = %d, want 1", len(d.targets))
	}
	got := d.targets[0]
	if got.InstanceID != "inst-1" || got.NodeID != "node-1" ||
		got.DeploymentID != "dep-1" || got.WakeID != "wake-1" || got.Port != 8081 {
		t.Fatalf("target = %#v", got)
	}
	if len(d.invs) != 0 {
		t.Fatalf("legacy dispatches = %d, want 0", len(d.invs))
	}
}
