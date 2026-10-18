package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) CreateDataRepairOperation(ctx context.Context, in DataRepairAdmission) (Operation, bool, error) {
	if err := validateDataRepairAdmission(in); err != nil {
		return Operation{}, false, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Operation{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	plan, err := q.LockCustomerOperationAccount(ctx, tx, mustPgUUID(in.AccountID))
	if err != nil {
		return Operation{}, false, mapErr(err)
	}
	prior, err := q.GetCustomerOperation(ctx, tx, sqlc.GetCustomerOperationParams{ID: mustPgUUID(in.ID), AccountID: mustPgUUID(in.AccountID)})
	if err == nil {
		op, e := operationPGRecord(prior)
		if e != nil {
			return Operation{}, false, e
		}
		return op, false, replayDataRepair(op, in)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Operation{}, false, err
	}
	if _, err := q.LockCustomerOperationCodeApp(ctx, tx, sqlc.LockCustomerOperationCodeAppParams{AppID: mustPgUUID(in.AppID), AccountID: mustPgUUID(in.AccountID)}); err != nil {
		return Operation{}, false, mapErr(err)
	}
	ownerID := in.Plan.SourceOperationID
	if in.Plan.ReplacesPlanID != "" {
		ownerID = in.Plan.ReplacesPlanID
	}
	sourceRaw, err := q.LockDataRepairOperation(ctx, tx, sqlc.LockDataRepairOperationParams{ID: mustPgUUID(ownerID), AccountID: mustPgUUID(in.AccountID)})
	if err != nil {
		return Operation{}, false, mapErr(err)
	}
	source, err := operationPGRecord(sourceRaw)
	if err != nil {
		return Operation{}, false, err
	}
	var parent Operation
	var parentEvent api.OperationEvent
	parentPending := false
	if in.Plan.ReplacesPlanID != "" {
		parent = cloneOperation(source)
		parentPending = !parent.State.Terminal()
		parentEvent, err = finishDataRepairReplacement(&parent, in, time.Now().UTC())
		if err != nil {
			return Operation{}, false, err
		}
	}
	limits := api.MustLimitsFor(api.Plan(plan)).Operations
	if !limits.Allowed {
		return Operation{}, false, NewOperationLimitError("plan_admission", 0, 1)
	}
	pending, err := q.CountPendingCustomerOperations(ctx, tx, mustPgUUID(in.AccountID))
	if err != nil {
		return Operation{}, false, err
	}
	if parentPending {
		pending-- // Child admission replaces its reserved nonterminal parent.
	}
	if pending >= int64(limits.PendingPerAccount) {
		return Operation{}, false, NewOperationLimitError("pending_per_account", int64(limits.PendingPerAccount), pending+1)
	}
	op, err := newDataRepairOperation(source, in, limits, time.Now().UTC())
	if err != nil {
		return Operation{}, false, err
	}
	record, err := operationRecordJSON(op)
	if err != nil {
		return Operation{}, false, err
	}
	if err = q.InsertDataRepairOperation(ctx, tx, sqlc.InsertDataRepairOperationParams{ID: mustPgUUID(op.ID), AccountID: mustPgUUID(op.AccountID), AppID: mustPgUUID(op.AppID), TenantID: mustPgUUID(op.PlatformTenantID), State: string(op.State), Record: record, ExpiresAt: operationPGTime(&op.ExpiresAt), CreatedAt: operationPGTime(&op.CreatedAt)}); err != nil {
		return Operation{}, false, mapErr(err)
	}
	if err = q.InsertDataRepairExecution(ctx, tx, mustPgUUID(op.ID)); err != nil {
		return Operation{}, false, err
	}
	if err = insertDataRepairEvent(ctx, tx, op, api.OperationEvent{Sequence: 1, Type: "accepted", ExecutionID: op.ID, Data: initialOperationEvent(op).Data, CreatedAt: op.CreatedAt}); err != nil {
		return Operation{}, false, err
	}
	if in.Plan.ReplacesPlanID != "" {
		if err = saveDataRepairTx(ctx, tx, parent, parentEvent); err != nil {
			return Operation{}, false, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return Operation{}, false, err
	}
	return op, true, nil
}

func insertDataRepairEvent(ctx context.Context, tx pgx.Tx, op Operation, event api.OperationEvent) error {
	q := sqlc.New()
	if err := q.InsertCustomerOperationEvent(ctx, tx, sqlc.InsertCustomerOperationEventParams{OperationID: mustPgUUID(op.ID), Sequence: event.Sequence, EventType: event.Type, ExecutionID: mustPgUUID(op.ID), Attempt: int32(event.Attempt), Data: event.Data, CreatedAt: pgtype.Timestamptz{Time: event.CreatedAt, Valid: true}}); err != nil {
		return err
	}
	return q.NotifyCustomerOperation(ctx, tx, op.ID)
}

func saveDataRepairTx(ctx context.Context, tx pgx.Tx, op Operation, event api.OperationEvent) error {
	record, err := operationRecordJSON(op)
	if err != nil {
		return err
	}
	if err = sqlc.New().UpdateDataRepairOperation(ctx, tx, sqlc.UpdateDataRepairOperationParams{ID: mustPgUUID(op.ID), State: string(op.State), Record: record, ExpiresAt: operationPGTime(&op.ExpiresAt)}); err != nil {
		return err
	}
	return insertDataRepairEvent(ctx, tx, op, event)
}

func (s *PgStore) mutateDataRepair(ctx context.Context, account, id string, mutate func(*Operation) (api.OperationEvent, bool, error)) (Operation, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Operation{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	raw, err := sqlc.New().LockDataRepairOperation(ctx, tx, sqlc.LockDataRepairOperationParams{ID: mustPgUUID(id), AccountID: mustPgUUID(account)})
	if err != nil {
		return Operation{}, mapErr(err)
	}
	op, err := operationPGRecord(raw)
	if err != nil {
		return Operation{}, err
	}
	event, replay, err := mutate(&op)
	if err != nil {
		return Operation{}, err
	}
	if !replay {
		if err = saveDataRepairTx(ctx, tx, op, event); err != nil {
			return Operation{}, err
		}
	}
	return op, tx.Commit(ctx)
}

func (s *PgStore) AdvanceDataRepairOperation(ctx context.Context, account, id string, in DataRepairAdvance) (Operation, error) {
	return s.mutateDataRepair(ctx, account, id, func(op *Operation) (api.OperationEvent, bool, error) {
		event, err := advanceDataRepair(op, in, time.Now().UTC())
		return event, false, err
	})
}
