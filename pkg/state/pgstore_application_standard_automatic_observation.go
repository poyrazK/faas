package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ApplicationStandardAutomaticObservationStore = (*PgStore)(nil)

func (s *PgStore) ClaimApplicationStandardObservation(ctx context.Context, owner string) (ApplicationStandardEnrollmentClaim, error) {
	if !standardWorkerOwnerValid(owner) {
		return ApplicationStandardEnrollmentClaim{}, ErrInvalidArgument
	}
	row, err := sqlc.New().ClaimApplicationStandardObservation(ctx, s.pool, sqlc.ClaimApplicationStandardObservationParams{Owner: owner, LeaseSeconds: api.ApplicationStandardWorkerLease.Seconds(), CheckSeconds: api.ApplicationStandardObservationInterval.Seconds()})
	if errors.Is(err, pgx.ErrNoRows) {
		return ApplicationStandardEnrollmentClaim{}, ErrNotFound
	}
	if err != nil {
		return ApplicationStandardEnrollmentClaim{}, standardRuntimeQualificationReadError(err)
	}
	return ApplicationStandardEnrollmentClaim{AppID: pgUUIDString(row.AppID), OrgID: pgUUIDString(row.OrgID), Owner: row.LeaseOwner, Generation: row.LeaseGeneration, DesiredRevision: row.DesiredRevision, Until: row.LeaseUntil.Time}, nil
}

func (s *PgStore) ObserveApplicationStandardEnrollment(ctx context.Context, c ApplicationStandardEnrollmentClaim) (ApplicationStandardEnrollment, error) {
	if !standardEnrollmentClaimValid(c) {
		return ApplicationStandardEnrollment{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ApplicationStandardEnrollment{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	until, err := lockStandardAutomaticObservation(ctx, tx, c)
	if err != nil {
		return ApplicationStandardEnrollment{}, err
	}
	c.Until = until // Never trust a caller-supplied deadline.
	q, err := qualifyStandardApplicationTx(ctx, tx, c.OrgID, c.AppID, c.DesiredRevision)
	if err != nil {
		return ApplicationStandardEnrollment{}, err
	}
	e, err := checkpointStandardAutomaticObservation(ctx, tx, c, q)
	if err != nil {
		return e, err
	}
	return e, tx.Commit(ctx)
}

func lockStandardAutomaticObservation(ctx context.Context, tx pgx.Tx, c ApplicationStandardEnrollmentClaim) (time.Time, error) {
	q := sqlc.New()
	// Approvals and pause/abort controls take this same organization fence.
	if _, err := q.LockApplicationStandardApprovalOrg(ctx, tx, mustPgUUID(c.OrgID)); err != nil {
		return time.Time{}, standardRuntimeQualificationReadError(err)
	}
	until, err := q.LockApplicationStandardObservationWorker(ctx, tx, sqlc.LockApplicationStandardObservationWorkerParams{AppID: mustPgUUID(c.AppID), OrgID: mustPgUUID(c.OrgID), Owner: c.Owner, Generation: c.Generation, DesiredRevision: c.DesiredRevision})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return time.Time{}, ErrApplicationStandardLeaseLost
		}
		return time.Time{}, standardRuntimeQualificationReadError(err)
	}
	active, err := q.HasApplicationStandardLocalIntentOperation(ctx, tx, mustPgUUID(c.AppID))
	if err != nil {
		return time.Time{}, err
	}
	if active {
		return time.Time{}, ErrApplicationStandardOperationInProgress
	}
	return until.Time, nil
}

func checkpointStandardAutomaticObservation(ctx context.Context, tx pgx.Tx, c ApplicationStandardEnrollmentClaim, qualification standardApplicationQualification) (ApplicationStandardEnrollment, error) {
	q := sqlc.New()
	clock, err := q.ArtifactEvidenceStorageTime(ctx, tx)
	if err != nil || !clock.Valid {
		return ApplicationStandardEnrollment{}, errors.Join(ErrApplicationStandardRuntimeStale, err)
	}
	e, err := readStandardEnrollment(ctx, tx, c.OrgID, c.AppID)
	if err != nil {
		return e, err
	}
	e = standardObservedEnrollment(e, qualification, clock.Time)
	count, err := q.CheckpointAutomaticApplicationStandardObservation(ctx, tx, sqlc.CheckpointAutomaticApplicationStandardObservationParams{AppID: mustPgUUID(c.AppID), OrgID: mustPgUUID(c.OrgID), Owner: c.Owner, Generation: c.Generation, DesiredRevision: c.DesiredRevision, Qualified: e.State == "observed", ErrorCode: e.ErrorCode, EvidenceUntil: standardPgTime(qualification.until)})
	if err != nil {
		return ApplicationStandardEnrollment{}, err
	}
	if count != 1 {
		return ApplicationStandardEnrollment{}, ErrApplicationStandardLeaseLost
	}
	valid, err := q.VerifyAutomaticApplicationStandardObservation(ctx, tx, sqlc.VerifyAutomaticApplicationStandardObservationParams{AppID: mustPgUUID(c.AppID), OrgID: mustPgUUID(c.OrgID), Generation: c.Generation, DesiredRevision: c.DesiredRevision, ClaimUntil: standardPgTime(c.Until), Qualified: e.State == "observed", EvidenceUntil: standardPgTime(qualification.until)})
	if err != nil {
		return ApplicationStandardEnrollment{}, err
	}
	if !valid {
		return ApplicationStandardEnrollment{}, ErrApplicationStandardLeaseLost
	}
	return readStandardEnrollment(ctx, tx, c.OrgID, c.AppID)
}
