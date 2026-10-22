// adr: 20261022170348
package gateway_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func serveRehearsalExecution(t *testing.T, store *fakeRehearsalPlatformAPIStore, method, path, body, key string) *httptest.ResponseRecorder {
	t.Helper()
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusTeapot)
	})
	handler := gateway.WithRehearsalPlatformAPI(next, func(context.Context, string) (string, error) { return "caller", nil }, store)
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Host = "api.svc.gregale:10081"
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response
}

type capturedRunStart struct {
	Execution struct {
		WorkflowID   string                      `json:"workflow_id"`
		Runtime      string                      `json:"runtime"`
		Profile      string                      `json:"profile"`
		Entrypoint   string                      `json:"entrypoint"`
		SourceSHA256 string                      `json:"source_sha256"`
		FileCount    int                         `json:"file_count"`
		InputSHA256  string                      `json:"input_sha256"`
		Limits       api.ResolvedExecutionLimits `json:"limits"`
		Network      api.ExecutionNetworkPolicy  `json:"network"`
	} `json:"execution"`
}

func hexSHA256(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestRehearsalPlatformAPICapturesContainedRunStarts(t *testing.T) {
	bundle := func(first, second string) string {
		return `{"runtime":"node22","entrypoint":"main.js","files":[` + first + `,` + second + `]}`
	}
	a, b := `{"path":"main.js","content":"Y29uc29sZS5sb2coMSk="}`, `{"path":"lib.js","content":"ZXhwb3J0cy54PTE="}`
	integrations := make([]string, 26)
	for i := range integrations {
		integrations[i] = `"` + fakeCallID(100+i) + `"`
	}
	for _, tc := range []struct {
		name, body string
		plan       api.Plan
		status     int
	}{
		{name: "source", plan: api.PlanPro, body: `{"runtime":"python312","source":"print('secret')","input":{"b":2,"a":1},"workflow_id":"nightly","step_label":"render"}`, status: http.StatusAccepted},
		{name: "bundle", plan: api.PlanPro, body: bundle(a, b), status: http.StatusAccepted},
		{name: "bundle-reordered", plan: api.PlanPro, body: bundle(b, a), status: http.StatusAccepted},
		{name: "free-plan", plan: api.PlanFree, body: `{"runtime":"python312","source":"print(1)"}`, status: http.StatusForbidden},
		{name: "unknown-field", plan: api.PlanPro, body: `{"runtime":"python312","source":"print(1)","nope":1}`, status: http.StatusBadRequest},
		{name: "empty-body", plan: api.PlanPro, status: http.StatusBadRequest},
		{name: "over-public-limit", plan: api.PlanPro, body: `{"runtime":"python312","source":"` + strings.Repeat("x", api.ExecutionSealedPayloadMaxBytes) + `"}`, status: http.StatusRequestEntityTooLarge},
		{name: "artifact-inputs", plan: api.PlanPro, body: `{"runtime":"node22","entrypoint":"main.js","files":[` + a + `],"artifact_inputs":[{"execution_id":"` + fakeCallID(9) + `","name":"out.json","path":"in.json"}]}`, status: http.StatusUnprocessableEntity},
		{name: "unknown-runtime", plan: api.PlanPro, body: `{"runtime":"cobol","source":"x"}`, status: http.StatusUnprocessableEntity},
		{name: "workflow-id-too-long", plan: api.PlanPro, body: `{"runtime":"python312","source":"x","workflow_id":"` + strings.Repeat("w", api.ExecutionWorkflowIDMaxBytes+1) + `"}`, status: http.StatusUnprocessableEntity},
		{name: "too-many-integrations", plan: api.PlanPro, body: `{"runtime":"python312","source":"x","integration_ids":[` + strings.Join(integrations, ",") + `]}`, status: http.StatusUnprocessableEntity},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeRehearsalPlatformAPIStore{run: strings.Repeat("a", 32), plan: tc.plan}
			response := serveRehearsalExecution(t, store, http.MethodPost, "/v1/executions", tc.body, "")
			if response.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", response.Code, tc.status, response.Body)
			}
			if tc.status != http.StatusAccepted {
				if len(store.calls) != 0 {
					t.Fatalf("refused start was captured: %+v", store.calls)
				}
				return
			}
			if len(store.calls) != 1 || response.Header().Get("X-Gregale-Rehearsal") != "captured" {
				t.Fatalf("calls = %+v, headers %v", store.calls, response.Header())
			}
			got := store.calls[0]
			if got.Kind != api.ScenarioPlatformKindExecution || got.Oversized || strings.Contains(string(got.Message), "secret") || strings.Contains(string(got.Message), "Y29uc29sZ") {
				t.Fatalf("captured %+v (%s): the message must keep digests, never source", got, got.Message)
			}
			var message capturedRunStart
			if err := json.Unmarshal(got.Message, &message); err != nil || got.Name != message.Execution.Runtime || message.Execution.Limits.TimeoutMS == 0 ||
				message.Execution.Network.Mode != api.ExecutionNetworkNone || len(message.Execution.SourceSHA256) != 64 {
				t.Fatalf("message = %s %v", got.Message, err)
			}
			switch tc.name {
			case "source":
				if message.Execution.InputSHA256 != hexSHA256(`{"a":1,"b":2}`) || message.Execution.SourceSHA256 != hexSHA256("print('secret')") || message.Execution.WorkflowID != "nightly" {
					t.Fatalf("source capture = %s", got.Message)
				}
			case "bundle", "bundle-reordered":
				if message.Execution.FileCount != 2 || message.Execution.Entrypoint != "main.js" || message.Execution.InputSHA256 != hexSHA256("null") {
					t.Fatalf("bundle capture = %s", got.Message)
				}
			}
			var run api.ExecutionResponse
			if err := json.NewDecoder(response.Body).Decode(&run); err != nil || run.ID != fakeCallID(0) || run.Status != api.ExecutionStatusQueued ||
				string(run.Runtime) != message.Execution.Runtime || run.Limits != message.Execution.Limits || run.Usage != nil || run.FinishedAt != nil {
				t.Fatalf("run response = %+v %v", run, err)
			}
		})
	}
}

func TestRehearsalPlatformAPIBundleDigestIgnoresFileOrder(t *testing.T) {
	a, b := `{"path":"main.js","content":"Y29uc29sZS5sb2coMSk="}`, `{"path":"lib.js","content":"ZXhwb3J0cy54PTE="}`
	var digests []string
	for _, files := range []string{a + "," + b, b + "," + a} {
		store := &fakeRehearsalPlatformAPIStore{run: strings.Repeat("a", 32), plan: api.PlanPro}
		if response := serveRehearsalExecution(t, store, http.MethodPost, "/v1/executions", `{"runtime":"node22","entrypoint":"main.js","files":[`+files+`]}`, ""); response.Code != http.StatusAccepted {
			t.Fatalf("status = %d: %s", response.Code, response.Body)
		}
		var message capturedRunStart
		_ = json.Unmarshal(store.calls[0].Message, &message)
		digests = append(digests, message.Execution.SourceSHA256)
	}
	if digests[0] != digests[1] {
		t.Fatalf("bundle digests differ by file order: %v", digests)
	}
}

func TestRehearsalPlatformAPIRunReadAndCancel(t *testing.T) {
	store := &fakeRehearsalPlatformAPIStore{run: strings.Repeat("a", 32), plan: api.PlanPro}
	start := serveRehearsalExecution(t, store, http.MethodPost, "/v1/executions", `{"runtime":"python312","source":"print(1)","workflow_id":"nightly","step_label":"render"}`, "")
	var started api.ExecutionResponse
	if err := json.NewDecoder(start.Body).Decode(&started); err != nil || start.Code != http.StatusAccepted {
		t.Fatalf("start = %d %v", start.Code, err)
	}
	read := func(want api.ExecutionStatus) api.ExecutionResponse {
		t.Helper()
		response := serveRehearsalExecution(t, store, http.MethodGet, "/v1/executions/"+started.ID, "", "")
		var run api.ExecutionResponse
		if err := json.NewDecoder(response.Body).Decode(&run); err != nil || response.Code != http.StatusOK || run.ID != started.ID || run.Status != want || run.StepLabel != "render" {
			t.Fatalf("read = %d %+v %v, want %s", response.Code, run, err, want)
		}
		return run
	}
	read(api.ExecutionStatusQueued)

	cancel := serveRehearsalExecution(t, store, http.MethodDelete, "/v1/executions/"+started.ID, "", "")
	var cancelled api.ExecutionResponse
	if err := json.NewDecoder(cancel.Body).Decode(&cancelled); err != nil || cancel.Code != http.StatusAccepted || cancelled.Status != api.ExecutionStatusCancelled ||
		cancelled.FinishedAt == nil || cancelled.Usage == nil || cancel.Header().Get("X-Gregale-Rehearsal") != "captured" {
		t.Fatalf("cancel = %d %+v %v", cancel.Code, cancelled, err)
	}
	if last := store.calls[len(store.calls)-1]; last.Kind != api.ScenarioPlatformKindExecutionCancel || last.Address != started.ID || last.Name != "cancel" || string(last.Message) != `{}` {
		t.Fatalf("cancel capture = %+v (%s)", last, last.Message)
	}
	read(api.ExecutionStatusCancelled)

	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/v1/executions/" + fakeCallID(42)},
		{http.MethodDelete, "/v1/executions/" + fakeCallID(42)},
	} {
		if response := serveRehearsalExecution(t, store, tc.method, tc.path, "", ""); response.Code != http.StatusNotFound {
			t.Fatalf("%s %s = %d, want 404: %s", tc.method, tc.path, response.Code, response.Body)
		}
	}
	if len(store.calls) != 2 {
		t.Fatalf("a refused cancellation was captured: %+v", store.calls)
	}
	// Capabilities and the event stream are not captures; they reach next.
	for _, path := range []string{"/v1/executions/capabilities", "/v1/executions/" + started.ID + "/events"} {
		if response := serveRehearsalExecution(t, store, http.MethodGet, path, "", ""); response.Code != http.StatusTeapot {
			t.Fatalf("GET %s = %d, want next", path, response.Code)
		}
	}
}

func TestRehearsalPlatformAPIRunReadRefusesOtherCaptures(t *testing.T) {
	const workflowRun = "44444444-4444-4444-8444-444444444444"
	store := &fakeRehearsalPlatformAPIStore{run: strings.Repeat("a", 32), plan: api.PlanPro,
		platformRecords: map[string]state.ScenarioCapturedPlatformRecord{workflowRun: {Kind: api.ScenarioPlatformKindWorkflowRun, Name: "invoice", Message: json.RawMessage(`{"input":{}}`)}}}
	if response := serveRehearsalExecution(t, store, http.MethodGet, "/v1/executions/"+workflowRun, "", ""); response.Code != http.StatusNotFound {
		t.Fatalf("a workflow run read as a Run: %d %s", response.Code, response.Body)
	}
	if response := serveRehearsalExecution(t, store, http.MethodDelete, "/v1/executions/"+workflowRun, "", ""); response.Code != http.StatusNotFound || len(store.calls) != 0 {
		t.Fatalf("a workflow run cancelled as a Run: %d %+v", response.Code, store.calls)
	}
}

func TestRehearsalPlatformAPIReplaysIdempotentRunStart(t *testing.T) {
	store := &fakeRehearsalPlatformAPIStore{run: strings.Repeat("a", 32), plan: api.PlanPro}
	first := serveRehearsalExecution(t, store, http.MethodPost, "/v1/executions", `{"runtime":"python312","source":"print(1)"}`, "k1")
	// apid replays the first response under a key whatever the new body.
	again := serveRehearsalExecution(t, store, http.MethodPost, "/v1/executions", `{"runtime":"node22","source":"console.log(1)"}`, "k1")
	var a, b api.ExecutionResponse
	_ = json.NewDecoder(first.Body).Decode(&a)
	_ = json.NewDecoder(again.Body).Decode(&b)
	if first.Code != http.StatusAccepted || again.Code != http.StatusAccepted || a.ID != b.ID || b.Runtime != "python312" ||
		again.Header().Get("Idempotent-Replayed") != "true" || len(store.calls) != 1 {
		t.Fatalf("replay: %d/%d ids %q/%q runtime %q header %q calls %d", first.Code, again.Code, a.ID, b.ID, b.Runtime, again.Header().Get("Idempotent-Replayed"), len(store.calls))
	}
}
