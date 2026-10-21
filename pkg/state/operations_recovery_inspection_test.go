// adr: 660
package state_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestOperationInspectionRevisionFencesChangedFileReceipt(t *testing.T) {
	m := state.NewMemStore()
	ctx := t.Context()
	now := time.Now().UTC()
	limits := api.MustLimitsFor(api.PlanPro)
	(*m.Accounts())["account"] = state.Account{ID: "account", Plan: api.PlanPro, Status: state.AccountActive}
	(*m.Apps())["app"] = state.App{ID: "app", AccountID: "account"}
	(*m.Deployments())["deployment"] = state.Deployment{ID: "deployment", AppID: "app", Scope: "default", Status: state.DeployLive}
	(*m.PlatformTenants())["tenant"] = state.PlatformTenant{ID: "tenant", AccountID: "account", Status: state.PlatformTenantActive}
	expiry := now.Add(time.Hour)
	op := state.Operation{OperationResponse: api.OperationResponse{ID: "operation", Generation: 1, State: api.OperationRequiresReconciliation, ExpiresAt: expiry,
		Artifacts: []api.OperationResultArtifact{{ID: "file", Name: "export.csv", SizeBytes: 3, SHA256: "sha256:file", ExpiresAt: &expiry}}},
		AccountID: "account", AppID: "app", Scope: "default", PlatformTenantID: "tenant", DefinitionID: "definition", DeploymentID: "deployment", CurrentInvocationID: "invocation",
		ValueMaxBytes: limits.MaxSourceBytesPerInvocation, PlanLimits: limits.Operations, ArtifactStorageKeys: map[string]string{"file": "private-storage"}}
	(*m.Invocations())["invocation"] = state.Invocation{ID: "invocation", AccountID: "account", AppID: "app", State: state.InvocationDeadLetter, Attempts: 1, Source: state.InvocationAsyncInvoke}
	data := m.OperationMemoryLocked()
	(*data.Operations())[op.ID] = op
	(*data.Definitions())[op.DefinitionID] = state.OperationDefinition{OperationDefinitionResponse: api.OperationDefinitionResponse{Spec: api.OperationDefinitionSpec{OutputSchema: json.RawMessage(`true`)}}}
	(*data.Blobs())["blob"] = state.OperationResultBlob{ID: "blob", OperationID: op.ID, AccountID: op.AccountID, ExecutionID: op.CurrentInvocationID, Generation: 1, SizeBytes: 3, State: "retained", StorageKey: "private-storage", ExpiresAt: expiry}
	before, _ := json.Marshal([]any{(*data.Operations()), (*data.Blobs()), (*data.Events()), (*data.Reports()), (*data.Recoveries()), (*m.Invocations())})
	p, err := m.PreviewOperationRecovery(ctx, "account", op.ID, api.OperationRecoveryPreviewRequest{ExpectedGeneration: 1, Resolution: "failed"})
	if err != nil || !p.Eligible || len(p.Inspection.Artifacts) != 1 || !p.Inspection.Artifacts[0].Retained {
		t.Fatalf("preview=%+v %v", p, err)
	}
	after, _ := json.Marshal([]any{(*data.Operations()), (*data.Blobs()), (*data.Events()), (*data.Reports()), (*data.Recoveries()), (*m.Invocations())})
	if !reflect.DeepEqual(before, after) {
		t.Fatal("preview mutated memory store")
	}
	blob := (*data.Blobs())["blob"]
	blob.State = "deleting"
	(*data.Blobs())["blob"] = blob
	current, err := m.InspectOperationRecovery(ctx, "account", op.ID)
	if err != nil || current.InspectionRevision == p.Inspection.InspectionRevision || current.Artifacts[0].Retained {
		t.Fatal("changed file receipt did not invalidate inspection", err)
	}
	request := api.OperationRecoveryRequest{RecoveryID: "decision", ExpectedGeneration: 1, Resolution: "failed", Evidence: "provider ledger checked", ExpectedInspectionRevision: p.Inspection.InspectionRevision}
	if _, err := m.RecoverOperation(ctx, "account", "", op.ID, request); !errors.Is(err, state.ErrConflict) {
		t.Fatal("changed same-generation receipt applied", err)
	}
	if len((*data.Recoveries())) != 0 || len((*data.Events())) != 0 || (*data.Operations())[op.ID].RecoveryCount != 0 {
		t.Fatal("rejected inspection consumed a receipt or quota")
	}
	// Wall-clock retention changes eligibility without inventing a durable change.
	snapshot, _ := m.OperationRecoverySnapshotLocked("account", op.ID)
	early := state.OperationRecoveryInspection(snapshot, now)
	late := state.OperationRecoveryInspection(snapshot, expiry.Add(time.Second))
	if early.InspectionRevision != late.InspectionRevision || len(late.RetryBlockers) == 0 {
		t.Fatal("observation time invalidated durable inspection revision")
	}
	(*snapshot.Op()).CompletionDelivery = api.OperationDeliveryResponse{State: "dead", Attempts: 8, LastError: "receiver unavailable"}
	if independent := state.OperationRecoveryInspection(snapshot, now); independent.InspectionRevision != early.InspectionRevision {
		t.Fatal("independent delivery invalidated recovery inspection")
	}
}

func TestRecoveryInspectionMissingOperationDoesNotInitializeStorage(t *testing.T) {
	m := state.NewMemStore()
	if _, err := m.InspectOperationRecovery(t.Context(), "account", "missing"); !errors.Is(err, state.ErrNotFound) || (*m.OperationData()) != nil {
		t.Fatal("inspection initialized mutable state", err)
	}
}
