package sched

import (
	"context"
	"errors"
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
	return e.recoverEnvironmentQualificationStatus(ctx, store, status)
}

type EnvironmentQualificationRecoveryPage struct {
	Examined   int
	Retired    int
	Skipped    int
	NextCursor string
}

// Recover one bounded page on the recorded host. A stale discovery result must
// pass the store's locked lease check before any native RPC. Failure advances
// the cursor so one unavailable attempt cannot starve its neighbors; a later
// pass retries the still-held frame. This is not a qualification/boot consumer.
func (e *Engine) RecoverEnvironmentQualificationExecutions(ctx context.Context, nodeID, afterInstanceID string, limit int) (page EnvironmentQualificationRecoveryPage, result error) {
	store, executionOK := e.store.(state.EnvironmentQualificationExecutionStore)
	discovery, recoveryOK := e.store.(state.EnvironmentQualificationRecoveryStore)
	if !executionOK || !recoveryOK {
		return page, state.ErrInvalidArgument
	}
	ctx, cancel := context.WithTimeout(ctx, 2*DestroyTimeout)
	defer cancel()
	rows, err := discovery.ListEnvironmentQualificationExecutionsForRecovery(ctx, nodeID, afterInstanceID, limit)
	if err != nil {
		return page, err
	}
	page.NextCursor = afterInstanceID
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return page, errors.Join(result, err)
		}
		id := row.Execution.InstanceID
		page.Examined++
		page.NextCursor = id
		status, err := discovery.EnvironmentQualificationExecutionForRecovery(ctx, nodeID, id)
		if errors.Is(err, state.ErrConflict) || errors.Is(err, state.ErrNotFound) {
			page.Skipped++
			continue
		}
		if err == nil {
			err = e.recoverEnvironmentQualificationStatus(ctx, store, status)
		}
		if err != nil {
			result = errors.Join(result, fmt.Errorf("qualification recovery instance %s: %w", id, err))
			continue
		}
		page.Retired++
	}
	if len(rows) < limit {
		page.NextCursor = ""
	}
	return page, result
}

func (e *Engine) recoverEnvironmentQualificationStatus(ctx context.Context, store state.EnvironmentQualificationExecutionStore, status state.EnvironmentQualificationExecutionStatus) error {
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
