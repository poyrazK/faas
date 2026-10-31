package state

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ EnvironmentQualificationObjectStorageStore = (*PgStore)(nil)

func (s *PgStore) PendingEnvironmentQualificationObjectStorageChecks(ctx context.Context, graphID string) ([]EnvironmentQualificationObjectStorageCheck, error) {
	if _, err := uuid.Parse(graphID); err != nil {
		return nil, ErrInvalidArgument
	}
	rows, err := sqlc.New().PendingEnvironmentQualificationObjectStorageChecks(ctx, s.pool, mustPgUUID(graphID))
	if err != nil {
		return nil, mapErr(err)
	}
	checks := make([]EnvironmentQualificationObjectStorageCheck, 0, len(rows))
	for _, row := range rows {
		var secrets []EnvironmentQualificationObjectStorageSecret
		if err := json.Unmarshal(row.Secrets, &secrets); err != nil {
			return nil, err
		}
		checks = append(checks, EnvironmentQualificationObjectStorageCheck{RequestID: row.RequestID, Attempt: row.Attempt,
			AccountID: row.AccountID, AppID: row.AppID, Scope: row.Scope, CredentialID: row.CredentialID, Prefix: row.Prefix,
			Permission: row.Permission, Mode: candidateObjectStorageProbeMode(row.Required, row.Permission), Secrets: secrets})
	}
	return checks, nil
}

func (s *PgStore) RecordEnvironmentQualificationObjectStorageReceipt(ctx context.Context, c EnvironmentQualificationObjectStorageCheck) error {
	if !validObjectStorageQualificationCheck(c) {
		return ErrInvalidArgument
	}
	deliveries, err := json.Marshal(c.Deliveries())
	if err != nil {
		return err
	}
	// The insert guard rechecks the lease, credential and exact deliveries; a
	// change maps to ErrBindingPromotionChanged and records nothing.
	return mapErr(sqlc.New().RecordEnvironmentQualificationObjectStorageReceipt(ctx, s.pool, sqlc.RecordEnvironmentQualificationObjectStorageReceiptParams{
		RequestID: mustPgUUID(c.RequestID), Attempt: c.Attempt, CredentialID: mustPgUUID(c.CredentialID), Prefix: c.Prefix,
		Permission: c.Permission, Mode: c.Mode, Deliveries: deliveries}))
}

// candidateObjectStorageDeadlineTx evaluates environment_binding_object_storage_deadline,
// the SQL the deferred activation guard uses. Zero means no object storage.
func candidateObjectStorageDeadlineTx(ctx context.Context, tx sqlc.DBTX, request EnvironmentWorkloadQualificationRequest, restoredInstanceID string, age time.Duration) (time.Time, error) {
	requirements := request.BindingContext.Policy.ObjectStorageRequirements
	if requirements == nil {
		requirements = map[string]string{}
	}
	body, err := json.Marshal(requirements)
	if err != nil {
		return time.Time{}, err
	}
	deadline, err := sqlc.New().EnvironmentQualificationObjectStorageDeadline(ctx, tx, sqlc.EnvironmentQualificationObjectStorageDeadlineParams{
		Scope: request.FrozenInputs.Scope, Requirements: body, MaxAgeSeconds: int64(age / time.Second),
		RestoredInstanceID: mustPgUUID(restoredInstanceID), RequestID: mustPgUUID(request.ID)})
	if err != nil {
		return time.Time{}, mapErr(err)
	}
	return candidateDeadlineFromSQL(deadline)
}

// candidateDeadlineFromSQL maps a receipt helper result: NULL blocks,
// infinity means the candidate has no binding of that kind.
func candidateDeadlineFromSQL(deadline pgtype.Timestamptz) (time.Time, error) {
	switch {
	case !deadline.Valid:
		return time.Time{}, ErrBindingReleaseRequired
	case deadline.InfinityModifier == pgtype.Infinity:
		return time.Time{}, nil
	case deadline.InfinityModifier != pgtype.Finite:
		return time.Time{}, ErrBindingReleaseRequired
	}
	return deadline.Time, nil
}
