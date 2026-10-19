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

var _ ApplicationStandardLocalIntentStore = (*PgStore)(nil)

func (s *PgStore) SetApplicationStandardLocalIntent(ctx context.Context, orgID, actorID, appID string, r ApplicationStandardLocalIntentRequest) (ApplicationStandardEnrollment, error) {
	intent, err := prepareStandardLocalIntent(orgID, actorID, appID, r)
	if err != nil {
		return ApplicationStandardEnrollment{}, err
	}
	for attempt := 0; attempt < api.ApplicationStandardApprovalLockAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return ApplicationStandardEnrollment{}, err
		}
		e, err := s.saveStandardLocalIntentAttempt(ctx, orgID, actorID, appID, intent)
		if !standardApprovalRetryable(err) {
			return e, err
		}
		if attempt+1 == api.ApplicationStandardApprovalLockAttempts {
			break
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

func (s *PgStore) saveStandardLocalIntentAttempt(ctx context.Context, orgID, actorID, appID string, intent standardLocalIntent) (ApplicationStandardEnrollment, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ApplicationStandardEnrollment{}, fmt.Errorf("begin local application intent: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	snapshot, now, err := lockStandardLocalIntentInputs(ctx, tx, orgID, actorID, appID)
	if err != nil {
		return ApplicationStandardEnrollment{}, err
	}
	before, err := readStandardEnrollment(ctx, tx, orgID, appID)
	if err != nil {
		return ApplicationStandardEnrollment{}, err
	}
	proposed, changed, err := validateStandardLocalIntent(snapshot, before, intent, now)
	if err != nil || !changed {
		return before, err
	}
	after := standardSavedLocalIntent(before, proposed, now)
	if err := persistStandardLocalIntent(ctx, tx, before, after, proposed, actorID); err != nil {
		return ApplicationStandardEnrollment{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ApplicationStandardEnrollment{}, fmt.Errorf("commit local application intent: %w", err)
	}
	return after, nil
}

func lockStandardLocalIntentInputs(ctx context.Context, tx pgx.Tx, orgID, actorID, appID string) (standardReviewSnapshot, time.Time, error) {
	q := sqlc.New()
	if _, err := q.LockApplicationStandardApprovalOrg(ctx, tx, mustPgUUID(orgID)); errors.Is(err, pgx.ErrNoRows) {
		return standardReviewSnapshot{}, time.Time{}, ErrNotFound
	} else if err != nil {
		return standardReviewSnapshot{}, time.Time{}, err
	}
	r := ApplicationStandardReviewRequest{Scope: "application", ScopeID: canonicalStandardUUID(appID), StandardID: "00000000-0000-0000-0000-000000000000"}
	snapshot, err := lockStandardReviewInputs(ctx, tx, orgID, actorID, r)
	if err != nil {
		return snapshot, time.Time{}, err
	}
	authority, err := q.ReadApplicationStandardLocalIntentAuthority(ctx, tx, sqlc.ReadApplicationStandardLocalIntentAuthorityParams{OrgID: mustPgUUID(orgID), ActorID: mustPgUUID(actorID)})
	if err != nil {
		return snapshot, time.Time{}, err
	}
	if err := authorizeStandardApproval(standardReviewSnapshot{OrgStatus: authority.Status, DeletedPending: authority.DeletedPending, ActorAuthorized: authority.ActorAuthorized}); err != nil {
		return snapshot, time.Time{}, err
	}
	if !snapshot.ScopeOwned || len(snapshot.Applications) != 1 {
		return snapshot, time.Time{}, ErrNotFound
	}
	active, err := q.HasApplicationStandardLocalIntentOperation(ctx, tx, mustPgUUID(appID))
	if err != nil {
		return snapshot, time.Time{}, err
	}
	if active {
		return snapshot, time.Time{}, ErrApplicationStandardOperationInProgress
	}
	return snapshot, authority.StorageTime.Time, nil
}

func persistStandardLocalIntent(ctx context.Context, tx pgx.Tx, before, after ApplicationStandardEnrollment, proposed ApplicationStandardReviewedApp, actorID string) error {
	q := sqlc.New()
	local, err := json.Marshal(after.LocalSettings)
	if err != nil {
		return err
	}
	now := pgtype.Timestamptz{Time: after.UpdatedAt, Valid: true}
	count, err := q.SaveApplicationStandardLocalIntent(ctx, tx, sqlc.SaveApplicationStandardLocalIntentParams{OrgID: mustPgUUID(after.OrgID), AppID: mustPgUUID(after.AppID), ExpectedRevision: before.DesiredRevision, LocalSettings: local, Additional: standardPgUUIDs(after.AdditionalLogDestinations), Now: now})
	if err != nil {
		return fmt.Errorf("write local application intent: %w", err)
	}
	if count != 1 {
		return ErrApplicationStandardLocalIntentStale
	}
	audit := standardLocalIntentAudit(before, after, proposed, actorID)
	if err := q.InsertApplicationStandardOperationAudit(ctx, tx, sqlc.InsertApplicationStandardOperationAuditParams{ID: pgtype.UUID{Bytes: audit.ID, Valid: true}, Kind: audit.Kind, Now: now, Data: audit.Data}); err != nil {
		return fmt.Errorf("audit local application intent: %w", err)
	}
	return nil
}
