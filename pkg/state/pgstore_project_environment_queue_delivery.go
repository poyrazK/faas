package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ProjectEnvironmentQueueDeliveryStore = (*PgStore)(nil)

func readEnvironmentQueueDeliveryAccount(ctx context.Context, db sqlc.DBTX, id string) (Account, error) {
	row, err := sqlc.New().ReadEnvironmentQueueDeliveryAccount(ctx, db, mustPgUUID(id))
	if err != nil {
		return Account{}, mapErr(err)
	}
	account := Account{ID: id, Plan: api.Plan(row.Plan), Status: AccountStatus(row.Status)}
	if row.AbuseHoldAt.Valid {
		account.AbuseHoldAt = &row.AbuseHoldAt.Time
	}
	return account, nil
}

func (s *PgStore) ClaimNextProjectEnvironmentQueueDelivery(ctx context.Context, req ProjectEnvironmentQueueDeliveryRequest) (ProjectEnvironmentQueueDelivery, error) {
	if err := validateEnvironmentQueueDeliveryRequest(req); err != nil {
		return ProjectEnvironmentQueueDelivery{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectEnvironmentQueueDelivery{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	set, err := projectEnvironmentQueueConsumersDB(ctx, tx, req.AccountID, req.ProjectID, req.DeploymentID, false, true)
	if err != nil {
		return ProjectEnvironmentQueueDelivery{}, err
	}
	consumer, err := queueConsumerByName(set, req.BindingName)
	if err != nil {
		return ProjectEnvironmentQueueDelivery{}, err
	}
	if consumer.Mode != req.Mode {
		return ProjectEnvironmentQueueDelivery{}, ErrConflict
	}
	account, err := readEnvironmentQueueDeliveryAccount(ctx, tx, req.AccountID)
	if err != nil {
		return ProjectEnvironmentQueueDelivery{}, err
	}
	limits, err := environmentQueueDeliveryLimits(account, req.LeaseSeconds)
	if err != nil {
		return ProjectEnvironmentQueueDelivery{}, err
	}
	q := sqlc.New()
	row, err := q.NextEnvironmentQueueDeliveryInvocation(ctx, tx, sqlc.NextEnvironmentQueueDeliveryInvocationParams{
		ConsumerID: mustPgUUID(consumer.ID), RuntimeSetID: mustPgUUID(set.ID)})
	if err != nil {
		return ProjectEnvironmentQueueDelivery{}, mapErr(err)
	}
	id := pgUUIDString(row.ID)
	if row.QuotaReserved {
		return ProjectEnvironmentQueueDelivery{}, ErrInvocationEnvironmentWorkIsolation
	}
	if owned, err := validateInvocationQueueClaimDB(ctx, tx, id, true); err != nil || !owned {
		if err == nil {
			err = ErrInvocationEnvironmentWorkIsolation
		}
		return ProjectEnvironmentQueueDelivery{}, err
	}
	owner, err := readInvocationEnvironmentQueueAdmissionDB(ctx, tx, id)
	if err != nil {
		return ProjectEnvironmentQueueDelivery{}, err
	}
	inv, err := invocationFromSQLC(row)
	if err != nil {
		return ProjectEnvironmentQueueDelivery{}, err
	}
	if environmentQueueDeliveryAttemptsExhausted(inv, limits) {
		count, err := q.ExhaustEnvironmentQueueDeliveryInvocation(ctx, tx, sqlc.ExhaustEnvironmentQueueDeliveryInvocationParams{
			InvocationID: row.ID, ConsumerID: mustPgUUID(consumer.ID), RuntimeSetID: mustPgUUID(set.ID), Attempt: row.Attempts})
		if err != nil {
			return ProjectEnvironmentQueueDelivery{}, err
		}
		if count != 1 {
			return ProjectEnvironmentQueueDelivery{}, ErrNotFound
		}
		if err := tx.Commit(ctx); err != nil {
			return ProjectEnvironmentQueueDelivery{}, err
		}
		return ProjectEnvironmentQueueDelivery{}, ErrNotFound
	}
	if err := q.EnsureEnvironmentQueueDeliveryQuota(ctx, tx, sqlc.EnsureEnvironmentQueueDeliveryQuotaParams{
		AccountID: mustPgUUID(req.AccountID), MaxInflight: int32(limits.MaxAsyncInvocationsPerAccount)}); err != nil {
		return ProjectEnvironmentQueueDelivery{}, err
	}
	if _, err := q.ReserveEnvironmentQueueDeliveryQuota(ctx, tx, mustPgUUID(req.AccountID)); err != nil {
		if errors.Is(mapErr(err), ErrNotFound) {
			return ProjectEnvironmentQueueDelivery{}, ErrQuotaExceeded
		}
		return ProjectEnvironmentQueueDelivery{}, err
	}
	// The account reservation serializes consumer capacity across generations.
	if err := queueClaimCapacityDB(ctx, tx, id); err != nil {
		return ProjectEnvironmentQueueDelivery{}, err
	}
	row, err = q.ClaimEnvironmentQueueDeliveryInvocation(ctx, tx, sqlc.ClaimEnvironmentQueueDeliveryInvocationParams{
		InvocationID: row.ID, ConsumerID: mustPgUUID(consumer.ID), RuntimeSetID: mustPgUUID(set.ID), LeaseSeconds: int32(req.LeaseSeconds)})
	if err != nil {
		return ProjectEnvironmentQueueDelivery{}, mapErr(err)
	}
	clock, err := q.EnvironmentQueueDeliveryClock(ctx, tx)
	if err != nil {
		return ProjectEnvironmentQueueDelivery{}, err
	}
	if !row.LeaseExpiresAt.Time.After(clock.Time) {
		return ProjectEnvironmentQueueDelivery{}, ErrNotFound
	}
	inv, err = invocationFromSQLC(row)
	if err != nil {
		return ProjectEnvironmentQueueDelivery{}, err
	}
	token, tokenHash, err := newEnvironmentQueueReceipt()
	if err != nil {
		return ProjectEnvironmentQueueDelivery{}, err
	}
	count, err := q.UpsertEnvironmentQueueDeliveryReceipt(ctx, tx, sqlc.UpsertEnvironmentQueueDeliveryReceiptParams{
		InvocationID: row.ID, Attempt: row.Attempts, TokenHash: tokenHash, OwnerHash: environmentQueueReceiptOwnerHash(owner),
		IssuedAt: row.ReceivedAt, LeaseExpiresAt: row.LeaseExpiresAt})
	if err != nil {
		return ProjectEnvironmentQueueDelivery{}, mapErr(err)
	}
	if count != 1 {
		return ProjectEnvironmentQueueDelivery{}, ErrInvocationEnvironmentWorkIsolation
	}
	if err := tx.Commit(ctx); err != nil {
		return ProjectEnvironmentQueueDelivery{}, err
	}
	return cloneEnvironmentQueueDelivery(inv, consumer, token), nil
}

func (s *PgStore) CompleteProjectEnvironmentQueueDelivery(ctx context.Context, scope ProjectEnvironmentQueueDeliveryScope, id, token string, result json.RawMessage) error {
	if len(result) > 0 && !json.Valid(result) {
		return ErrInvalidArgument
	}
	return s.finishEnvironmentQueueDelivery(ctx, scope, id, token, result, nil)
}

func (s *PgStore) RetryProjectEnvironmentQueueDelivery(ctx context.Context, scope ProjectEnvironmentQueueDeliveryScope, id, token, lastError string) error {
	return s.finishEnvironmentQueueDelivery(ctx, scope, id, token, nil, &lastError)
}

func (s *PgStore) finishEnvironmentQueueDelivery(ctx context.Context, scope ProjectEnvironmentQueueDeliveryScope, id, token string, result json.RawMessage, lastError *string) error {
	if err := validateEnvironmentQueueDeliveryScope(scope); err != nil {
		return err
	}
	tokenHash, err := environmentQueueReceiptTokenHash(id, token)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	// Retiring a deployment stops new claims but must allow a current lease to
	// finish. Environment ownership is locked before the invocation and quota.
	set, err := projectEnvironmentQueueConsumersDB(ctx, tx, scope.AccountID, scope.ProjectID, scope.DeploymentID, false, false)
	if err != nil {
		return err
	}
	consumer, err := queueConsumerByName(set, scope.BindingName)
	if err != nil {
		return err
	}
	owner, err := readInvocationEnvironmentQueueAdmissionDB(ctx, tx, id)
	if err != nil {
		return err
	}
	if owner.ConsumerID != consumer.ID || owner.RuntimeSetID != set.ID {
		return ErrNotFound
	}
	if owned, err := validateInvocationQueueClaimDB(ctx, tx, id, false); err != nil || !owned {
		if err == nil {
			err = ErrInvocationEnvironmentWorkIsolation
		}
		return err
	}
	q := sqlc.New()
	row, err := q.LockEnvironmentQueueDeliveryInvocation(ctx, tx, mustPgUUID(id))
	if err != nil {
		return mapErr(err)
	}
	r, err := q.ReadEnvironmentQueueDeliveryReceipt(ctx, tx, row.ID)
	if err != nil {
		return mapErr(err)
	}
	clock, err := q.EnvironmentQueueDeliveryClock(ctx, tx)
	if err != nil {
		return err
	}
	inv, err := invocationFromSQLC(row)
	if err != nil {
		return err
	}
	if err := validateEnvironmentQueueReceipt(environmentQueueReceipt{Attempt: int(r.Attempt), TokenHash: r.TokenHash, OwnerHash: r.OwnerHash,
		IssuedAt: r.IssuedAt.Time, LeaseExpiresAt: r.LeaseExpiresAt.Time}, owner, inv, tokenHash, clock.Time); err != nil {
		return err
	}
	if lastError != nil {
		account, err := readEnvironmentQueueDeliveryAccount(ctx, tx, scope.AccountID)
		if err != nil {
			return err
		}
		inv, err = environmentQueueRetry(inv, account.Plan, clock.Time, *lastError)
		if err != nil {
			return err
		}
	} else {
		inv.State, inv.QuotaReserved, inv.CompletedAt, inv.LastError = InvocationCompleted, false, &clock.Time, ""
		outcome := OutcomeSuccess
		inv.Outcome = &outcome
	}
	args := sqlc.FinishEnvironmentQueueDeliveryInvocationParams{InvocationID: row.ID, Attempt: row.Attempts, State: string(inv.State),
		LastError: inv.LastError, DueAt: pgtype.Timestamptz{Time: inv.DueAt, Valid: true}, Result: result}
	if inv.Outcome != nil {
		args.Outcome = pgtype.Text{String: string(*inv.Outcome), Valid: true}
	}
	if inv.CompletedAt != nil {
		args.CompletedAt = pgtype.Timestamptz{Time: *inv.CompletedAt, Valid: true}
	}
	if inv.LeaseExpiresAt != nil {
		args.LeaseExpiresAt = pgtype.Timestamptz{Time: *inv.LeaseExpiresAt, Valid: true}
	}
	if inv.InstanceID != "" {
		args.InstanceID = mustPgUUID(inv.InstanceID)
	}
	count, err := q.FinishEnvironmentQueueDeliveryInvocation(ctx, tx, args)
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrNotFound
	}
	if err := q.ReleaseEnvironmentQueueDeliveryQuota(ctx, tx, mustPgUUID(scope.AccountID)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("state: finish environment queue delivery: %w", err)
	}
	return nil
}

// Generic completion cannot act on a receipt-owned attempt. Lock first, then
// read in a new statement so a concurrent receipt insert is visible after wait.
func rejectEnvironmentQueueReceiptDB(ctx context.Context, db sqlc.DBTX, id string, lock bool) error {
	q := sqlc.New()
	if lock {
		if _, err := q.LockLegacyInvocationReceiptFence(ctx, db, mustPgUUID(id)); err != nil {
			return mapErr(err)
		}
	}
	owned, err := q.EnvironmentQueueDeliveryReceiptExists(ctx, db, mustPgUUID(id))
	if err != nil {
		return err
	}
	if owned {
		return ErrConflict
	}
	return nil
}
