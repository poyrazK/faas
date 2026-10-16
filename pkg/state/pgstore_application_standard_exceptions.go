package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ApplicationStandardExceptionStore = (*PgStore)(nil)

func (s *PgStore) ApproveApplicationStandardException(ctx context.Context, orgID, actorID, appID string, r ApplicationStandardExceptionRequest) (ApplicationStandardException, error) {
	x, err := prepareStandardException(orgID, actorID, appID, r)
	if err != nil {
		return x, err
	}
	return s.mutateStandardException(ctx, x, actorID, r.ExpectedRevision, false)
}

func (s *PgStore) RevokeApplicationStandardException(ctx context.Context, orgID, actorID, appID, exceptionID string, expected int64) (ApplicationStandardException, error) {
	if !standardApprovalIdentityValid(orgID, actorID, appID) || !validStandardResourceRead(orgID, exceptionID) || expected <= 0 || expected >= api.ApplicationStandardMaxVersion {
		return ApplicationStandardException{}, ErrInvalidArgument
	}
	x := ApplicationStandardException{OrgID: canonicalStandardUUID(orgID), AppID: canonicalStandardUUID(appID)}
	x.ID = canonicalStandardUUID(exceptionID)
	return s.mutateStandardException(ctx, x, actorID, expected, true)
}

func (s *PgStore) mutateStandardException(ctx context.Context, x ApplicationStandardException, actorID string, expected int64, revoke bool) (ApplicationStandardException, error) {
	for attempt := 0; attempt < api.ApplicationStandardApprovalLockAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return ApplicationStandardException{}, err
		}
		out, err := s.mutateStandardExceptionAttempt(ctx, x, actorID, expected, revoke)
		if !standardApprovalRetryable(err) {
			return out, err
		}
		if attempt+1 == api.ApplicationStandardApprovalLockAttempts {
			break
		}
		timer := time.NewTimer(time.Duration(attempt+1) * api.ApplicationStandardApprovalLockRetry)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ApplicationStandardException{}, ctx.Err()
		case <-timer.C:
		}
	}
	return ApplicationStandardException{}, ErrApplicationStandardReviewBusy
}

func (s *PgStore) mutateStandardExceptionAttempt(ctx context.Context, x ApplicationStandardException, actorID string, expected int64, revoke bool) (ApplicationStandardException, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ApplicationStandardException{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	snapshot, now, err := lockStandardLocalIntentInputs(ctx, tx, x.OrgID, actorID, x.AppID)
	if err != nil {
		return ApplicationStandardException{}, err
	}
	if err := authorizeStandardApproval(snapshot); err != nil {
		return ApplicationStandardException{}, err
	}
	before, err := readStandardEnrollment(ctx, tx, x.OrgID, x.AppID)
	if err != nil {
		return ApplicationStandardException{}, err
	}
	if before.DesiredRevision != expected {
		return ApplicationStandardException{}, ErrApplicationStandardLocalIntentStale
	}
	x, err = writeStandardException(ctx, tx, snapshot, before, x, actorID, expected, now, revoke)
	if err != nil {
		return ApplicationStandardException{}, err
	}
	if err := persistStandardExceptionChange(ctx, tx, x, before, actorID, now, revoke); err != nil {
		return ApplicationStandardException{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ApplicationStandardException{}, fmt.Errorf("commit standard exception: %w", err)
	}
	return x, nil
}

func writeStandardException(ctx context.Context, tx pgx.Tx, snapshot standardReviewSnapshot, before ApplicationStandardEnrollment, x ApplicationStandardException, actorID string, expected int64, now time.Time, revoke bool) (ApplicationStandardException, error) {
	q := sqlc.New()
	now = now.UTC().Truncate(time.Microsecond)
	if revoke {
		raw, err := q.ReadApplicationStandardException(ctx, tx, sqlc.ReadApplicationStandardExceptionParams{OrgID: mustPgUUID(x.OrgID), AppID: mustPgUUID(x.AppID), ExceptionID: mustPgUUID(x.ID)})
		if errors.Is(err, pgx.ErrNoRows) {
			return x, ErrNotFound
		}
		if err != nil {
			return x, err
		}
		x, err = decodeStandardException(raw)
		if err != nil {
			return x, err
		}
		if x.RevokedAt != nil {
			return x, ErrConflict
		}
		count, err := q.RevokeApplicationStandardException(ctx, tx, sqlc.RevokeApplicationStandardExceptionParams{OrgID: mustPgUUID(x.OrgID), AppID: mustPgUUID(x.AppID), ExceptionID: mustPgUUID(x.ID), ActorID: mustPgUUID(actorID), Now: standardPgTime(now)})
		if err != nil {
			return x, err
		}
		if count != 1 {
			return x, ErrConflict
		}
		x.RevokedAt = &now
		x.RevokedBy = canonicalStandardUUID(actorID)
		return x, nil
	}
	x.CreatedAt = now
	if err := validateStandardException(snapshot, before, x, expected, now); err != nil {
		return x, err
	}
	err := q.InsertApplicationStandardException(ctx, tx, sqlc.InsertApplicationStandardExceptionParams{ID: mustPgUUID(x.ID), OrgID: mustPgUUID(x.OrgID), AppID: mustPgUUID(x.AppID), StandardID: mustPgUUID(x.StandardID), Version: x.Version, Field: string(x.Field), Value: x.Value, Reason: x.Reason, ActorID: mustPgUUID(actorID), Now: standardPgTime(now), ExpiresAt: standardPgTime(x.ExpiresAt)})
	return x, err
}

func persistStandardExceptionChange(ctx context.Context, tx pgx.Tx, x ApplicationStandardException, before ApplicationStandardEnrollment, actorID string, now time.Time, revoke bool) error {
	q := sqlc.New()
	count, err := q.QueueApplicationStandardExceptionChange(ctx, tx, sqlc.QueueApplicationStandardExceptionChangeParams{OrgID: mustPgUUID(x.OrgID), AppID: mustPgUUID(x.AppID), ExpectedRevision: before.DesiredRevision, Now: standardPgTime(now)})
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrApplicationStandardLocalIntentStale
	}
	action := "approved"
	if revoke {
		action = "revoked"
	}
	audit := standardExceptionAudit(x, before, actorID, action, now)
	return q.InsertApplicationStandardOperationAudit(ctx, tx, sqlc.InsertApplicationStandardOperationAuditParams{ID: pgtype.UUID{Bytes: audit.ID, Valid: true}, Kind: audit.Kind, Now: standardPgTime(now), Data: audit.Data})
}

func decodeStandardException(raw []byte) (ApplicationStandardException, error) {
	var x ApplicationStandardException
	if err := json.Unmarshal(raw, &x); err != nil {
		return x, fmt.Errorf("decode standard exception: %w", err)
	}
	x.CreatedAt, x.ExpiresAt = x.CreatedAt.UTC(), x.ExpiresAt.UTC()
	if x.RevokedAt != nil {
		t := x.RevokedAt.UTC()
		x.RevokedAt = &t
	}
	return x, nil
}

func (s *PgStore) ListApplicationStandardExceptions(ctx context.Context, orgID, appID, after string) ([]ApplicationStandardException, error) {
	if !validStandardResourceRead(orgID, appID) || after != "" && !validStandardResourceRead(orgID, after) {
		return nil, ErrInvalidArgument
	}
	app, err := s.AppByID(ctx, appID)
	if err != nil {
		return nil, err
	}
	if app.Status == AppDeleted || !sameStandardUUID(app.OrgID, orgID) {
		return nil, ErrNotFound
	}
	if after != "" {
		after = canonicalStandardUUID(after)
	}
	rows, err := sqlc.New().ListApplicationStandardExceptions(ctx, s.pool, sqlc.ListApplicationStandardExceptionsParams{OrgID: mustPgUUID(orgID), AppID: mustPgUUID(appID), AfterID: after, PageLimit: api.ApplicationStandardMaxListPage})
	if err != nil {
		return nil, fmt.Errorf("list standard exceptions: %w", err)
	}
	xs := []ApplicationStandardException{}
	for _, raw := range rows {
		x, err := decodeStandardException(raw)
		if err != nil {
			return nil, err
		}
		xs = append(xs, x)
	}
	return xs, nil
}

func standardNullablePgTime(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return standardPgTime(*t)
}
