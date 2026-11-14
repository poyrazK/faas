// adr: 20261111163000
package gateway

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/httpjson"
	"github.com/onebox-faas/faas/pkg/internalsvc"
	"github.com/onebox-faas/faas/pkg/state"
)

type workflowRegressionDispatcher struct {
	result json.RawMessage
	calls  int
}

func (*workflowRegressionDispatcher) Wake(context.Context, string) error { return nil }
func (d *workflowRegressionDispatcher) Invoke(_ context.Context, _ string, inv state.Invocation) (state.Invocation, error) {
	d.calls++
	inv.State = state.InvocationCompleted
	inv.Result = d.result
	return inv, nil
}

// Admission is stubbed because this test isolates the production gateway's
// response encoding and the exact bounded decoder used by the scheduler.
type workflowRegressionVerifier struct{}

func (workflowRegressionVerifier) Verify(context.Context, string) (string, error) {
	return "schedd", nil
}
func (workflowRegressionVerifier) AllowedSvcNames() []string { return []string{"schedd"} }

func TestNativeWorkflowResponseFitsScheduler(t *testing.T) {
	raw := []byte(`{"protocol":"gregale.workflow.code/v1","ops":[{"seq":0,"name":"work","kind":"step","status":"succeeded","output":"` + strings.Repeat("<", 200*1024) + `"}],"result":{"status":"running"}}`)
	if !json.Valid(raw) || len(raw) >= 960*1024 {
		t.Fatalf("invalid SDK fixture: %d bytes", len(raw))
	}
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	token, err := internalsvc.Mint("schedd", time.Minute, map[string]any{"app_id": "app-1"}, priv, internalsvc.KidFromPub(pub))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		result []byte
	}{
		{"plain_control", bytes.ReplaceAll(raw, []byte("<"), []byte("x"))},
		{"html_characters", raw},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dispatcher := &workflowRegressionDispatcher{result: tc.result}
			server := NewSynthServer("unused", dispatcher, nil)
			server.internalSvcVerifier = workflowRegressionVerifier{}
			server.workflowAdmission = func(context.Context, string, string, string, string, int) error { return nil }
			request := invocationDispatchRequest{
				InvocationID: "workflow-inv-1", AppID: "app-1", Source: "workflow", Method: "POST", Path: "/entry",
				Headers: map[string]string{
					"X-Faas-Internal-Wake": "workflow", "X-Faas-Workflow-Run-Id": "run-1",
					"X-Faas-Workflow-Step": "$driver", "X-Faas-Workflow-Attempt": "1", "X-Faas-Workflow-Mode": "code",
				},
			}
			body, err := json.Marshal(request)
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, "/v1/invocations:dispatch", bytes.NewReader(body))
			req.Header.Set("Authorization", "Bearer "+token)
			rec := httptest.NewRecorder()
			server.handleInvocationDispatch(rec, req)
			if rec.Code != http.StatusOK || dispatcher.calls != 1 {
				t.Fatalf("unexpected admission/dispatch: status=%d calls=%d body=%s", rec.Code, dispatcher.calls, rec.Body.String())
			}
			// pkg/sched/loop.go uses gatewayInvocationResponseMaxBytes=1<<20
			// and httpjson.Decode for native workflow responses.
			var decoded struct {
				State  string
				Result json.RawMessage
			}
			err = httpjson.Decode(bytes.NewReader(rec.Body.Bytes()), 1<<20, &decoded)
			t.Logf("sdk_bytes=%d gateway_bytes=%d scheduler_limit=%d decode_error=%v", len(tc.result), rec.Body.Len(), 1<<20, err)
			if err != nil {
				t.Fatalf("valid SDK result cannot reach scheduler: %v", err)
			}
		})
	}
}
