package state

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ApplicationStandardObservationStore = (*PgStore)(nil)

func (s *PgStore) ObserveApplicationStandardOperation(ctx context.Context, c ApplicationStandardWorkerClaim) (ApplicationStandardOperation, error) {
	if !standardWorkerClaimValid(c) {
		return ApplicationStandardOperation{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ApplicationStandardOperation{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := sqlc.New().LockApplicationStandardWorkerOperation(ctx, tx, sqlc.LockApplicationStandardWorkerOperationParams{OperationID: mustPgUUID(c.OperationID), OrgID: mustPgUUID(c.OrgID), Owner: c.Owner, Generation: c.Generation}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ApplicationStandardOperation{}, ErrApplicationStandardLeaseLost
		}
		return ApplicationStandardOperation{}, standardRuntimeQualificationReadError(err)
	}
	o, err := readStandardOperation(ctx, tx, c.OrgID, c.OperationID, "")
	if err != nil {
		return o, err
	}
	qualified := map[string]standardApplicationQualification{}
	for _, target := range o.Targets {
		if target.State != "persisted" && target.State != "observed" {
			continue
		}
		q, err := qualifyStandardApplicationTx(ctx, tx, o.OrgID, target.AppID, target.DesiredRevision)
		if err != nil {
			return o, err
		}
		qualified[target.AppID] = q
	}
	if err := restrictStandardObservationClaimTx(ctx, tx, c, qualified); err != nil {
		return o, err
	}
	if err := checkpointStandardObservationsTx(ctx, tx, c, &o, qualified); err != nil {
		return o, err
	}
	return checkpointStandardMaterialization(ctx, tx, c, o)
}

func qualifyStandardApplicationTx(ctx context.Context, tx pgx.Tx, orgID, appID string, revision int64) (standardApplicationQualification, error) {
	locked, err := sqlc.New().LockApplicationStandardObservation(ctx, tx, sqlc.LockApplicationStandardObservationParams{AppID: mustPgUUID(appID), OrgID: mustPgUUID(orgID)})
	if err != nil {
		return standardApplicationQualification{}, standardRuntimeQualificationReadError(err)
	}
	if !locked.Valid {
		return standardApplicationQualification{reason: "application_unavailable"}, nil
	}
	if _, err := sqlc.New().LockApplicationStandardObservationEvidence(ctx, tx, mustPgUUID(appID)); err != nil {
		return standardApplicationQualification{}, standardRuntimeQualificationReadError(err)
	}
	// A stale artifact SQL guard can abort a statement. Roll back its private
	// savepoint while retaining the outer membership/application locks, so the
	// worker may checkpoint a pending reason without granting any observation.
	read, err := tx.Begin(ctx)
	if err != nil {
		return standardApplicationQualification{}, err
	}
	defer func() { _ = read.Rollback(ctx) }()
	q, err := readStandardApplicationQualificationTx(ctx, read, orgID, appID, revision)
	if err != nil || q.reason != "" {
		return q, err
	}
	return q, read.Commit(ctx)
}

func readStandardApplicationQualificationTx(ctx context.Context, tx pgx.Tx, orgID, appID string, revision int64) (standardApplicationQualification, error) {
	r, err := readStandardConsumerRoster(ctx, tx, orgID, appID)
	if err != nil {
		return standardApplicationQualification{}, err
	}
	q := standardApplicationQualification{reason: standardObservationEnrollment(r, revision)}
	if q.reason != "" {
		return q, nil
	}
	q, err = qualifyStandardLoggingTx(ctx, tx, r)
	if err != nil || q.reason != "" {
		return q, err
	}
	for _, row := range r.LiveInstances {
		runtime, err := standardRuntimeQualification(r, row.InstanceID)
		if err != nil {
			return q, err
		}
		if runtime.Reason == "" {
			runtime, err = readStandardRuntimeQualificationTx(ctx, tx, runtime)
		}
		if err != nil || !runtime.Qualified {
			q.reason = runtime.Reason
			return q, err
		}
		q.restrict(runtime.ApprovalExpiresAt)
	}
	return qualifyStandardArtifactsTx(ctx, tx, r, q)
}

func checkpointStandardObservationsTx(ctx context.Context, tx pgx.Tx, c ApplicationStandardWorkerClaim, o *ApplicationStandardOperation, qualified map[string]standardApplicationQualification) error {
	clock, err := sqlc.New().ArtifactEvidenceStorageTime(ctx, tx)
	if err != nil || !clock.Valid {
		return errors.Join(ErrApplicationStandardRuntimeStale, err)
	}
	for i := range o.Targets {
		t := &o.Targets[i]
		qualification, ok := qualified[t.AppID]
		if !ok {
			continue
		}
		standardObservedTarget(t, qualification, clock.Time)
		count, err := sqlc.New().CheckpointApplicationStandardObservation(ctx, tx, sqlc.CheckpointApplicationStandardObservationParams{
			AppID: mustPgUUID(t.AppID), OrgID: mustPgUUID(o.OrgID), Revision: t.DesiredRevision, Qualified: t.State == "observed", ErrorCode: t.ErrorCode,
			EvidenceUntil: standardPgTime(qualification.until), OperationID: mustPgUUID(c.OperationID), Owner: c.Owner, Generation: c.Generation,
		})
		if err != nil {
			return err
		}
		if count != 1 && t.State == "observed" {
			return ErrApplicationStandardRuntimeStale
		}
		if err := checkpointStandardTarget(ctx, tx, o.ID, *t); err != nil {
			return err
		}
	}
	return nil
}
