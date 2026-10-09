package sched

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type qualificationRestoreRuntimeVMM struct {
	*qualificationRuntimeVMM
	boots            int
	restores         int
	coldBootFallback bool
	restoreDelay     time.Duration
	proofs           map[string]state.EnvironmentQualificationRetirement
	createSpecs      map[string]AppSpec
	restoreSpecs     map[string]AppSpec
	beforeRestore    func(state.EnvironmentQualificationExecution) error
	retiredArtifacts []string
}

func (v *qualificationRestoreRuntimeVMM) RetireEnvironmentQualificationArtifacts(_ context.Context,
	capture, restored state.EnvironmentQualificationExecution, smoke state.EnvironmentQualificationSmokeReceipt, captureID string) error {
	if capture.CaptureInstanceID != "" || restored.CaptureInstanceID != capture.InstanceID ||
		smoke.CaptureInstanceID != capture.InstanceID || smoke.InstanceID != restored.InstanceID || captureID == "" {
		return state.ErrConflict
	}
	v.retiredArtifacts = append(v.retiredArtifacts, captureID)
	return nil
}

func (v *qualificationRestoreRuntimeVMM) CreateEnvironmentQualification(ctx context.Context, frame state.EnvironmentQualificationExecution, spec AppSpec) (*WakeOutcome, error) {
	out, err := v.qualificationRuntimeVMM.CreateEnvironmentQualification(ctx, frame, spec)
	if err == nil && out != nil {
		if v.createSpecs == nil {
			v.createSpecs = make(map[string]AppSpec)
		}
		v.createSpecs[frame.Resource] = spec
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

func (v *qualificationRestoreRuntimeVMM) RestoreEnvironmentQualification(ctx context.Context, frame state.EnvironmentQualificationExecution, spec AppSpec) (*WakeOutcome, error) {
	if v.beforeRestore != nil {
		if err := v.beforeRestore(frame); err != nil {
			return nil, err
		}
	}
	if v.restoreDelay > 0 {
		timer := time.NewTimer(v.restoreDelay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if frame.CaptureInstanceID == "" || frame.CaptureInstanceID == frame.InstanceID {
		return nil, state.ErrConflict
	}
	if v.restoreSpecs == nil {
		v.restoreSpecs = make(map[string]AppSpec)
	}
	v.restoreSpecs[frame.Resource] = spec
	v.restores++
	if v.proofs == nil {
		v.proofs = make(map[string]state.EnvironmentQualificationRetirement)
	}
	proof := v.proof
	proof.ReceiptID, proof.NativeGeneration = uuid.NewString(), uuid.NewString()
	v.proofs[frame.InstanceID] = proof
	method := vmmdpb.WakeMethod_WAKE_RESTORE
	if v.coldBootFallback {
		method = vmmdpb.WakeMethod_WAKE_COLD_BOOT
	}
	return &WakeOutcome{Instance: frame.InstanceID, LeaseUID: 20001, HostIP: fmt.Sprintf("10.100.0.%d", 20+v.restores),
		Netns: "qualification-restore-" + frame.InstanceID, Method: method, RequestedMethod: vmmdpb.WakeMethod_WAKE_RESTORE}, nil
}

func (v *qualificationRestoreRuntimeVMM) CaptureEnvironmentQualification(_ context.Context, frame state.EnvironmentQualificationExecution) (EnvironmentQualificationSnapshotEvidence, error) {
	proof, exists := v.proofs[frame.InstanceID]
	if !exists {
		return EnvironmentQualificationSnapshotEvidence{}, state.ErrConflict
	}
	captureID := proof.ReceiptID
	snapshot := state.Snapshot{StorageKey: state.SnapshotCaptureMemKey(frame.DeploymentID, state.SnapshotTierWarm, captureID)}
	return EnvironmentQualificationSnapshotEvidence{Execution: frame, Snapshot: state.EnvironmentQualificationSnapshot{
		CaptureID: captureID, NativeGeneration: proof.NativeGeneration, KernelBootID: proof.KernelBootID, FCVersion: "test-fc",
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
	if _, err := store.RecordEnvironmentQualificationConfigReceipt(t.Context(), request, admission.Execution, strings.Repeat("a", 64)); err != nil {
		t.Fatal("restored guest config acknowledgement:", err)
	}
	return admission, instance
}

func TestEnvironmentQualificationRestoreRuntimeUsesTargetReceipt(t *testing.T) {
	store, _, request := qualificationExecutionFixture(t, api.ExecutionModeRequest, time.Minute)
	vmm := &qualificationRestoreRuntimeVMM{qualificationRuntimeVMM: newQualificationRuntimeVMM(&fakeVMM{})}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "test-fc")
	if err := engine.WithEnvironmentWorkloadQualificationRuntime(t.Context(), request, func(ctx context.Context, ins state.Instance) error {
		_, err := engine.CaptureEnvironmentWorkloadQualification(ctx, request, ins)
		if err != nil {
			return err
		}
		status, err := store.EnvironmentQualificationExecution(ctx, ins.ID)
		if err != nil {
			return err
		}
		_, err = store.RecordEnvironmentQualificationConfigReceipt(ctx, request, status.Execution, strings.Repeat("a", 64))
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

func TestEnvironmentQualificationRestoreRuntimeOwnsDistinctTargetAndRetiresIt(t *testing.T) {
	store, _, request := qualificationExecutionFixture(t, api.ExecutionModeRequest, time.Minute)
	vmm := &qualificationRestoreRuntimeVMM{qualificationRuntimeVMM: newQualificationRuntimeVMM(&fakeVMM{}), restoreDelay: api.EnvironmentGitOpsQualificationRuntimeCheckInterval + 100*time.Millisecond}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "test-fc")
	var captured state.Instance
	if err := engine.WithEnvironmentWorkloadQualificationRuntime(t.Context(), request, func(ctx context.Context, ins state.Instance) error {
		captured = ins
		_, err := engine.CaptureEnvironmentWorkloadQualification(ctx, request, ins)
		if err != nil {
			return err
		}
		status, err := store.EnvironmentQualificationExecution(ctx, ins.ID)
		if err != nil {
			return err
		}
		_, err = store.RecordEnvironmentQualificationConfigReceipt(ctx, request, status.Execution, strings.Repeat("a", 64))
		return err
	}); err != nil {
		t.Fatal("capture fixture:", err)
	}
	var restored state.Instance
	err := engine.WithEnvironmentWorkloadQualificationRestoreRuntime(t.Context(), request, func(_ context.Context, ins state.Instance) error {
		restored = ins
		if ins.ID == captured.ID || ins.NodeID != captured.NodeID || ins.WakeID == captured.WakeID || ins.State != string(state.StateRunning) {
			return fmt.Errorf("restore did not publish a distinct target on the captured node: %+v", ins)
		}
		return nil
	})
	if err != nil {
		t.Fatal("restore runtime:", err)
	}
	if vmm.restores != 1 || vmm.lastRetired.InstanceID != restored.ID {
		t.Fatalf("restore/retirement did not target the distinct runtime: restores=%d retired=%+v target=%s", vmm.restores, vmm.lastRetired, restored.ID)
	}
	status, err := store.EnvironmentQualificationExecution(t.Context(), restored.ID)
	if err != nil || status.Execution.CaptureInstanceID != captured.ID || !status.DispatchStarted || status.RetiredAt == nil || status.Retirement == nil || status.Retirement.Kind != state.QualificationNativeRetired {
		t.Fatalf("restored execution did not retain its own dispatch and retirement evidence: %+v %v", status, err)
	}
}

func TestEnvironmentQualificationRestoreRejectsColdBootFallbackAndRetiresTarget(t *testing.T) {
	store, _, request := qualificationExecutionFixture(t, api.ExecutionModeRequest, time.Minute)
	vmm := &qualificationRestoreRuntimeVMM{qualificationRuntimeVMM: newQualificationRuntimeVMM(&fakeVMM{}), coldBootFallback: true}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "test-fc")
	if err := engine.WithEnvironmentWorkloadQualificationRuntime(t.Context(), request, func(ctx context.Context, ins state.Instance) error {
		_, err := engine.CaptureEnvironmentWorkloadQualification(ctx, request, ins)
		return err
	}); err != nil {
		t.Fatal("capture fixture:", err)
	}
	if err := engine.WithEnvironmentWorkloadQualificationRestoreRuntime(t.Context(), request, func(context.Context, state.Instance) error {
		t.Fatal("cold-boot fallback reached the restore visitor")
		return nil
	}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("cold-boot fallback was accepted: %v", err)
	}
	if vmm.restores != 1 || vmm.lastRetired.InstanceID == request.ReservedInstanceID {
		t.Fatalf("failed target was not independently retired: restores=%d retired=%+v", vmm.restores, vmm.lastRetired)
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

func TestEnvironmentQualificationGraphRestoreKeepsServiceBindingScopedToNewTarget(t *testing.T) {
	store, requests := claimedQualificationGraphFixture(t)
	vmm := &qualificationRestoreRuntimeVMM{qualificationRuntimeVMM: newQualificationRuntimeVMM(&fakeVMM{})}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "test-fc").WithEnvironmentQualificationServiceProxy(func(context.Context, string) (string, error) {
		return "http://10.100.0.1:10081", nil
	})
	captured := make(map[string]state.Instance, len(requests))
	err := engine.WithEnvironmentQualificationGraphRuntimes(t.Context(), requests, func(ctx context.Context, instances map[string]state.Instance) error {
		for resource, instance := range instances {
			captured[resource] = instance
		}
		return engine.CaptureEnvironmentWorkloadQualificationGraph(ctx, requests, instances)
	})
	if err != nil {
		t.Fatal("capture source graph:", err)
	}
	var restored map[string]state.Instance
	err = engine.WithEnvironmentQualificationGraphRestoreRuntimes(t.Context(), requests, func(ctx context.Context, instances map[string]state.Instance) error {
		restored = instances
		caller := instances["workload/api"]
		route, err := store.ResolveEnvironmentQualificationService(ctx, state.EnvironmentQualificationServiceRequest{
			NodeID: caller.NodeID, HostIP: caller.HostIP, GraphID: requests[0].GraphID, Binding: "backend",
		})
		if err != nil {
			return err
		}
		if route.Caller.InstanceID != caller.ID || route.Target.InstanceID != instances["workload/api2"].ID || route.Port != 8087 {
			return fmt.Errorf("restored binding route used the captured runtime or a different target: %+v", route)
		}
		return nil
	})
	if err != nil {
		t.Fatal("restore and smoke graph:", err)
	}
	if len(restored) != len(requests) || vmm.restores != len(requests) {
		t.Fatalf("restored graph cohort is incomplete: targets=%d restores=%d want=%d", len(restored), vmm.restores, len(requests))
	}
	for resource, target := range restored {
		if target.ID == captured[resource].ID || target.NodeID != captured[resource].NodeID || target.WakeID == captured[resource].WakeID {
			t.Fatalf("restore reused capture identity for %s: capture=%+v target=%+v", resource, captured[resource], target)
		}
	}
}
