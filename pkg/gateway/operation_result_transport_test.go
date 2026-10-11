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

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/exclusivework"
	"github.com/onebox-faas/faas/pkg/httpjson"
	"github.com/onebox-faas/faas/pkg/internalsvc"
	"github.com/onebox-faas/faas/pkg/state"
)

type operationResultSynthDispatcher struct {
	fakeSynthDispatcher
	result json.RawMessage
}

func (d *operationResultSynthDispatcher) Invoke(_ context.Context, _ string, inv state.Invocation) (state.Invocation, error) {
	inv.Result = d.result
	inv.State = state.InvocationDispatching
	return inv, nil
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
