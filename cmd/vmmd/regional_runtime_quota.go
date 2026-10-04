package main

// adr: 489
import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
)

// Production wiring requires the journal reader whenever durable state is
// configured. Independent builder/execution claims remain owned by their RPCs.
func regionalRuntimeQuotaAuthorizer(store state.Store) (fcvm.RuntimeAuthorizer, error) {
	if store == nil {
		return nil, nil
	}
	reader := store
	return func(ctx context.Context, operation fcvm.RuntimeAdmission) error {
		if operation.IsBuilder || operation.ExecutionOnly {
			return nil
		}
		if operation.AppTaskOnly {
			task, err := store.AppTaskByID(ctx, operation.AccountID, operation.AppID, operation.Instance)
			if err != nil {
				return err
			}
			status := state.AppTaskRestoring
			if operation.Command {
				status = state.AppTaskRunning
			}
			if task.DeploymentID != operation.DeploymentID || task.Status != status || task.LeaseToken == nil || task.LeaseExpiresAt == nil || !task.LeaseExpiresAt.After(time.Now()) || task.CancelRequested != nil {
				return state.ErrAppTaskLeaseLost
			}
			return state.CheckRegionalRuntimeQuotaAdmission(ctx, reader, operation.AppID)
		}
		instance, err := store.InstanceByID(ctx, operation.Instance)
		if err != nil {
			return fmt.Errorf("vmmd: runtime admission instance: %w", err)
		}
		if instance.AppID != operation.AppID {
			return state.ErrConflict
		}
		return state.CheckRegionalRuntimeQuotaAdmission(ctx, reader, instance.AppID)
	}, nil
}
