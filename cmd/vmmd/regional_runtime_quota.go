package main

// adr: 489
import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/runtimequotahost"
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
		if operation.RegionalRuntime != nil || (!operation.IsBuilder && !operation.ExecutionOnly) {
			if err := checkRegionalRuntimeOperation(ctx, reader, operation); err != nil {
				return err
			}
		}
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

func checkRegionalRuntimeOperation(ctx context.Context, reader state.Store, operation fcvm.RuntimeAdmission) error {
	head, err := reader.RegionalRuntimeQuotaHead(ctx, operation.AppID)
	if errors.Is(err, state.ErrNotFound) {
		if operation.RegionalRuntime != nil {
			return runtimequota.ErrTrust
		}
		return nil
	}
	if err != nil {
		return err
	}
	if operation.RegionalRuntime == nil {
		return runtimequota.ErrTrust
	}
	if operation.IsBuilder || operation.ExecutionOnly || head.Grant != operation.RegionalRuntime.Permit.Grant || operation.Plan != head.Grant.Policy.Plan {
		return runtimequota.ErrBinding
	}
	return reader.CheckRegionalRuntimeIssuedPlacement(ctx, *operation.RegionalRuntime)
}

func openRegionalRuntimeHostJournal(dir, nodeID string, readBootID func() ([]byte, error)) (*runtimequotahost.Journal, error) {
	if dir == "" {
		return nil, nil
	}
	if nodeID == "" {
		return nil, fmt.Errorf("vmmd: regional host journal requires registered node identity")
	}
	if readBootID == nil {
		readBootID = func() ([]byte, error) { return os.ReadFile("/proc/sys/kernel/random/boot_id") }
	}
	bootID, err := readBootID()
	if err != nil {
		return nil, fmt.Errorf("vmmd: regional host kernel boot identity: %w", err)
	}
	j, err := runtimequotahost.Open(dir, nodeID, strings.TrimSpace(string(bootID)))
	if err != nil {
		return nil, fmt.Errorf("vmmd: regional host journal: %w", err)
	}
	return j, nil
}
