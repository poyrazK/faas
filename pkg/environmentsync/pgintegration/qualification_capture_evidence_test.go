package pgintegration_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

// These are storage-contract fixtures, not native Firecracker acceptance.
func captureProof(frame state.EnvironmentQualificationExecution) state.EnvironmentQualificationSnapshot {
	capture := uuid.NewString()
	snapshot := state.Snapshot{StorageKey: state.SnapshotCaptureMemKey(frame.DeploymentID, state.SnapshotTierWarm, capture)}
	return state.EnvironmentQualificationSnapshot{CaptureID: capture, NativeGeneration: uuid.NewString(), KernelBootID: uuid.NewString(),
		StorageKey: snapshot.StorageKey, VMStateStorageKey: state.SnapshotVMStateKey(snapshot), DriveStorageKey: state.SnapshotDriveKey(snapshot),
		BackingStorageKey: state.SnapshotBackingKey(snapshot), MemBytes: 1024, VMStateBytes: 128, StoredBytes: 2048}
}

func TestEnvironmentGitOpsCaptureEvidenceIsFencedImmutableAndDoesNotActivate(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		claimed, _, runtime := qualificationRuntimeFixture(t, basic)
		executor := basic.(state.EnvironmentQualificationExecutionStore)
		status, err := executor.EnvironmentQualificationExecution(t.Context(), claimed.ReservedInstanceID)
		if err != nil {
			t.Fatal(err)
		}
		frame := status.Execution
		proof := captureProof(frame)
		store := basic.(state.EnvironmentQualificationSnapshotStore)
		if _, err := store.RecordEnvironmentQualificationSnapshot(t.Context(), claimed, frame, proof); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("cold boot counted as input acknowledgement: %v", err)
		}
		if _, err := basic.(state.EnvironmentGitOpsQualificationRuntimeStore).PublishEnvironmentWorkloadQualificationRuntime(t.Context(), claimed, runtime); err != nil {
			t.Fatal(err)
		}
		foreign := frame
		foreign.Artifact.RootfsKey = "foreign/rootfs"
		if _, err := store.RecordEnvironmentQualificationSnapshot(t.Context(), claimed, foreign, proof); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("changed frame accepted: %v", err)
		}
		bad := proof
		bad.StorageKey = "snap/foreign/warm/captures/" + proof.CaptureID + "/v2/mem"
		if _, err := store.RecordEnvironmentQualificationSnapshot(t.Context(), claimed, frame, bad); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("foreign capture accepted: %v", err)
		}
		receipt, err := store.RecordEnvironmentQualificationSnapshot(t.Context(), claimed, frame, proof)
		if err != nil || receipt.Execution != frame || receipt.Snapshot != proof || !receipt.Inputs.Boundary.Equal(runtime.Inputs.Boundary) || receipt.Inputs.Scope != runtime.Inputs.Scope || !receipt.Inputs.AllSecrets {
			t.Fatalf("capture: %+v %v", receipt, err)
		}
		repeated, err := store.RecordEnvironmentQualificationSnapshot(t.Context(), claimed, frame, proof)
		if err != nil || !reflect.DeepEqual(receipt, repeated) {
			t.Fatalf("capture replay changed evidence: %+v %v", repeated, err)
		}
		bad = captureProof(frame)
		if _, err := store.RecordEnvironmentQualificationSnapshot(t.Context(), claimed, frame, bad); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("capture evidence replaced: %v", err)
		}
		if err := basic.UpdateDeploymentStatus(t.Context(), frame.DeploymentID, state.DeployLive, ""); err == nil {
			t.Fatal("capture granted activation")
		}
		dep, err := basic.DeploymentByID(t.Context(), frame.DeploymentID)
		if err != nil || !dep.EnvironmentWorkloadHeld() || dep.Status != state.DeploySnapshotting {
			t.Fatalf("capture escaped hold: %+v %v", dep, err)
		}
		retirement := state.EnvironmentQualificationRetirement{Kind: state.QualificationNativeRetired, ReceiptID: uuid.NewString(), NativeGeneration: proof.NativeGeneration, KernelBootID: proof.KernelBootID, ProcessesExited: true, ResourcesRemoved: true}
		foreignRetirement := retirement
		foreignRetirement.NativeGeneration = uuid.NewString()
		if err := executor.RetireEnvironmentQualificationExecution(t.Context(), frame, foreignRetirement); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("foreign physical generation retired captured VM: %v", err)
		}
		if err := executor.RetireEnvironmentQualificationExecution(t.Context(), frame, retirement); err != nil {
			t.Fatal(err)
		}
		if _, err := store.RecordEnvironmentQualificationSnapshot(t.Context(), claimed, frame, proof); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("retired execution republished evidence: %v", err)
		}
		historical, err := store.EnvironmentQualificationSnapshotReceipt(t.Context(), frame.InstanceID)
		if err != nil || !reflect.DeepEqual(historical, receipt) {
			t.Fatalf("retirement erased history: %+v %v", historical, err)
		}
	})
}

func TestEnvironmentGitOpsScopedBindingIdentityAndWriteGuard(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		source, desired, lease := newWorkloadFixture(t, basic, "enforce", "pro")
		intent := basic.(state.EnvironmentGitOpsIntentStore)
		plan := planForStore(t, intent, lease, desired)
		if _, err := basic.(state.EnvironmentGitOpsWorkloadCreationStore).PrepareEnvironmentGitOpsWorkloads(t.Context(), lease, plan); err != nil {
			t.Fatal(err)
		}
		observation, err := intent.ObserveEnvironmentGitOps(t.Context(), lease, desired)
		if err != nil {
			t.Fatal(err)
		}
		apiID, functionID := observation.State.ResourceIDs["workload/api"], observation.State.ResourceIDs["workload/function"]
		workloads := basic.(state.EnvironmentWorkloadIntentStore)
		row, err := workloads.EnvironmentWorkloadIntent(t.Context(), source.AccountID, apiID, source.EnvironmentID)
		if err != nil || row.ServiceBindings["function"].TargetAppID != functionID || observation.State.ResourceIDs["workload/api/service_bindings/function"] != functionID {
			t.Fatalf("binding lost scoped identity: %+v %v", row, err)
		}
		changed := row.ServiceBindings["function"]
		if count, err := basic.CountAppEnv(t.Context(), source.AccountID, apiID); err != nil || count != 1 {
			t.Fatalf("generated binding key did not consume quota: %d %v", count, err)
		}
		if count, err := basic.CountAppEnvInScope(t.Context(), source.AccountID, apiID, "staging"); err != nil || count != 0 {
			t.Fatalf("generated key leaked to neighboring environment: %d %v", count, err)
		}
		if err := basic.UpsertAppEnvInScope(t.Context(), source.AccountID, apiID, "production", "OCCUPIED_URL", "ordinary"); err != nil {
			t.Fatal(err)
		}
		row.ServiceBindings["secondary"] = state.EnvironmentScopedServiceBinding{Workload: "function", EnvKey: "OCCUPIED_URL", TargetAppID: functionID}
		if _, err := workloads.PutEnvironmentWorkloadIntent(t.Context(), row); err == nil {
			t.Fatal("scoped binding occupied an existing variable key")
		}
		delete(row.ServiceBindings, "secondary")
		if err := basic.UpsertAppEnvInScope(t.Context(), source.AccountID, apiID, "production", "FUNCTION_URL", "http://foreign.internal"); !errors.Is(err, state.ErrEnvironmentGitManaged) {
			t.Fatalf("binding environment key overwritten: %v", err)
		}
		changed.EnvKey = "OTHER_URL"
		row.ServiceBindings["function"] = changed
		if _, err := workloads.PutEnvironmentWorkloadIntent(t.Context(), row); !errors.Is(err, state.ErrEnvironmentGitManaged) {
			t.Fatalf("owned binding changed through ordinary intent: %v", err)
		}
		changed.TargetAppID = apiID
		row.ServiceBindings["function"] = changed
		if _, err := workloads.PutEnvironmentWorkloadIntent(t.Context(), row); err == nil {
			t.Fatal("binding target rebound to caller")
		}
		preserved, err := workloads.EnvironmentWorkloadIntent(t.Context(), source.AccountID, apiID, source.EnvironmentID)
		if err != nil || preserved.ServiceBindings["function"].EnvKey != "FUNCTION_URL" || preserved.ServiceBindings["function"].TargetAppID != functionID {
			t.Fatalf("rejected write mutated binding: %+v %v", preserved, err)
		}
	})
}
