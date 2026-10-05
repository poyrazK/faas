// adr: 521
package acceptance_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func testCustomerOperationManagedContractIsolation(t *testing.T, s operationLifecycleStore) {
	t.Helper()
	ctx, acct, app, def, tenant, _ := operationFixture(t, s)
	op, _, err := s.AdmitOperation(ctx, state.OperationAdmission{AccountID: acct.ID, DefinitionID: def.ID,
		PlatformTenantID: tenant.ID, IdempotencyKey: "managed-contract-isolation", Input: []byte(`{"count":1}`)})
	if err != nil {
		t.Fatal(err)
	}
	node, err := s.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	instance, err := s.CreateInstance(ctx, app.ID, def.DeploymentID, "stopped", 128, node.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimInvocationWithCap(ctx, op.CurrentInvocationID, instance.ID, 60, 100)
	if err != nil {
		t.Fatal(err)
	}
	var headers map[string]string
	if err := json.Unmarshal(claimed.Headers, &headers); err != nil {
		t.Fatal(err)
	}
	if api.OperationIDHeader == api.ExclusiveOperationIDHeader || headers[api.OperationIDHeader] != op.ID || headers[api.ExclusiveOperationIDHeader] != "" {
		t.Fatalf("customer claim crossed the managed operation identity boundary: %+v", headers)
	}
	// A managed operation identity cannot substitute for the customer operation
	// proof, even when the invocation and attempt capability are otherwise valid.
	delete(headers, api.OperationIDHeader)
	headers[api.ExclusiveOperationIDHeader] = op.ID
	foreign := claimed
	foreign.OperationID = ""
	foreign.Headers, err = json.Marshal(headers)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.AdmitPlatformTenantInvocation(ctx, s, app.ID, foreign); !errors.Is(err, state.ErrOperationStaleAttempt) {
		t.Fatalf("managed header substituted for customer authority: %v", err)
	}
	// An ordinary HTTP definition validates the whole response body. Only an
	// explicitly negotiated execution adapter may decode managed result envelopes.
	envelope := json.RawMessage(`{"gregale_operation_result":1,"result":{"file":"export.csv"},"effects":[]}`)
	if err := s.CompleteKeyedInvocation(ctx, claimed.ID, claimed.Attempts, envelope); err != nil {
		t.Fatal(err)
	}
	finished, err := s.OperationByID(ctx, acct.ID, tenant.ID, op.ID)
	if err != nil {
		t.Fatal(err)
	}
	if finished.State != api.OperationRequiresReconciliation || finished.FailureCode != "invalid_output" || len(finished.Result) != 0 {
		t.Fatalf("ordinary HTTP silently decoded a managed result: %+v", finished)
	}
	inv, err := s.InvocationByID(ctx, claimed.ID)
	var retained, original any
	retainedErr := json.Unmarshal(inv.Result, &retained)
	originalErr := json.Unmarshal(envelope, &original)
	retainedJSON, _ := json.Marshal(retained)
	originalJSON, _ := json.Marshal(original)
	if err != nil || inv.State != state.InvocationCompleted || retainedErr != nil || originalErr != nil || string(retainedJSON) != string(originalJSON) {
		t.Fatalf("backend completion evidence lost: %+v %v", inv, err)
	}
}

func TestMemCustomerOperationManagedContractIsolation(t *testing.T) {
	testCustomerOperationManagedContractIsolation(t, state.NewMemStore())
}

func TestPgCustomerOperationManagedContractIsolation(t *testing.T) {
	s, _ := pgStore(t)
	testCustomerOperationManagedContractIsolation(t, s)
}
