package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ EnvironmentQualificationConfigReceiptStore = (*PgStore)(nil)

func qualificationConfigReceiptFromSQL(row sqlc.EnvironmentQualificationConfigReceipt) EnvironmentQualificationConfigReceipt {
	receipt := EnvironmentQualificationConfigReceipt{
		RequestID: pgUUIDString(row.RequestID), Attempt: row.Attempt, GraphID: pgUUIDString(row.GraphID),
		InstanceID: pgUUIDString(row.InstanceID), APIEnvSHA256: row.ApiEnvSha256, RecordedAt: row.RecordedAt.Time,
	}
	if row.CaptureInstanceID.Valid {
		receipt.CaptureInstanceID = pgUUIDString(row.CaptureInstanceID)
	}
	return receipt
}

func (s *PgStore) EnvironmentQualificationConfigReceipt(ctx context.Context, instanceID string) (EnvironmentQualificationConfigReceipt, error) {
	if !qualificationRecoveryUUIDValid(instanceID) {
		return EnvironmentQualificationConfigReceipt{}, ErrInvalidArgument
	}
	row, err := sqlc.New().EnvironmentQualificationConfigReceipt(ctx, s.pool, mustPgUUID(instanceID))
	if err != nil {
		return EnvironmentQualificationConfigReceipt{}, mapErr(err)
	}
	return qualificationConfigReceiptFromSQL(row), nil
}

func (s *PgStore) RecordEnvironmentQualificationConfigReceipt(ctx context.Context, claimed EnvironmentWorkloadQualificationRequest,
	frame EnvironmentQualificationExecution, apiEnvSHA256 string) (EnvironmentQualificationConfigReceipt, error) {
	var zero EnvironmentQualificationConfigReceipt
	if !qualificationRecoveryUUIDValid(claimed.ID) || !qualificationRecoveryUUIDValid(claimed.GraphID) ||
		!qualificationRecoveryUUIDValid(frame.InstanceID) || claimed.Attempt < 1 || !qualificationConfigDigestValid(apiEnvSHA256) ||
		frame.CaptureInstanceID != "" && frame.CaptureInstanceID != claimed.ReservedInstanceID {
		return zero, ErrInvalidArgument
	}
	q := sqlc.New()
	prior, err := q.EnvironmentQualificationConfigReceipt(ctx, s.pool, mustPgUUID(frame.InstanceID))
	if err == nil {
		receipt := qualificationConfigReceiptFromSQL(prior)
		if !qualificationConfigReceiptMatchesFrame(receipt, claimed, frame) || receipt.APIEnvSHA256 != apiEnvSHA256 {
			return zero, ErrConflict
		}
		return receipt, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return zero, mapErr(err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return zero, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	row, err := s.qualificationCurrentTx(ctx, tx, claimed.ID)
	if err != nil {
		return zero, err
	}
	current := qualificationRequestFromSQL(row)
	if !qualificationClaimIdentityMatches(current, claimed) || !qualificationLeaseMatches(current, claimed, time.Now()) ||
		current.Attempt != frame.Attempt || frame.RequestID != current.ID || frame.GraphID != current.GraphID ||
		frame.AppID != current.AppID || frame.DeploymentID != current.DeploymentID || frame.Resource != current.Resource {
		return zero, ErrConflict
	}
	if _, err := q.SetEnvironmentWorkloadQualificationContext(ctx, tx, current.LeaseToken); err != nil {
		return zero, mapErr(err)
	}
	executionRow, err := q.LockEnvironmentQualificationExecution(ctx, tx, mustPgUUID(frame.InstanceID))
	if err != nil {
		return zero, mapErr(err)
	}
	status, err := qualificationExecutionFromSQL(executionRow)
	if err != nil {
		return zero, err
	}
	if status.Execution != frame || !status.DispatchStarted || status.RetiredAt != nil {
		return zero, ErrConflict
	}
	instanceRow, err := q.LockEnvironmentQualificationRuntimeInstance(ctx, tx, mustPgUUID(frame.InstanceID))
	if err != nil {
		return zero, mapErr(err)
	}
	instance := qualificationInstanceFromSQL(instanceRow)
	if instance.State != string(StateRunning) || instance.AppID != current.AppID || instance.DeploymentID != current.DeploymentID ||
		instance.NodeID != frame.NodeID || instance.WakeID != frame.WakeID {
		return zero, ErrConflict
	}
	config, err := q.InstanceRuntimeConfigReceipt(ctx, tx, mustPgUUID(frame.InstanceID))
	if err != nil {
		return zero, mapErr(err)
	}
	inputs, err := runtimeConfigInputsFromSQL(config.Scope, config.BoundaryAt, config.Variables, config.SecretVersions,
		config.SecretRefs, config.SidecarSecretVersions, config.AllSecrets)
	if err != nil || pgUUIDString(config.WakeID) != frame.WakeID {
		return zero, ErrConflict
	}
	fresh, err := readRuntimeConfigInputsFresh(ctx, tx, current.AppID, inputs)
	if err != nil {
		return zero, err
	}
	if !fresh {
		return zero, ErrConflict
	}
	var captureInstanceID pgtype.UUID
	if frame.CaptureInstanceID != "" {
		reservation, reservationErr := q.EnvironmentQualificationRestoreReservation(ctx, tx, sqlc.EnvironmentQualificationRestoreReservationParams{
			RequestID: mustPgUUID(current.ID), Attempt: current.Attempt,
		})
		if reservationErr != nil || pgUUIDString(reservation.InstanceID) != frame.InstanceID ||
			pgUUIDString(reservation.CaptureInstanceID) != frame.CaptureInstanceID {
			return zero, ErrConflict
		}
		captureInstanceID = mustPgUUID(frame.CaptureInstanceID)
	}
	stored, err := q.InsertEnvironmentQualificationConfigReceipt(ctx, tx, sqlc.InsertEnvironmentQualificationConfigReceiptParams{
		RequestID: mustPgUUID(current.ID), Attempt: current.Attempt, GraphID: mustPgUUID(current.GraphID),
		InstanceID: mustPgUUID(frame.InstanceID), CaptureInstanceID: captureInstanceID, ApiEnvSha256: apiEnvSHA256,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		stored, err = q.EnvironmentQualificationConfigReceipt(ctx, tx, mustPgUUID(frame.InstanceID))
	}
	if err != nil {
		return zero, mapErr(err)
	}
	receipt := qualificationConfigReceiptFromSQL(stored)
	if !qualificationConfigReceiptMatchesFrame(receipt, claimed, frame) || receipt.APIEnvSHA256 != apiEnvSHA256 {
		return zero, ErrConflict
	}
	if err := tx.Commit(ctx); err != nil {
		return zero, err
	}
	return receipt, nil
}
