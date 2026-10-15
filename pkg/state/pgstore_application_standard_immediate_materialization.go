package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ApplicationStandardImmediateMaterializationStore = (*PgStore)(nil)

func (s *PgStore) ClaimApplicationStandardEnrollmentForApp(ctx context.Context, r ApplicationStandardEnrollmentClaimRequest) (ApplicationStandardEnrollmentClaim, error) {
	if !validStandardImmediateClaim(r) {
		return ApplicationStandardEnrollmentClaim{}, ErrInvalidArgument
	}
	row, err := sqlc.New().ClaimApplicationStandardEnrollmentForApp(ctx, s.pool, sqlc.ClaimApplicationStandardEnrollmentForAppParams{OrgID: mustPgUUID(r.OrgID), AppID: mustPgUUID(r.AppID), DesiredRevision: r.DesiredRevision, Owner: r.Owner, LeaseSeconds: api.ApplicationStandardWorkerLease.Seconds()})
	if errors.Is(err, pgx.ErrNoRows) {
		return ApplicationStandardEnrollmentClaim{}, ErrNotFound
	}
	if err != nil {
		return ApplicationStandardEnrollmentClaim{}, fmt.Errorf("claim application standard enrollment for app: %w", err)
	}
	return ApplicationStandardEnrollmentClaim{AppID: pgUUIDString(row.AppID), OrgID: pgUUIDString(row.OrgID), Owner: row.LeaseOwner, Generation: row.LeaseGeneration, DesiredRevision: row.DesiredRevision, Until: row.LeaseUntil.Time}, nil
}

func (s *PgStore) ReleaseApplicationStandardEnrollmentWorker(ctx context.Context, c ApplicationStandardEnrollmentClaim) error {
	if !standardEnrollmentClaimValid(c) {
		return ErrInvalidArgument
	}
	count, err := sqlc.New().ReleaseApplicationStandardEnrollmentWorker(ctx, s.pool, sqlc.ReleaseApplicationStandardEnrollmentWorkerParams{AppID: mustPgUUID(c.AppID), OrgID: mustPgUUID(c.OrgID), Owner: c.Owner, Generation: c.Generation})
	if err != nil {
		return fmt.Errorf("release application standard enrollment worker: %w", err)
	}
	if count != 1 {
		return ErrApplicationStandardLeaseLost
	}
	return nil
}
