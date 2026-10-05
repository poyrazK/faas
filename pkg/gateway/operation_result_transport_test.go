// adr: 488
package gateway

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/exclusivework"
	"github.com/onebox-faas/faas/pkg/httpjson"
	"github.com/onebox-faas/faas/pkg/internalsvc"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/trafficrevocation"
)

type operationResultSynthDispatcher struct {
	fakeSynthDispatcher
	result     json.RawMessage
	invocation state.Invocation
	handoff    string
}

func (d *operationResultSynthDispatcher) Invoke(ctx context.Context, _ string, inv state.Invocation) (state.Invocation, error) {
	d.invocation = inv
	d.handoff = trafficrevocation.HandoffValue(ctx)
	inv.Result = d.result
	inv.State = state.InvocationDispatching
	return inv, nil
}

func TestManagedWorkflowOperationPreservesTrafficSecurityHandoff(t *testing.T) {
	accountID, appID, runID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	operationID, err := api.ManagedWorkflowStepOperationID(runID, "charge")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := trafficrevocation.EncodeSnapshot(map[trafficrevocation.Scope]trafficrevocation.State{
		{Kind: "account", ID: accountID}: {Revision: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	token, err := internalsvc.Mint("schedd", 30*time.Second, nil, priv, internalsvc.KidFromPub(pub))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, snapshot string
		generation     int64
		status         int
	}{
		{"active", snapshot, 1, http.StatusOK},
		{"malformed-handoff", "invalid", 1, http.StatusServiceUnavailable},
		{"obsolete-generation", snapshot, 2, http.StatusConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dispatcher := &operationResultSynthDispatcher{result: json.RawMessage(`{"ok":true}`)}
			srv := NewSynthServer("", dispatcher, nil)
			srv.internalSvcVerifier = &testInternalSvcVerifier{allowed: map[string]ed25519.PublicKey{"schedd": pub}}
			srv.workflowAdmission = func(_ context.Context, gotApp, gotRun, tenantID, step string, attempt int) error {
				if gotApp != appID || gotRun != runID || tenantID != "" || step != "charge" || attempt != 1 {
					t.Fatalf("workflow admission identity=%s/%s/%s/%s/%d", gotApp, gotRun, tenantID, step, attempt)
				}
				return nil
			}
			srv.managedWorkflowOperationIdentity = func(_ context.Context, gotApp, gotRun, step string) (string, bool, error) {
				if gotApp != appID || gotRun != runID || step != "charge" {
					t.Fatalf("operation identity=%s/%s/%s", gotApp, gotRun, step)
				}
				return accountID, true, nil
			}
			body, err := json.Marshal(invocationDispatchRequest{
				InvocationID: "workflow-inv", AppID: appID, AccountID: accountID, Source: "workflow",
				SecuritySnapshot:                   tc.snapshot,
				OperationResultVersion:             api.ManagedOperationResultVersion,
				ManagedWorkflowOperationID:         operationID,
				ManagedWorkflowOperationGeneration: tc.generation,
				Headers: map[string]string{
					"X-Faas-Internal-Wake": "workflow", "X-Faas-Workflow-Run-Id": runID,
					"X-Faas-Workflow-Step": "charge", "X-Faas-Workflow-Attempt": "1",
					"security_snapshot": "forged",
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, "/v1/invocations:dispatch", bytes.NewReader(body))
			req.Header.Set("Authorization", "Bearer "+token)
			response := httptest.NewRecorder()
			srv.handleInvocationDispatch(response, req)
			if response.Code != tc.status {
				t.Fatalf("status=%d want=%d body=%s", response.Code, tc.status, response.Body.String())
			}
			if tc.status != http.StatusOK {
				if dispatcher.invocation.ID != "" {
					t.Fatal("refused workflow reached dispatcher")
				}
				return
			}
			inv := dispatcher.invocation
			if inv.AccountID != accountID || inv.ManagedOperationAccountID != accountID || inv.ManagedOperationID != operationID || inv.ManagedOperationGeneration != 1 || inv.OperationResultVersion != api.ManagedOperationResultVersion {
				t.Fatalf("operation/account identity changed: %+v", inv)
			}
			if dispatcher.handoff != snapshot {
				t.Fatal("owner security handoff was dropped or replaced by guest metadata")
			}
		})
	}
}

func TestManagedOperationResultTransportPreservesByteBudget(t *testing.T) {
	prefix := `{"gregale_operation_result":1,"result":"`
	suffix := `","effects":[]}`
	pattern := "<>&\u2028\u2029"
	budget := api.MaxExclusiveResultBytes - len(prefix) - len(suffix)
	result := json.RawMessage(prefix + strings.Repeat(pattern, budget/len(pattern)) + strings.Repeat("x", budget%len(pattern)) + suffix)
	dispatcher := &operationResultSynthDispatcher{result: result}
	srv := NewSynthServer("", dispatcher, nil)
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	token, err := internalsvc.Mint("schedd", 30*time.Second, nil, priv, internalsvc.KidFromPub(pub))
	if err != nil {
		t.Fatal(err)
	}
	srv.internalSvcVerifier = &testInternalSvcVerifier{allowed: map[string]ed25519.PublicKey{"schedd": pub}}
	body, err := json.Marshal(invocationDispatchRequest{
		InvocationID: "op-1", AppID: "app-1", Source: string(state.InvocationExclusiveOperation),
		ExclusiveClaim:         &exclusivework.Claim{AccountID: "account-1", OperationID: "op-1", Generation: 1},
		OperationResultVersion: api.ManagedOperationResultVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/invocations:dispatch", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	srv.handleInvocationDispatch(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var decoded struct {
		Result json.RawMessage `json:"result"`
	}
	if err := httpjson.Decode(response.Body, api.MaxExclusiveGatewayResponseBytes, &decoded); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded.Result, result) {
		t.Fatalf("result bytes changed: got=%d want=%d", len(decoded.Result), len(result))
	}
}
