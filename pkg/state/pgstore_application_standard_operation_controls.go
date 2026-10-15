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

var _ ApplicationStandardOperationControlStore = (*PgStore)(nil)

func (s *PgStore) ControlApplicationStandardOperation(ctx context.Context, orgID, actorID, operationID string, expected time.Time, action ApplicationStandardOperationAction) (ApplicationStandardOperation, error) {
	if !validStandardOperationControl(orgID, actorID, operationID, expected, action) {
		return ApplicationStandardOperation{}, ErrInvalidArgument
	}
	for attempt := 0; attempt < api.ApplicationStandardApprovalLockAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return ApplicationStandardOperation{}, err
		}
		o, err := s.controlStandardOperationAttempt(ctx, orgID, actorID, operationID, expected, action)
		if !standardApprovalRetryable(err) {
			return o, err
		}
		if attempt+1 == api.ApplicationStandardApprovalLockAttempts {
			break
		}
		timer := time.NewTimer(time.Duration(attempt+1) * api.ApplicationStandardApprovalLockRetry)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ApplicationStandardOperation{}, ctx.Err()
		case <-timer.C:
		}
	}
	return ApplicationStandardOperation{}, ErrApplicationStandardReviewBusy
}

func (s *PgStore) controlStandardOperationAttempt(ctx context.Context, orgID, actorID, operationID string, expected time.Time, action ApplicationStandardOperationAction) (ApplicationStandardOperation, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ApplicationStandardOperation{}, fmt.Errorf("begin standard operation control: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	now, err := lockStandardOperationControl(ctx, tx, orgID, actorID, operationID)
	if err != nil {
		return ApplicationStandardOperation{}, err
	}
	before, err := readStandardOperation(ctx, tx, orgID, operationID, "")
	if err != nil {
		return ApplicationStandardOperation{}, err
	}
	after, err := prepareStandardOperationControl(before, expected, action, now)
	if err != nil || after.UpdatedAt.Equal(before.UpdatedAt) {
		return after, err
	}
	if err := persistStandardOperationControl(ctx, tx, before, after, actorID, action); err != nil {
		return ApplicationStandardOperation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ApplicationStandardOperation{}, fmt.Errorf("commit standard operation control: %w", err)
	}
	return after, nil
}

func lockStandardOperationControl(ctx context.Context, tx pgx.Tx, orgID, actorID, operationID string) (time.Time, error) {
	q := sqlc.New()
	if _, err := q.LockApplicationStandardApprovalOrg(ctx, tx, mustPgUUID(orgID)); errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, ErrNotFound
	} else if err != nil {
		return time.Time{}, err
	}
	if _, err := q.LockApplicationStandardApprovalAccounts(ctx, tx, standardPgUUIDs([]string{actorID})); err != nil {
		return time.Time{}, err
	}
	if _, err := q.LockApplicationStandardApprovalMemberships(ctx, tx, mustPgUUID(orgID)); err != nil {
		return time.Time{}, err
	}
	a, err := q.ReadApplicationStandardOperationAuthority(ctx, tx, sqlc.ReadApplicationStandardOperationAuthorityParams{OrgID: mustPgUUID(orgID), ActorID: mustPgUUID(actorID)})
	if err != nil {
		return time.Time{}, err
	}
	if err := authorizeStandardApproval(standardReviewSnapshot{OrgStatus: a.Status, DeletedPending: a.DeletedPending, ActorAuthorized: a.ActorAuthorized}); err != nil {
		return time.Time{}, err
	}
	if _, err := q.LockApplicationStandardOperationControl(ctx, tx, sqlc.LockApplicationStandardOperationControlParams{OrgID: mustPgUUID(orgID), OperationID: mustPgUUID(operationID)}); errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, ErrNotFound
	} else if err != nil {
		return time.Time{}, err
	}
	return a.StorageTime.Time, nil
}

func persistStandardOperationControl(ctx context.Context, tx pgx.Tx, before, after ApplicationStandardOperation, actorID string, action ApplicationStandardOperationAction) error {
	q := sqlc.New()
	now := pgtype.Timestamptz{Time: after.UpdatedAt, Valid: true}
	count, err := q.ControlApplicationStandardOperation(ctx, tx, sqlc.ControlApplicationStandardOperationParams{OrgID: mustPgUUID(after.OrgID), OperationID: mustPgUUID(after.ID), ExpectedUpdatedAt: pgtype.Timestamptz{Time: before.UpdatedAt, Valid: true}, State: after.State, ErrorCode: after.ErrorCode, Now: now})
	if err != nil {
		return fmt.Errorf("write standard operation control: %w", err)
	}
	if count != 1 {
		return ErrApplicationStandardOperationStale
	}
	if action == ApplicationStandardOperationAbort {
		if err := q.SkipAbortedApplicationStandardTargets(ctx, tx, sqlc.SkipAbortedApplicationStandardTargetsParams{OperationID: mustPgUUID(after.ID), Now: now}); err != nil {
			return fmt.Errorf("skip aborted standard targets: %w", err)
		}
	}
	audit := standardOperationControlAudit(before, after, actorID, action)
	if err := q.InsertApplicationStandardOperationAudit(ctx, tx, sqlc.InsertApplicationStandardOperationAuditParams{ID: pgtype.UUID{Bytes: audit.ID, Valid: true}, Kind: audit.Kind, Now: now, Data: audit.Data}); err != nil {
		return fmt.Errorf("audit standard operation control: %w", err)
	}
	return nil
}
