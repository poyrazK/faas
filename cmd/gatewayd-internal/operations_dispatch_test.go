// ADR-521: customer operations preserve ownership, execution fences and independent delivery.
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestSynthAdapterOperationProofAndPrivateRevision(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "operation-synth@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "operation-synth", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	tenant, _, err := store.CreatePlatformTenant(ctx, acct.ID, "alice", "Alice", 100)
	if err != nil {
		t.Fatal(err)
	}
	def, err := store.PutOperationDefinition(ctx, state.OperationDefinition{AccountID: acct.ID, OperationDefinitionResponse: api.OperationDefinitionResponse{AppID: app.ID, DeploymentID: dep.ID, Scope: dep.Scope, Spec: api.OperationDefinitionSpec{Name: "export", Method: "POST", Path: "/exports", Owner: api.OperationOwnerPlatformTenant, InputSchema: []byte(`true`), OutputSchema: []byte(`true`), ProgressStages: []string{"generating"}}}})
	if err != nil {
		t.Fatal(err)
	}
	op, _, err := store.AdmitOperation(ctx, state.OperationAdmission{AccountID: acct.ID, DefinitionID: def.ID, PlatformTenantID: tenant.ID, IdempotencyKey: "export", Input: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := store.CreateInstance(ctx, app.ID, dep.ID, string(state.StateRunning), 128, uuid.NewString(), "")
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimInvocationWithCap(ctx, op.CurrentInvocationID, instance.ID, 60, 100)
	if err != nil {
		t.Fatal(err)
	}
	var proof map[string]string
	_ = json.Unmarshal(claimed.Headers, &proof)
	calls := 0
	adapter := &synthAdapter{store: store, forward: func(gateway.Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			if r.URL.Path != "/exports" || r.Header.Get(api.OperationIDHeader) != op.ID || r.Header.Get(api.OperationCapabilityHeader) != proof[api.OperationCapabilityHeader] || r.Header.Get(api.InvocationIDHeader) != claimed.ID {
				t.Fatalf("operation delivery context: %s %v", r.URL.Path, r.Header)
			}
			_, _ = w.Write([]byte(`{"ok":true}`))
		})
	}}
	wire := claimed
	wire.OperationID, wire.AccountID = "", ""
	wire.Path = "/forged"
	target := gateway.Target{AppID: app.ID, DeploymentID: dep.ID, InstanceID: instance.ID, NodeID: instance.NodeID}
	if _, status, err := adapter.InvokeWithTargetStatus(ctx, app.ID, wire, target); err != nil || status != 200 || calls != 1 {
		t.Fatalf("private revision operation dispatch: %d %v calls=%d", status, err, calls)
	}
	proof[api.OperationCapabilityHeader] = "forged"
	wire.Headers, _ = json.Marshal(proof)
	if _, _, err := adapter.InvokeWithTargetStatus(ctx, app.ID, wire, target); err == nil || calls != 1 {
		t.Fatal("forged operation proof reached worker")
	}
}
