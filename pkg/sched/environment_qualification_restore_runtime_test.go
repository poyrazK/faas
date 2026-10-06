package sched

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type qualificationRestoreRuntimeVMM struct {
	*qualificationRuntimeVMM
	boots  int
	proofs map[string]state.EnvironmentQualificationRetirement
}

func (v *qualificationRestoreRuntimeVMM) CreateEnvironmentQualification(ctx context.Context, frame state.EnvironmentQualificationExecution, spec AppSpec) (*WakeOutcome, error) {
	out, err := v.qualificationRuntimeVMM.CreateEnvironmentQualification(ctx, frame, spec)
	if err == nil && out != nil {
		if v.proofs == nil {
			v.proofs = make(map[string]state.EnvironmentQualificationRetirement)
		}
		proof := v.proof
		proof.ReceiptID, proof.NativeGeneration = uuid.NewString(), uuid.NewString()
		v.proofs[frame.InstanceID] = proof
		v.boots++
		out.HostIP = fmt.Sprintf("10.100.0.%d", 10+v.boots)
		out.Netns = "qualification-" + frame.InstanceID
	}
	return out, err
}

func (v *qualificationRestoreRuntimeVMM) CaptureEnvironmentQualification(_ context.Context, frame state.EnvironmentQualificationExecution) (EnvironmentQualificationSnapshotEvidence, error) {
	captureID := uuid.NewString()
	proof, exists := v.proofs[frame.InstanceID]
	if !exists {
		return EnvironmentQualificationSnapshotEvidence{}, state.ErrConflict
	}
	snapshot := state.Snapshot{StorageKey: state.SnapshotCaptureMemKey(frame.DeploymentID, state.SnapshotTierWarm, captureID)}
	return EnvironmentQualificationSnapshotEvidence{Execution: frame, Snapshot: state.EnvironmentQualificationSnapshot{
		CaptureID: captureID, NativeGeneration: proof.NativeGeneration, KernelBootID: proof.KernelBootID,
		StorageKey: snapshot.StorageKey, VMStateStorageKey: state.SnapshotVMStateKey(snapshot), DriveStorageKey: state.SnapshotDriveKey(snapshot),
		BackingStorageKey: state.SnapshotBackingKey(snapshot), MemBytes: 1024, VMStateBytes: 128, StoredBytes: 2048,
	}}, nil
}

func (v *qualificationRestoreRuntimeVMM) RetireEnvironmentQualification(ctx context.Context, frame state.EnvironmentQualificationExecution) (EnvironmentQualificationRetirementEvidence, error) {
	evidence, err := v.qualificationRuntimeVMM.RetireEnvironmentQualification(ctx, frame)
	if err != nil {
		return evidence, err
	}
	proof, exists := v.proofs[frame.InstanceID]
	if !exists {
		return EnvironmentQualificationRetirementEvidence{}, state.ErrConflict
	}
	evidence.Retirement = proof
	return evidence, nil
}

func publishQualificationRestoreRuntime(t *testing.T, store *state.MemStore, request state.EnvironmentWorkloadQualificationRequest, hostIP string) (state.EnvironmentQualificationRestoreAdmission, state.Instance) {
	t.Helper()
	capture, err := store.InstanceByID(t.Context(), request.ReservedInstanceID)
	if err != nil {
		t.Fatal(err)
	}
	admission, err := store.CreateEnvironmentQualificationRestore(t.Context(), request, state.EnvironmentWorkloadQualificationPlacement{
		NodeID: capture.NodeID, WakeID: uuid.NewString(), RAMMB: capture.RAMMB,
	})
	if err != nil || !admission.Created || admission.Execution.CaptureInstanceID != capture.ID {
		t.Fatalf("restore admission: %+v %v", admission, err)
	}
	inputs, exists, err := store.InstanceRuntimeConfigReceipt(t.Context(), capture.ID)
	if err != nil || !exists {
		t.Fatalf("capture runtime receipt: %t %v", exists, err)
	}
	runtime := state.EnvironmentWorkloadQualificationRuntime{NodeID: admission.Instance.NodeID, WakeID: admission.Instance.WakeID,
		Netns: "restore-" + admission.Instance.ID, HostIP: hostIP, GuestUID: 20001, Inputs: inputs}
	if _, err := store.PublishEnvironmentQualificationRestoreRuntime(t.Context(), request, admission.Execution, runtime); err == nil {
		t.Fatal("restore runtime published before durable dispatch")
	}
	if err := store.MarkEnvironmentQualificationRestoreDispatched(t.Context(), request, admission.Execution); err != nil {
		t.Fatal(err)
	}
	instance, err := store.PublishEnvironmentQualificationRestoreRuntime(t.Context(), request, admission.Execution, runtime)
	if err != nil || instance.State != string(state.StateRunning) || instance.HostIP != hostIP || instance.Netns != runtime.Netns {
		t.Fatalf("restore runtime publication: %+v %v", instance, err)
	}
	retry, err := store.PublishEnvironmentQualificationRestoreRuntime(t.Context(), request, admission.Execution, runtime)
	if err != nil || retry != instance {
		t.Fatalf("identical receipt retry: %+v %v", retry, err)
	}
	forged := runtime
	forged.HostIP = "10.100.1.99"
	if _, err := store.PublishEnvironmentQualificationRestoreRuntime(t.Context(), request, admission.Execution, forged); err == nil {
		t.Fatal("restore runtime receipt was replaced")
	}
	return admission, instance
}

func TestEnvironmentQualificationRestoreRuntimeUsesTargetReceipt(t *testing.T) {
	store, _, request := qualificationExecutionFixture(t, api.ExecutionModeRequest, time.Minute)
	vmm := &qualificationRestoreRuntimeVMM{qualificationRuntimeVMM: newQualificationRuntimeVMM(&fakeVMM{})}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "test-fc")
	if err := engine.WithEnvironmentWorkloadQualificationRuntime(t.Context(), request, func(ctx context.Context, ins state.Instance) error {
		_, err := engine.CaptureEnvironmentWorkloadQualification(ctx, request, ins)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	admission, target := publishQualificationRestoreRuntime(t, store, request, "10.100.0.20")
	status, err := store.EnvironmentQualificationExecution(t.Context(), target.ID)
	if err != nil || status.Execution != admission.Execution || status.CaptureInstanceID != request.ReservedInstanceID || !status.DispatchStarted {
		t.Fatalf("restore runtime lost target execution identity: %+v %v", status, err)
	}
}

func TestEnvironmentQualificationServiceRoutesToRestoredTargetRuntime(t *testing.T) {
	store, requests := claimedQualificationGraphFixture(t)
	vmm := &qualificationRestoreRuntimeVMM{qualificationRuntimeVMM: newQualificationRuntimeVMM(&fakeVMM{})}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "test-fc").WithEnvironmentQualificationServiceProxy(func(context.Context, string) (string, error) { return "http://10.100.0.1:10081", nil })
	err := engine.WithEnvironmentQualificationGraphRuntimes(t.Context(), requests, func(ctx context.Context, instances map[string]state.Instance) error {
		for _, request := range requests {
			if _, err := engine.CaptureEnvironmentWorkloadQualification(ctx, request, instances[request.Resource]); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	restored := make(map[string]state.Instance, len(requests))
	for i, request := range requests {
		_, restored[request.Resource] = publishQualificationRestoreRuntime(t, store, request, fmt.Sprintf("10.100.0.%d", 20+i))
	}
	caller := restored["workload/api"]
	route, err := store.ResolveEnvironmentQualificationService(t.Context(), state.EnvironmentQualificationServiceRequest{
		NodeID: caller.NodeID, HostIP: caller.HostIP, GraphID: requests[0].GraphID, Binding: "backend",
	})
	if err != nil || route.Caller.InstanceID != caller.ID || route.Target.InstanceID != restored["workload/api2"].ID || route.Port != 8087 {
		t.Fatalf("restored graph route: %+v %v", route, err)
	}
}
