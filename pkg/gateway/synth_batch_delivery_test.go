package gateway

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 1321 — batch delivery invokes once per dispatch batch and maps
// batchItemFailures back to per-record results.

func runBatchDelivery(t *testing.T, disp *fakeBatchDispatcher, req batchDispatchRequest) map[string]batchDispatchResult {
	t.Helper()
	body, _ := json.Marshal(req)
	rec := httptest.NewRecorder()
	srv := &SynthServer{dispatcher: disp, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	srv.handleInvocationDispatchBatch(rec, httptest.NewRequest(http.MethodPost, "/v1/invocations:dispatch_batch", bytes.NewReader(body)))
	var out batchDispatchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v (%s)", err, rec.Body)
	}
	byItem := map[string]batchDispatchResult{}
	for _, r := range out.Results {
		byItem[r.ItemIdentifier] = r
	}
	if len(byItem) != len(req.Records) {
		t.Fatalf("results = %+v, want one per record", out.Results)
	}
	return byItem
}

func batchDeliveryRequest() batchDispatchRequest {
	b64 := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	return batchDispatchRequest{InvocationID: "inv", AppID: "app-1", Source: "esm", TriggerID: "trig-1", Delivery: "batch",
		Records: []batchDispatchRecord{
			{ItemIdentifier: "a", PayloadB64: b64(`{"n":1}`), Headers: map[string]string{"k": "v"}},
			{ItemIdentifier: "b", PayloadB64: b64("raw bytes")},
			{ItemIdentifier: "c", PayloadB64: b64(`{"n":3}`)},
		}}
}

func TestBatchDeliveryInvokesOnceWithEveryRecord(t *testing.T) {
	disp := &fakeBatchDispatcher{result: state.Invocation{State: "succeeded", Result: json.RawMessage(`{"batchItemFailures":[{"itemIdentifier":"b"}]}`)}}
	results := runBatchDelivery(t, disp, batchDeliveryRequest())
	if len(disp.calls) != 1 {
		t.Fatalf("invocations = %d, want one for the whole batch", len(disp.calls))
	}
	call := disp.calls[0]
	var body batchInvocationBody
	if err := json.Unmarshal(call.Payload, &body); err != nil || len(body.Records) != 3 {
		t.Fatalf("invocation body = %s, %v", call.Payload, err)
	}
	if string(body.Records[0].Data) != `{"n":1}` || body.Records[0].DataBase64 != "" || body.Records[0].Headers["k"] != "v" {
		t.Fatalf("JSON record = %+v, want data inline", body.Records[0])
	}
	if raw, _ := base64.StdEncoding.DecodeString(body.Records[1].DataBase64); string(raw) != "raw bytes" || body.Records[1].Data != nil {
		t.Fatalf("binary record = %+v, want data_base64", body.Records[1])
	}
	var headers map[string]string
	if err := json.Unmarshal(call.Headers, &headers); err != nil || headers["X-Gregale-Batch-Size"] != "3" || call.Path != "/_triggers/esm/trig-1" {
		t.Fatalf("invocation headers/path = %s %s", call.Headers, call.Path)
	}
	if results["a"].Status != "succeeded" || results["c"].Status != "succeeded" {
		t.Fatalf("unlisted records = %+v / %+v, want succeeded", results["a"], results["c"])
	}
	if results["b"].Status != "retry" || results["b"].Code != "function_failed" {
		t.Fatalf("listed record = %+v, want retry function_failed", results["b"])
	}
}

func TestBatchDeliveryFailureRetriesEveryRecord(t *testing.T) {
	for name, tc := range map[string]struct {
		disp *fakeBatchDispatcher
		code string
	}{
		"function error":     {disp: &fakeBatchDispatcher{result: state.Invocation{State: "failed"}}, code: "function_state_failed"},
		"malformed failures": {disp: &fakeBatchDispatcher{result: state.Invocation{State: "succeeded", Result: json.RawMessage(`{"batchItemFailures":"b"}`)}}, code: "response_malformed"},
	} {
		t.Run(name, func(t *testing.T) {
			results := runBatchDelivery(t, tc.disp, batchDeliveryRequest())
			for item, r := range results {
				if r.Status != "retry" || r.Code != tc.code {
					t.Fatalf("%s = %+v, want retry %s", item, r, tc.code)
				}
			}
		})
	}
	errDisp := &fakeBatchDispatcher{blockCh: make(chan struct{}), blockErr: errors.New("boom")}
	close(errDisp.blockCh)
	for item, r := range runBatchDelivery(t, errDisp, batchDeliveryRequest()) {
		if r.Status != "retry" || r.Code != "invoke_error" {
			t.Fatalf("%s after invoke error = %+v", item, r)
		}
	}
}

func TestBatchDeliveryFallsBackPerRecord(t *testing.T) {
	disp := &fakeBatchDispatcher{result: state.Invocation{State: "succeeded", Result: json.RawMessage(`null`)}}
	req := batchDeliveryRequest()
	req.Delivery = ""
	runBatchDelivery(t, disp, req)
	if len(disp.calls) != 3 {
		t.Fatalf("record delivery invocations = %d, want 3", len(disp.calls))
	}
	// A record with a durable invocation identity keeps per-record delivery.
	disp = &fakeBatchDispatcher{result: state.Invocation{State: "succeeded", Result: json.RawMessage(`null`)}}
	req = batchDeliveryRequest()
	req.Records[0].InvocationID = "11111111-1111-4111-8111-111111111111"
	req.Records[0].ItemIdentifier = req.Records[0].InvocationID
	req.Records[0].InvocationAttempt = 1
	runBatchDelivery(t, disp, req)
	if len(disp.calls) != 3 {
		t.Fatalf("invocations with a durable record = %d, want per-record delivery", len(disp.calls))
	}
	// An undecodable payload is dead-lettered and kept out of the batch.
	disp = &fakeBatchDispatcher{result: state.Invocation{State: "succeeded", Result: json.RawMessage(`null`)}}
	req = batchDeliveryRequest()
	req.Records[2].PayloadB64 = "%%%"
	results := runBatchDelivery(t, disp, req)
	var body batchInvocationBody
	if len(disp.calls) != 1 || json.Unmarshal(disp.calls[0].Payload, &body) != nil || len(body.Records) != 2 || results["c"].Code != "payload_b64_invalid" {
		t.Fatalf("bad payload: calls=%d results=%+v", len(disp.calls), results)
	}
}
