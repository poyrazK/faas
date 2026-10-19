package state

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ EnvironmentQualificationPostgresStore = (*PgStore)(nil)

func (s *PgStore) PendingEnvironmentQualificationPostgresChecks(ctx context.Context, graphID string) ([]EnvironmentQualificationPostgresCheck, error) {
	if _, err := uuid.Parse(graphID); err != nil {
		return nil, ErrInvalidArgument
	}
	rows, err := sqlc.New().PendingEnvironmentQualificationPostgresChecks(ctx, s.pool, mustPgUUID(graphID))
	if err != nil {
		return nil, mapErr(err)
	}
	checks := make([]EnvironmentQualificationPostgresCheck, 0, len(rows))
	for _, row := range rows {
		checks = append(checks, EnvironmentQualificationPostgresCheck{RequestID: row.RequestID, Attempt: row.Attempt,
			AccountID: row.AccountID, AppID: row.AppID, Scope: row.Scope, BindingID: row.BindingID, DatabaseID: row.DatabaseID,
			EnvironmentKey: row.EnvironmentKey, Access: row.Access, CredentialGeneration: row.CredentialGeneration,
			DeliveryVersion: row.DeliveryVersion, PostgresMajor: int(row.PostgresMajor), CredentialRef: row.CredentialRef,
			Ciphertext: row.Ciphertext, Kid: row.Kid, ValueHash: row.ValueHash})
	}
	return checks, nil
}

func (s *PgStore) RecordEnvironmentQualificationPostgresReceipt(ctx context.Context, c EnvironmentQualificationPostgresCheck) error {
	if !validPostgresQualificationCheck(c) {
		return ErrInvalidArgument
	}
	// The insert guard rechecks the lease, binding and delivery; a changed
	// credential maps to ErrBindingPromotionChanged and records nothing.
	return mapErr(sqlc.New().RecordEnvironmentQualificationPostgresReceipt(ctx, s.pool, sqlc.RecordEnvironmentQualificationPostgresReceiptParams{
		RequestID: mustPgUUID(c.RequestID), Attempt: c.Attempt, BindingID: mustPgUUID(c.BindingID), EnvironmentKey: c.EnvironmentKey,
		Access: c.Access, CredentialGeneration: c.CredentialGeneration, DeliveryVersion: c.DeliveryVersion}))
}

func validPostgresQualificationCheck(c EnvironmentQualificationPostgresCheck) bool {
	_, requestErr := uuid.Parse(c.RequestID)
	_, bindingErr := uuid.Parse(c.BindingID)
	return requestErr == nil && bindingErr == nil && c.Attempt > 0 && c.EnvironmentKey != "" &&
		candidatePostgresAccessSupported(c.Access) && c.CredentialGeneration > 0 && c.DeliveryVersion > 0
}

// candidatePostgresDeadlineTx evaluates environment_binding_postgres_deadline,
// the same SQL the deferred activation guard uses. A zero time means the
// candidate has no PostgreSQL binding.
func candidatePostgresDeadlineTx(ctx context.Context, tx sqlc.DBTX, request EnvironmentWorkloadQualificationRequest, restoredInstanceID string, age time.Duration) (time.Time, error) {
	deadline, err := sqlc.New().EnvironmentQualificationPostgresDeadline(ctx, tx, sqlc.EnvironmentQualificationPostgresDeadlineParams{
		Scope: request.FrozenInputs.Scope, MaxAgeSeconds: int64(age / time.Second), RestoredInstanceID: mustPgUUID(restoredInstanceID), RequestID: mustPgUUID(request.ID)})
	if err != nil {
		return time.Time{}, mapErr(err)
	}
	return candidateDeadlineFromSQL(deadline)
}
