// spec: §4.9

package sched

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// TestWorkflowStepUsesItsDeclaredTimeout pins H9-151: on production-us a step
// declaring `timeout: 2m` was cut at exactly 30 s on every attempt
// ("Client.Timeout exceeded while awaiting headers") because the shared synth
// client's fixed Timeout outranked the step's context deadline, so the
// per-plan WorkflowStepMaxTimeout (10 min to 2 h) was unreachable.
func TestWorkflowStepUsesItsDeclaredTimeout(t *testing.T) {
	const handlerTime = 150 * time.Millisecond
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(handlerTime):
		case <-r.Context().Done():
			return
		}
		_, _ = w.Write([]byte(`{"state":"dispatching","status_code":200,"result":{"ok":true}}`))
	}))
	defer srv.Close()
	client := srv.Client()
	client.Timeout = 50 * time.Millisecond // stands in for the 30 s synth limit
	h := &httpGatewaySynth{client: client, basePrefix: srv.URL, mintInternalSvcToken: func(string) (string, error) { return "test-token", nil }}
	identity := WorkflowStepIdentity{RunID: "run-1"}
	headers := func() map[string]string { return map[string]string{"X-Faas-Workflow-Run-Id": "run-1"} }

	status, body, _, err := h.ExecuteWorkflowStepWithRetryAfter(t.Context(), "app-1", identity, "/slow", http.MethodPost, headers(), nil, time.Second, "", 0)
	if err != nil || status != http.StatusOK || string(body) != `{"ok":true}` {
		t.Fatalf("step with a 1 s timeout and a 150 ms handler: status=%d body=%s err=%v", status, body, err)
	}

	// The declared timeout still bounds the attempt.
	_, _, _, err = h.ExecuteWorkflowStepWithRetryAfter(t.Context(), "app-1", identity, "/slow", http.MethodPost, headers(), nil, 100*time.Millisecond, "", 0)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("step outliving its 100 ms timeout: err=%v, want context.DeadlineExceeded", err)
	}

	// Ordinary synth calls keep the client's limit.
	_, err = h.InvokeWithWake(t.Context(), "app-1",
		state.Invocation{ID: "inv-1", AppID: "app-1", Source: state.InvocationAsyncInvoke, Method: http.MethodPost, Path: "/slow"},
		WakeResult{InstanceID: "instance-1", NodeID: "node-1"})
	if err == nil {
		t.Fatal("an async invocation outlived the synth client's limit")
	}
}
