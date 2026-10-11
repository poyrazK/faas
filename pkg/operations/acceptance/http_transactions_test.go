// adr: 638
package acceptance_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func testOperationHTTPTransactionRecovery(t *testing.T, s operationLifecycleStore) {
	t.Helper()
	ctx, account, app, ordinary, tenant, other := operationFixture(t, s)
	spec := ordinary.Spec
	spec.Name, spec.Path = "transaction-export", "/transaction-exports"
	spec.HTTPTransactionVersion = api.OperationHTTPTransactionVersion
	definition, err := s.PutOperationDefinition(ctx, state.OperationDefinition{AccountID: account.ID,
		OperationDefinitionResponse: api.OperationDefinitionResponse{AppID: app.ID, DeploymentID: ordinary.DeploymentID, Scope: ordinary.Scope, Spec: spec}})
	if err != nil {
		t.Fatal(err)
	}
	node, err := s.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	instance, err := s.CreateInstance(ctx, app.ID, definition.DeploymentID, "stopped", 128, node.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	op, _, err := s.AdmitOperation(ctx, state.OperationAdmission{AccountID: account.ID, DefinitionID: definition.ID,
		PlatformTenantID: tenant.ID, IdempotencyKey: "transaction", Input: []byte(`{"count":1}`)})
	if err != nil {
		t.Fatal(err)
	}
	claim := func(id string) state.Invocation {
		t.Helper()
		inv, err := s.ClaimInvocationWithCap(ctx, id, instance.ID, 60, 100)
		if err != nil {
			t.Fatal(err)
		}
		var proof map[string]string
		if err := json.Unmarshal(inv.Headers, &proof); err != nil {
			t.Fatal(err)
		}
		if proof[api.OperationTransactionVersionHeader] != "1" || proof[api.OperationResultMaxBytesHeader] != strconv.Itoa(op.ValueMaxBytes) || proof[api.ExclusiveOperationIDHeader] != "" {
			t.Fatalf("incorrect customer transaction claim: %v", proof)
		}
		// Wire claims cannot override the immutable definition's negotiated limit.
		proof[api.OperationTransactionVersionHeader], proof[api.OperationResultMaxBytesHeader] = "999", "1"
		wire := inv
		wire.OperationID, wire.AccountID = "", ""
		wire.Headers, _ = json.Marshal(proof)
		delivered, err := state.AdmitPlatformTenantInvocation(ctx, s, app.ID, wire)
		if err != nil {
			t.Fatal(err)
		}
		var headers map[string]string
		_ = json.Unmarshal(delivered.Headers, &headers)
		if headers[api.OperationTransactionVersionHeader] != "1" || headers[api.OperationResultMaxBytesHeader] != strconv.Itoa(op.ValueMaxBytes) {
			t.Fatal("synthetic transport trusted wire negotiation")
		}
		return delivered
	}
	original := claim(op.CurrentInvocationID)
	// Business commit may have happened; preserve uncertainty until explicit recovery.
	if err := s.FailInvocation(ctx, original.ID, "response lost after database commit", time.Second, 10, state.WithClaimAttempt(original.Attempts)); err != nil {
		t.Fatal(err)
	}
	request := api.OperationRecoveryRequest{RecoveryID: "replay-receipt", ExpectedGeneration: 1, Resolution: "safe_to_retry", Evidence: "handler uses the result-only PostgreSQL receipt transaction; replay preserves committed business writes"}
	if _, err := s.RecoverOperation(ctx, account.ID, other.ID, op.ID, request); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("other customer authorized receipt recovery: %v", err)
	}
	recovered, err := s.RecoverOperation(ctx, account.ID, tenant.ID, op.ID, request)
	if err != nil || recovered.ID != op.ID || recovered.Generation != 2 || recovered.CurrentInvocationID == original.ID {
		t.Fatalf("recovery changed logical identity: %+v %v", recovered, err)
	}
	current := claim(recovered.CurrentInvocationID)
	if _, err := state.AdmitPlatformTenantInvocation(ctx, s, app.ID, original); err == nil {
		t.Fatal("stale transaction execution dispatched after recovery")
	}
	result := json.RawMessage(`{"file":"committed.csv"}`)
	if err := s.CompleteKeyedInvocation(ctx, original.ID, original.Attempts, result); err == nil {
		t.Fatal("stale transaction result bypassed completion fence")
	}
	if err := s.CompleteKeyedInvocation(ctx, current.ID, current.Attempts, result); err != nil {
		t.Fatal(err)
	}
	finished, err := s.OperationByID(ctx, account.ID, tenant.ID, op.ID)
	var compact bytes.Buffer
	compactErr := json.Compact(&compact, finished.Result)
	if err != nil || finished.State != api.OperationSucceeded || compactErr != nil || !bytes.Equal(compact.Bytes(), result) {
		t.Fatalf("saved business result did not complete typed Operation: %+v %v", finished, err)
	}
	if _, err := s.OperationByID(ctx, account.ID, other.ID, op.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("saved result crossed customer ownership: %v", err)
	}
	page, err := s.OperationExecutions(ctx, account.ID, op.ID, 0, 10)
	if err != nil || len(page.Executions) != 2 {
		t.Fatalf("execution history did not retain both attempts: %+v %v", page, err)
	}
}

func TestMemOperationHTTPTransactionRecovery(t *testing.T) {
	testOperationHTTPTransactionRecovery(t, state.NewMemStore())
}

func TestPgOperationHTTPTransactionRecovery(t *testing.T) {
	s, _ := pgStore(t)
	testOperationHTTPTransactionRecovery(t, s)
}
