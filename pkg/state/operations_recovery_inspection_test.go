// adr: 641
package state

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestOperationInspectionRevisionFencesChangedFileReceipt(t *testing.T) {
	m := NewMemStore()
	ctx := t.Context()
	now := time.Now().UTC()
	limits := api.MustLimitsFor(api.PlanPro)
	m.accounts["account"] = Account{ID: "account", Plan: api.PlanPro, Status: AccountActive}
	m.apps["app"] = App{ID: "app", AccountID: "account"}
	m.deployments["deployment"] = Deployment{ID: "deployment", AppID: "app", Scope: "default", Status: DeployLive}
	m.platformTenants["tenant"] = PlatformTenant{ID: "tenant", AccountID: "account", Status: PlatformTenantActive}
	expiry := now.Add(time.Hour)
	op := Operation{OperationResponse: api.OperationResponse{ID: "operation", Generation: 1, State: api.OperationRequiresReconciliation, ExpiresAt: expiry,
		Artifacts: []api.OperationResultArtifact{{ID: "file", Name: "export.csv", SizeBytes: 3, SHA256: "sha256:file", ExpiresAt: &expiry}}},
		AccountID: "account", AppID: "app", Scope: "default", PlatformTenantID: "tenant", DefinitionID: "definition", DeploymentID: "deployment", CurrentInvocationID: "invocation",
		ValueMaxBytes: limits.MaxSourceBytesPerInvocation, PlanLimits: limits.Operations, ArtifactStorageKeys: map[string]string{"file": "private-storage"}}
	m.invocations["invocation"] = Invocation{ID: "invocation", AccountID: "account", AppID: "app", State: InvocationDeadLetter, Attempts: 1, Source: InvocationAsyncInvoke}
	data := m.operationMemoryLocked()
	data.operations[op.ID] = op
	data.definitions[op.DefinitionID] = OperationDefinition{OperationDefinitionResponse: api.OperationDefinitionResponse{Spec: api.OperationDefinitionSpec{OutputSchema: json.RawMessage(`true`)}}}
	data.blobs["blob"] = OperationResultBlob{ID: "blob", OperationID: op.ID, AccountID: op.AccountID, ExecutionID: op.CurrentInvocationID, Generation: 1, SizeBytes: 3, State: "retained", StorageKey: "private-storage", ExpiresAt: expiry}
	before, _ := json.Marshal([]any{data.operations, data.blobs, data.events, data.reports, data.recoveries, m.invocations})
	p, err := m.PreviewOperationRecovery(ctx, "account", op.ID, api.OperationRecoveryPreviewRequest{ExpectedGeneration: 1, Resolution: "failed"})
	if err != nil || !p.Eligible || len(p.Inspection.Artifacts) != 1 || !p.Inspection.Artifacts[0].Retained {
		t.Fatalf("preview=%+v %v", p, err)
	}
	after, _ := json.Marshal([]any{data.operations, data.blobs, data.events, data.reports, data.recoveries, m.invocations})
	if !reflect.DeepEqual(before, after) {
		t.Fatal("preview mutated memory store")
	}
	blob := data.blobs["blob"]
	blob.State = "deleting"
	data.blobs["blob"] = blob
	current, err := m.InspectOperationRecovery(ctx, "account", op.ID)
	if err != nil || current.InspectionRevision == p.Inspection.InspectionRevision || current.Artifacts[0].Retained {
		t.Fatal("changed file receipt did not invalidate inspection", err)
	}
	request := api.OperationRecoveryRequest{RecoveryID: "decision", ExpectedGeneration: 1, Resolution: "failed", Evidence: "provider ledger checked", ExpectedInspectionRevision: p.Inspection.InspectionRevision}
	if _, err := m.RecoverOperation(ctx, "account", "", op.ID, request); !errors.Is(err, ErrConflict) {
		t.Fatal("changed same-generation receipt applied", err)
	}
	if len(data.recoveries) != 0 || len(data.events) != 0 || data.operations[op.ID].RecoveryCount != 0 {
		t.Fatal("rejected inspection consumed a receipt or quota")
	}
	// Wall-clock retention changes eligibility without inventing a durable change.
	snapshot, _ := m.operationRecoverySnapshotLocked("account", op.ID)
	early := operationRecoveryInspection(snapshot, now)
	late := operationRecoveryInspection(snapshot, expiry.Add(time.Second))
	if early.InspectionRevision != late.InspectionRevision || len(late.RetryBlockers) == 0 {
		t.Fatal("observation time invalidated durable inspection revision")
	}
	snapshot.op.CompletionDelivery = api.OperationDeliveryResponse{State: "dead", Attempts: 8, LastError: "receiver unavailable"}
	if independent := operationRecoveryInspection(snapshot, now); independent.InspectionRevision != early.InspectionRevision {
		t.Fatal("independent delivery invalidated recovery inspection")
	}
}

func TestRecoveryInspectionMissingOperationDoesNotInitializeStorage(t *testing.T) {
	m := NewMemStore()
	if _, err := m.InspectOperationRecovery(t.Context(), "account", "missing"); !errors.Is(err, ErrNotFound) || m.operationData != nil {
		t.Fatal("inspection initialized mutable state", err)
	}
}
