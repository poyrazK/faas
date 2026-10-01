package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ErrAccountWorkerCapacity means no additional resident worker may be created.
// A refusal publishes neither an instance nor a node reservation.
var ErrAccountWorkerCapacity = errors.New("state: account worker replica capacity exhausted")

func reserveAccountWorker(ctx context.Context, tx pgx.Tx, appID, deploymentID string) error {
	appUUID, err := uuid.Parse(appID)
	if err != nil {
		return ErrInvalidArgument
	}
	depUUID, err := uuid.Parse(deploymentID)
	if err != nil {
		return ErrInvalidArgument
	}
	q := sqlc.New()
	account, err := q.WorkerAdmissionLockAccount(ctx, tx, sqlc.WorkerAdmissionLockAccountParams{
		AppID: pgtypeFromUUID(appUUID), DeploymentID: pgtypeFromUUID(depUUID),
	})
	if err != nil {
		return mapErr(err)
	}
	// Read after acquiring the account lock in a separate statement. A waiter
	// must observe the instance committed by its predecessor, not the snapshot
	// taken before waiting for that predecessor's lock.
	used, err := q.WorkerAdmissionCount(ctx, tx, account.ID)
	if err != nil {
		return err
	}
	limits, ok := api.LimitsFor(api.Plan(account.Plan))
	if !ok || used >= int64(limits.WorkerReplicasMax) {
		return fmt.Errorf("%w: account=%s used=%d limit=%d", ErrAccountWorkerCapacity,
			uuid.UUID(account.ID.Bytes).String(), used, limits.WorkerReplicasMax)
	}
	return nil
}

func (m *MemStore) checkAccountWorkerReservationLocked(appID, deploymentID string) error {
	app, ok := m.apps[appID]
	if !ok {
		return ErrNotFound
	}
	dep, ok := m.deployments[deploymentID]
	if !ok || dep.AppID != appID {
		return ErrNotFound
	}
	account, ok := m.accounts[app.AccountID]
	if !ok {
		return ErrNotFound
	}
	used := 0
	for _, ins := range m.instances {
		if ins.Mode == string(InstanceModeWorker) && State(ins.State).CountsForRAM() &&
			m.apps[ins.AppID].AccountID == account.ID {
			used++
		}
	}
	limits, ok := api.LimitsFor(account.Plan)
	if !ok || used >= limits.WorkerReplicasMax {
		return fmt.Errorf("%w: account=%s used=%d limit=%d", ErrAccountWorkerCapacity,
			account.ID, used, limits.WorkerReplicasMax)
	}
	return nil
}
