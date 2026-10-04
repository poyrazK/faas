package state

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"time"
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
	inv := Invocation{
		ID: operationUUIDString(row.ID), AppID: operationUUIDString(row.AppID), AccountID: operationUUIDString(row.AccountID),
		OperationID: operationUUIDString(row.OperationID), PlatformTenantID: operationUUIDString(row.PlatformTenantID), InstanceID: row.InstanceID.String,
		Source: InvocationSource(row.Source), State: InvocationState(row.State), QueueName: row.QueueName,
		Method: row.Method, Path: row.Path, Payload: row.Payload, Headers: row.Headers, Result: row.Result, DueAt: row.DueAt.Time,
		AckURL: row.AckUrl.String, Attempts: int(row.Attempts), QuotaReserved: row.QuotaReserved, LastError: row.LastError.String, CreatedAt: row.CreatedAt.Time,
		WorkPolicyName: row.WorkPolicyName.String, WorkPolicyRevision: row.WorkPolicyRevision.Int64, WorkKeyDigest: row.WorkKeyDigest,
		WorkFairnessDigest: row.WorkFairnessDigest, WorkFairnessLimit: int(row.WorkFairnessLimit.Int32), WorkSequence: row.WorkSequence.Int64,
		RetryPolicyJSON: row.RetryPolicy, OnSuccessDestinationID: operationUUIDString(row.OnSuccessDestinationID), OnFailureDestinationID: operationUUIDString(row.OnFailureDestinationID),
		ScheduledAt: operationTimestamp(row.ScheduledAt), LeaseExpiresAt: operationTimestamp(row.LeaseExpiresAt), ReceivedAt: operationTimestamp(row.ReceivedAt), CompletedAt: operationTimestamp(row.CompletedAt),
		WorkExpiresAt: operationTimestamp(row.WorkExpiresAt), DeadlineAt: operationTimestamp(row.DeadlineAt), ResultRetentionUntil: operationTimestamp(row.ResultRetentionUntil), LastReplayedAt: operationTimestamp(row.LastReplayedAt),
	}
	if row.CronID.Valid {
		id := operationUUIDString(row.CronID)
		inv.CronID = &id
	}
	if row.Outcome.Valid {
		outcome := InvocationOutcome(row.Outcome.String)
		inv.Outcome = &outcome
	}
	return inv, nil
}
func operationUUIDString(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	return uuid.UUID(id.Bytes).String()
}
func operationTimestamp(at pgtype.Timestamptz) *time.Time {
	if !at.Valid {
		return nil
	}
	value := at.Time
	return &value
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
