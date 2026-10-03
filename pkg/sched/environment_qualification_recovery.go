package sched

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/state"
)

// Recovery consumes the original persisted placement even after approval,
// request or app ownership changes. It grants no replay or serving authority.
func (e *Engine) RecoverEnvironmentQualificationExecution(ctx context.Context, instanceID string) error {
	store, ok := e.store.(state.EnvironmentQualificationExecutionStore)
	if !ok {
		return state.ErrInvalidArgument
	}
	ctx, cancel := context.WithTimeout(ctx, 2*DestroyTimeout)
	defer cancel()
	status, err := store.EnvironmentQualificationExecution(ctx, instanceID)
	if err != nil {
		return err
	}
	frame := status.Execution
	if status.RetiredAt == nil {
		proof := state.EnvironmentQualificationRetirement{Kind: state.QualificationNeverDispatched}
		if status.DispatchStarted {
			vm, ok := e.vmm.(EnvironmentQualificationVMM)
			if !ok {
				return fmt.Errorf("qualification recovery requires attempt-aware native retirement: %w", state.ErrConflict)
			}
			evidence, err := vm.RetireEnvironmentQualification(ctx, frame)
			if err != nil {
				return err
			}
			if evidence.Execution != frame {
				return state.ErrConflict
			}
			proof = evidence.Retirement
		}
		release, err := e.lockQualificationApp(ctx, frame.AppID)
		if err != nil {
			return err
		}
		defer release()
		if err := store.RetireEnvironmentQualificationExecution(ctx, frame, proof); err != nil {
			return err
		}
	}
	e.releaseHostPortLeases(ctx, frame.NodeID, frame.InstanceID)
	e.ledger.Release(frame.InstanceID)
	return nil
}
