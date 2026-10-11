package state

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func operationLockedInvocation(ctx context.Context, tx pgx.Tx, id string) (Invocation, error) {
	key, err := operationUUID(id)
	if err != nil {
		return Invocation{}, err
	}
	row, err := sqlc.New().LockCustomerOperationInvocation(ctx, tx, key)
	if err != nil {
		return Invocation{}, mapErr(err)
	}
	return invocationFromSQL(row)
}
func operationUUIDString(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	return uuid.UUID(id.Bytes).String()
}
func purgeOperationOwnerTx(ctx context.Context, tx pgx.Tx, accountID, appID string) error {
	var account, app pgtype.UUID
	var err error
	if accountID != "" {
		account, err = operationUUID(accountID)
		if err != nil {
			return err
		}
	}
	if appID != "" {
		app, err = operationUUID(appID)
		if err != nil {
			return err
		}
	}
	q := sqlc.New()
	tasks, err := q.LockOwnedActiveOperationJobTasksForPurge(ctx, tx, sqlc.LockOwnedActiveOperationJobTasksForPurgeParams{AccountID: account, AppID: app})
	if err != nil {
		return err
	}
	count, err := q.CountOwnedActiveOperationJobTasksForPurge(ctx, tx, sqlc.CountOwnedActiveOperationJobTasksForPurgeParams{AccountID: account, AppID: app})
	if err != nil {
		return err
	}
	if count != int64(len(tasks)) {
		return ErrConflict
	}
	for _, task := range tasks {
		if task.Status == "claimed" {
			return ErrConflict
		}
	}
	for _, task := range tasks {
		if err := q.CancelQueuedCustomerOperationJobTask(ctx, tx, task.RunID); err != nil {
			return err
		}
		if err := q.RecomputeCustomerOperationJobRun(ctx, tx, task.RunID); err != nil {
			return err
		}
	}
	if err := q.DeleteCustomerOperationsForOwner(ctx, tx, sqlc.DeleteCustomerOperationsForOwnerParams{AccountID: account, AppID: app}); err != nil {
		return err
	}
	if err := q.DeleteCustomerOperationDefinitionsForOwner(ctx, tx, sqlc.DeleteCustomerOperationDefinitionsForOwnerParams{AccountID: account, AppID: app}); err != nil {
		return err
	}
	if err := q.DeleteCustomerOperationReceiptsForOwner(ctx, tx, sqlc.DeleteCustomerOperationReceiptsForOwnerParams{AccountID: account, AppID: app}); err != nil {
		return err
	}
	return q.DeleteCustomerOperationExecutionsForOwner(ctx, tx, sqlc.DeleteCustomerOperationExecutionsForOwnerParams{AccountID: account, AppID: app})
}
