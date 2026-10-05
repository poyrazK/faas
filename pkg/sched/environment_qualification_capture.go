package sched

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/state"
)

// CaptureEnvironmentWorkloadQualification retains vmmd's original capture
// evidence while the execution lease is current. It never creates a serving
// snapshot or emits deployment_ready. Smoke and restore proof remain separate.
func (e *Engine) CaptureEnvironmentWorkloadQualification(ctx context.Context, claimed state.EnvironmentWorkloadQualificationRequest, ins state.Instance) (state.EnvironmentQualificationSnapshotReceipt, error) {
	var zero state.EnvironmentQualificationSnapshotReceipt
	store, ok := e.store.(state.EnvironmentQualificationSnapshotStore)
	executor, executionOK := e.store.(state.EnvironmentQualificationExecutionStore)
	qualifier, qualificationOK := e.store.(state.EnvironmentGitOpsQualificationStore)
	vm, nativeOK := e.vmm.(EnvironmentQualificationSnapshotVMM)
	if !ok || !executionOK || !qualificationOK || !nativeOK || claimed.ReservedInstanceID != ins.ID || claimed.LeaseUntil == nil {
		return zero, state.ErrConflict
	}
	ctx, cancel := context.WithDeadline(WithScope(ctx, claimed.FrozenInputs.Scope), *claimed.LeaseUntil)
	defer cancel()
	if err := qualifier.ValidateEnvironmentWorkloadQualification(ctx, claimed); err != nil {
		return zero, err
	}
	status, err := executor.EnvironmentQualificationExecution(ctx, ins.ID)
	if err != nil {
		return zero, err
	}
	frame := status.Execution
	if !status.DispatchStarted || status.RetiredAt != nil || frame.RequestID != claimed.ID || frame.Attempt != claimed.Attempt ||
		frame.NodeID != ins.NodeID || frame.WakeID != ins.WakeID || frame.Artifact != claimed.Artifact {
		return zero, state.ErrConflict
	}
	prior, err := store.EnvironmentQualificationSnapshotReceipt(ctx, ins.ID)
	if err == nil {
		return store.RecordEnvironmentQualificationSnapshot(ctx, claimed, frame, prior.Snapshot)
	}
	if !errors.Is(err, state.ErrNotFound) {
		return zero, err
	}
	evidence, err := vm.CaptureEnvironmentQualification(ctx, frame)
	if err != nil {
		return zero, err
	}
	if evidence.Execution != frame {
		return zero, state.ErrConflict
	}
	return store.RecordEnvironmentQualificationSnapshot(ctx, claimed, frame, evidence.Snapshot)
}
