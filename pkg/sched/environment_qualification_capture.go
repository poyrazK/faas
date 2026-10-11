package sched

import (
	"context"
	"errors"
	"fmt"

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
		return zero, fmt.Errorf("capture capabilities or reserved runtime do not match: %w", state.ErrConflict)
	}
	ctx, cancel := context.WithDeadline(WithScope(ctx, claimed.FrozenInputs.Scope), *claimed.LeaseUntil)
	defer cancel()
	if err := qualifier.ValidateEnvironmentWorkloadQualification(ctx, claimed); err != nil {
		return zero, fmt.Errorf("revalidate qualification claim: %w", err)
	}
	status, err := executor.EnvironmentQualificationExecution(ctx, ins.ID)
	if err != nil {
		return zero, fmt.Errorf("read original execution frame: %w", err)
	}
	frame := status.Execution
	if !status.DispatchStarted || status.RetiredAt != nil || frame.RequestID != claimed.ID || frame.Attempt != claimed.Attempt ||
		frame.NodeID != ins.NodeID || frame.WakeID != ins.WakeID || frame.Artifact != claimed.Artifact {
		return zero, fmt.Errorf("original execution frame is not live for capture: %w", state.ErrConflict)
	}
	prior, err := store.EnvironmentQualificationSnapshotReceipt(ctx, ins.ID)
	if err == nil {
		receipt, err := store.RecordEnvironmentQualificationSnapshot(ctx, claimed, frame, prior.Snapshot)
		if err != nil {
			return zero, fmt.Errorf("replay committed capture receipt: %w", err)
		}
		return receipt, nil
	}
	if !errors.Is(err, state.ErrNotFound) {
		return zero, fmt.Errorf("read prior capture receipt: %w", err)
	}
	evidence, err := vm.CaptureEnvironmentQualification(ctx, frame)
	if err != nil {
		return zero, fmt.Errorf("native capture failed: %w", err)
	}
	if evidence.Execution != frame {
		return zero, fmt.Errorf("native capture substituted execution frame: %w", state.ErrConflict)
	}
	receipt, err := store.RecordEnvironmentQualificationSnapshot(ctx, claimed, frame, evidence.Snapshot)
	if err != nil {
		return zero, fmt.Errorf("persist native capture receipt: %w", err)
	}
	return receipt, nil
}

// CaptureEnvironmentWorkloadQualificationGraph stores one capture receipt per
// live graph member after the graph visitor succeeds and before any member is
// retired. Partial cohorts remain incomplete in graph evidence.
func (e *Engine) CaptureEnvironmentWorkloadQualificationGraph(ctx context.Context, claimed []state.EnvironmentWorkloadQualificationRequest, instances map[string]state.Instance) error {
	ordered, err := qualificationGraphExecutionOrder(claimed)
	if err != nil {
		return fmt.Errorf("order capture cohort: %w", err)
	}
	if len(instances) != len(ordered) {
		return fmt.Errorf("capture instance cohort size mismatch: %w", state.ErrConflict)
	}
	for _, request := range ordered {
		ins, exists := instances[request.Resource]
		if !exists || ins.ID != request.ReservedInstanceID || ins.DeploymentID != request.DeploymentID {
			return fmt.Errorf("capture runtime for %s differs from its reservation: %w", request.Resource, state.ErrConflict)
		}
		if _, err := e.CaptureEnvironmentWorkloadQualification(ctx, request, ins); err != nil {
			return fmt.Errorf("capture qualification workload %s: %w", request.Resource, err)
		}
	}
	return nil
}
