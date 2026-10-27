// adr: 20261018183000
package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

const operationChildrenListMax = 1000

// StartChildOperation takes the locks every Operation admission takes, in
// the same order (workflow admission, account, then the parent's execution),
// so a child start serializes with the parent's cancellation and its
// pending-work count.
func (s *PgStore) StartChildOperation(ctx context.Context, in OperationChildStart) (OperationChild, bool, error) {
	if err := validateOperationChildRequest(in.Request); err != nil {
		return OperationChild{}, false, err
	}
	account, err := operationUUID(in.accountID())
	if err != nil {
		return OperationChild{}, false, err
	}
	if in.JobAuthority == nil && in.WorkflowAuthority == nil {
		if _, err := operationUUID(in.Authority.InvocationID); err != nil {
			return OperationChild{}, false, err
		}
	}
	// Read the parent and the immutable child definition before taking a
	// transaction connection; both are rechecked under the locks below.
	snapshot, err := s.OperationByID(ctx, in.accountID(), "", in.ParentID)
	if err != nil {
		return OperationChild{}, false, err
	}
	childDef, err := s.OperationDefinitionForDeployment(ctx, snapshot.AccountID, snapshot.AppID, snapshot.DeploymentID, in.Request.Operation)
	if err != nil {
		return OperationChild{}, false, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return OperationChild{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if childDef.Spec.Workflow != "" {
		if err := q.LockWorkflowRunAdmission(ctx, tx, childDef.AppID); err != nil {
			return OperationChild{}, false, err
		}
	}
	plan, err := q.LockCustomerOperationAccount(ctx, tx, account)
	if err != nil {
		return OperationChild{}, false, mapErr(err)
	}
	parent, err := lockChildParentTx(ctx, tx, in, snapshot.ID)
	if err != nil {
		return OperationChild{}, false, err
	}
	parentID := mustPgUUID(parent.ID)
	isChild, err := q.IsOperationChild(ctx, tx, parentID)
	if err != nil {
		return OperationChild{}, false, err
	}
	if err := checkOperationChildParent(parent, isChild); err != nil {
		return OperationChild{}, false, err
	}
	known, err := q.GetOperationChildByKey(ctx, tx, sqlc.GetOperationChildByKeyParams{ParentOperationID: parentID, ChildKey: in.Request.Key})
	switch {
	case err == nil:
	case errors.Is(err, pgx.ErrNoRows):
		limit := api.MustLimitsFor(api.Plan(plan)).Operations.ChildrenPerOperation
		started, err := q.CountOperationChildren(ctx, tx, parentID)
		if err != nil {
			return OperationChild{}, false, err
		}
		if started >= int64(limit) {
			return OperationChild{}, false, NewOperationLimitError("children_per_operation", int64(limit), started+1)
		}
	default:
		return OperationChild{}, false, err
	}
	child, created, err := s.admitOperationTx(ctx, tx, account, mustPgUUID(childDef.ID), mustPgUUID(parent.PlatformTenantID), operationChildAdmission(parent, childDef.ID, in.Request), childDef)
	if err != nil {
		return OperationChild{}, false, err
	}
	if known.Valid {
		if operationUUIDString(known) != child.ID {
			return OperationChild{}, false, fmt.Errorf("%w: key names an expired child", ErrConflict)
		}
		return OperationChild{Key: in.Request.Key, Operation: child}, false, nil
	}
	if err := q.InsertOperationChild(ctx, tx, sqlc.InsertOperationChildParams{ParentOperationID: parentID, ChildKey: in.Request.Key,
		ChildOperationID: mustPgUUID(child.ID), AccountID: account, CreatedAt: pgtype.Timestamptz{Time: child.CreatedAt, Valid: true}}); err != nil {
		return OperationChild{}, false, fmt.Errorf("state: record child operation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return OperationChild{}, false, err
	}
	return OperationChild{Key: in.Request.Key, Operation: child}, created, nil
}

// lockChildParentTx locks the parent's current execution, the lock its
// cancellation takes, and checks the presented proof against it.
func lockChildParentTx(ctx context.Context, tx pgx.Tx, in OperationChildStart, parentID string) (Operation, error) {
	if in.WorkflowAuthority != nil {
		parent, _, _, err := workflowOperationAuthorityTx(ctx, tx, parentID, *in.WorkflowAuthority, false)
		return parent, err
	}
	if in.JobAuthority != nil {
		parent, _, err := jobOperationAuthorityTx(ctx, tx, parentID, *in.JobAuthority)
		return parent, err
	}
	inv, err := operationLockedInvocation(ctx, tx, in.Authority.InvocationID)
	if err != nil {
		return Operation{}, err
	}
	parent, _, _, exists, err := operationForInvocationTx(ctx, tx, inv.ID)
	if err != nil {
		return Operation{}, err
	}
	if !exists || parent.ID != parentID {
		return Operation{}, ErrNotFound
	}
	return parent, ValidateOperationExecutionAuthority(parent, inv, in.Authority, time.Now().UTC())
}

func (s *PgStore) OperationChildren(ctx context.Context, accountID, tenantID, parentID string) ([]OperationChild, error) {
	parent, err := s.OperationByID(ctx, accountID, tenantID, parentID)
	if err != nil {
		return nil, err
	}
	rows, err := sqlc.New().ListOperationChildren(ctx, s.pool, sqlc.ListOperationChildrenParams{ParentOperationID: mustPgUUID(parent.ID),
		AccountID: mustPgUUID(parent.AccountID), RowLimit: operationChildrenListMax})
	if err != nil {
		return nil, err
	}
	out := make([]OperationChild, 0, len(rows))
	for _, row := range rows {
		op, err := operationPGRecord(row.Record)
		if err != nil {
			return nil, err
		}
		out = append(out, OperationChild{Key: row.ChildKey, Operation: op})
	}
	return out, nil
}

func (s *PgStore) OperationChildSettlements(ctx context.Context, limit int) ([]OperationChildSettlement, error) {
	rows, err := sqlc.New().ListSettledChildParents(ctx, s.pool, sqlc.ListSettledChildParentsParams{Event: api.OperationChildrenSettledEvent, RowLimit: int32(limit)})
	if err != nil {
		return nil, err
	}
	out := make([]OperationChildSettlement, 0, len(rows))
	for _, row := range rows {
		out = append(out, OperationChildSettlement{AccountID: pgUUIDString(row.AccountID), ParentID: pgUUIDString(row.ID),
			Children:   api.OperationChildrenSettled{Total: row.Total, Succeeded: row.Succeeded, Failed: row.Failed, Cancelled: row.Cancelled},
			ReadySince: row.ReadySince.Time})
	}
	return out, nil
}
