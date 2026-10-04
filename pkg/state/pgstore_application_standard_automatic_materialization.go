package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ApplicationStandardAutomaticMaterializationStore = (*PgStore)(nil)

func (s *PgStore) ClaimApplicationStandardEnrollment(ctx context.Context, owner string) (ApplicationStandardEnrollmentClaim, error) {
	if !standardWorkerOwnerValid(owner) {
		return ApplicationStandardEnrollmentClaim{}, ErrInvalidArgument
	}
	if err := s.queueExpiredStandardExceptions(ctx); err != nil {
		return ApplicationStandardEnrollmentClaim{}, err
	}
	row, err := sqlc.New().ClaimApplicationStandardEnrollment(ctx, s.pool, sqlc.ClaimApplicationStandardEnrollmentParams{Owner: owner, LeaseSeconds: api.ApplicationStandardWorkerLease.Seconds(), RetrySeconds: api.ApplicationStandardBlockedRetry.Seconds()})
	if errors.Is(err, pgx.ErrNoRows) {
		return ApplicationStandardEnrollmentClaim{}, ErrNotFound
	}
	if err != nil {
		return ApplicationStandardEnrollmentClaim{}, fmt.Errorf("claim standard enrollment: %w", err)
	}
	return ApplicationStandardEnrollmentClaim{AppID: pgUUIDString(row.AppID), OrgID: pgUUIDString(row.OrgID), Owner: row.LeaseOwner, Generation: row.LeaseGeneration, DesiredRevision: row.DesiredRevision, Until: row.LeaseUntil.Time}, nil
}

func (s *PgStore) MaterializeApplicationStandardEnrollment(ctx context.Context, c ApplicationStandardEnrollmentClaim) (ApplicationStandardEnrollment, error) {
	if !standardEnrollmentClaimValid(c) {
		return ApplicationStandardEnrollment{}, ErrInvalidArgument
	}
	for attempt := 0; attempt < api.ApplicationStandardApprovalLockAttempts; attempt++ {
		e, err := s.materializeStandardEnrollmentAttempt(ctx, c)
		if !standardApprovalRetryable(err) {
			return e, err
		}
		timer := time.NewTimer(time.Duration(attempt+1) * api.ApplicationStandardApprovalLockRetry)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ApplicationStandardEnrollment{}, ctx.Err()
		case <-timer.C:
		}
	}
	return ApplicationStandardEnrollment{}, ErrApplicationStandardReviewBusy
}

func (s *PgStore) materializeStandardEnrollmentAttempt(ctx context.Context, c ApplicationStandardEnrollmentClaim) (ApplicationStandardEnrollment, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ApplicationStandardEnrollment{}, err
	}
	defer tx.Rollback(ctx)
	q := sqlc.New()
	until, err := q.LockApplicationStandardEnrollmentWorker(ctx, tx, sqlc.LockApplicationStandardEnrollmentWorkerParams{AppID: mustPgUUID(c.AppID), OrgID: mustPgUUID(c.OrgID), Owner: c.Owner, Generation: c.Generation, DesiredRevision: c.DesiredRevision})
	if errors.Is(err, pgx.ErrNoRows) {
		return ApplicationStandardEnrollment{}, ErrApplicationStandardLeaseLost
	}
	if err != nil {
		return ApplicationStandardEnrollment{}, err
	}
	c.Until = until.Time // Authority comes from storage, never a caller's timestamp.
	if _, err := q.LockApplicationStandardApprovalOrg(ctx, tx, mustPgUUID(c.OrgID)); err != nil {
		return ApplicationStandardEnrollment{}, err
	}
	queued, err := q.HasApplicationStandardQueuedTarget(ctx, tx, mustPgUUID(c.AppID))
	if err != nil {
		return ApplicationStandardEnrollment{}, err
	}
	if queued {
		if _, err := q.ReleaseApplicationStandardEnrollmentWorker(ctx, tx, sqlc.ReleaseApplicationStandardEnrollmentWorkerParams{AppID: mustPgUUID(c.AppID), OrgID: mustPgUUID(c.OrgID), Owner: c.Owner, Generation: c.Generation}); err != nil {
			return ApplicationStandardEnrollment{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return ApplicationStandardEnrollment{}, err
		}
		return ApplicationStandardEnrollment{}, ErrApplicationStandardOperationInProgress
	}
	r := ApplicationStandardReviewRequest{Scope: "application", ScopeID: c.AppID, StandardID: "00000000-0000-0000-0000-000000000000"}
	snapshot, err := lockStandardReviewInputs(ctx, tx, c.OrgID, c.OrgID, r)
	if err != nil {
		return ApplicationStandardEnrollment{}, err
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	app, proposed, code, err := resolveAutomaticStandardEnrollment(snapshot, now)
	if err != nil {
		return ApplicationStandardEnrollment{}, err
	}
	if code != "" {
		return blockAutomaticStandardEnrollment(ctx, tx, c, code)
	}
	projection, err := readStandardControlProjection(ctx, tx, app, proposed, now)
	if errors.Is(err, ErrConflict) || errors.Is(err, ErrApplicationStandardReviewBlocked) {
		return blockAutomaticStandardEnrollment(ctx, tx, c, "control_projection_conflict")
	}
	if err != nil {
		return ApplicationStandardEnrollment{}, err
	}
	e, err := automaticInstalledStandardEnrollment(app, proposed, now)
	if err != nil {
		return e, err
	}
	if err := installStandardControlProjectionWithClaim(ctx, tx, e, projection, &c); err != nil {
		return ApplicationStandardEnrollment{}, err
	}
	valid, err := q.VerifyAutomaticApplicationStandardInstallation(ctx, tx, sqlc.VerifyAutomaticApplicationStandardInstallationParams{AppID: mustPgUUID(c.AppID), OrgID: mustPgUUID(c.OrgID), DesiredRevision: c.DesiredRevision, Generation: c.Generation, ClaimUntil: standardPgTime(c.Until)})
	if err != nil {
		return ApplicationStandardEnrollment{}, err
	}
	if !valid {
		return ApplicationStandardEnrollment{}, ErrApplicationStandardLeaseLost
	}
	e, err = readStandardEnrollment(ctx, tx, c.OrgID, c.AppID)
	if err != nil {
		return ApplicationStandardEnrollment{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ApplicationStandardEnrollment{}, fmt.Errorf("commit automatic standard projection: %w", err)
	}
	return e, nil
}

func blockAutomaticStandardEnrollment(ctx context.Context, tx pgx.Tx, c ApplicationStandardEnrollmentClaim, code string) (ApplicationStandardEnrollment, error) {
	count, err := sqlc.New().BlockApplicationStandardEnrollmentWorker(ctx, tx, sqlc.BlockApplicationStandardEnrollmentWorkerParams{AppID: mustPgUUID(c.AppID), OrgID: mustPgUUID(c.OrgID), Owner: c.Owner, Generation: c.Generation, ErrorCode: code})
	if err != nil {
		return ApplicationStandardEnrollment{}, err
	}
	if count != 1 {
		return ApplicationStandardEnrollment{}, ErrApplicationStandardLeaseLost
	}
	e, err := readStandardEnrollment(ctx, tx, c.OrgID, c.AppID)
	if err != nil {
		return ApplicationStandardEnrollment{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ApplicationStandardEnrollment{}, err
	}
	return e, nil
}

func (s *PgStore) ReleaseApplicationStandardOperationWorker(ctx context.Context, c ApplicationStandardWorkerClaim) error {
	if !standardWorkerClaimValid(c) {
		return ErrInvalidArgument
	}
	count, err := sqlc.New().ReleaseApplicationStandardOperationWorker(ctx, s.pool, sqlc.ReleaseApplicationStandardOperationWorkerParams{OperationID: mustPgUUID(c.OperationID), OrgID: mustPgUUID(c.OrgID), Owner: c.Owner, Generation: c.Generation})
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrApplicationStandardLeaseLost
	}
	return nil
}
