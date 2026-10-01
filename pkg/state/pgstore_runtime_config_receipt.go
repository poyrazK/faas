package state

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ RuntimeConfigReceiptStore = (*PgStore)(nil)
var _ RuntimeConfigReceiptStore = (*MemStore)(nil)
var _ RuntimeConfigReceiptPublisher = (*PgStore)(nil)
var _ RuntimeConfigReceiptPublisher = (*MemStore)(nil)

func runtimeConfigInputsJSON(inputs RuntimeConfigInputs) ([]byte, []byte) {
	variables, secrets := []byte(`{}`), []byte(`{}`)
	if inputs.Variables != nil {
		variables, _ = json.Marshal(inputs.Variables)
	}
	if inputs.SecretVersions != nil {
		secrets, _ = json.Marshal(inputs.SecretVersions)
	}
	return variables, secrets
}

func runtimeConfigInputsFromSQL(scope string, boundary pgtype.Timestamptz, variables, secrets []byte, all bool) (RuntimeConfigInputs, error) {
	inputs := RuntimeConfigInputs{Scope: scope, Boundary: boundary.Time, AllSecrets: all}
	if json.Unmarshal(variables, &inputs.Variables) != nil || json.Unmarshal(secrets, &inputs.SecretVersions) != nil || validateRuntimeConfigInputs(inputs) != nil {
		return RuntimeConfigInputs{}, ErrInvalidArgument
	}
	return inputs, nil
}

func readInstanceRuntimeConfigReceipt(ctx context.Context, db sqlc.DBTX, id string) (RuntimeConfigInputs, bool, error) {
	if id == "" {
		return RuntimeConfigInputs{}, false, nil
	}
	row, err := sqlc.New().InstanceRuntimeConfigReceipt(ctx, db, mustPgUUID(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return RuntimeConfigInputs{}, false, nil
	}
	if err != nil {
		return RuntimeConfigInputs{}, false, mapErr(err)
	}
	inputs, err := runtimeConfigInputsFromSQL(row.Scope, row.BoundaryAt, row.Variables, row.SecretVersions, row.AllSecrets)
	return inputs, err == nil, err
}

func (s *PgStore) InstanceRuntimeConfigReceipt(ctx context.Context, id string) (RuntimeConfigInputs, bool, error) {
	return readInstanceRuntimeConfigReceipt(ctx, s.pool, id)
}

func (s *PgStore) SnapshotRuntimeConfigReceipt(ctx context.Context, id string) (RuntimeConfigInputs, bool, error) {
	row, err := sqlc.New().SnapshotRuntimeConfigReceipt(ctx, s.pool, mustPgUUID(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return RuntimeConfigInputs{}, false, nil
	}
	if err != nil {
		return RuntimeConfigInputs{}, false, mapErr(err)
	}
	inputs, err := runtimeConfigInputsFromSQL(row.Scope, row.BoundaryAt, row.Variables, row.SecretVersions, row.AllSecrets)
	return inputs, err == nil, err
}

func (s *PgStore) RecordInstanceRuntimeConfigReceipt(ctx context.Context, id, wakeID string, inputs RuntimeConfigInputs) error {
	if err := validateRuntimeConfigInputs(inputs); err != nil {
		return err
	}
	return recordInstanceRuntimeConfigReceipt(ctx, s.pool, id, wakeID, inputs)
}

func recordInstanceRuntimeConfigReceipt(ctx context.Context, db sqlc.DBTX, id, wakeID string, inputs RuntimeConfigInputs) error {
	variables, secrets := runtimeConfigInputsJSON(inputs)
	count, err := sqlc.New().RecordInstanceRuntimeConfigReceipt(ctx, db, sqlc.RecordInstanceRuntimeConfigReceiptParams{
		InstanceID: mustPgUUID(id), WakeID: mustPgUUID(wakeID), Scope: inputs.Scope, BoundaryAt: gitOpsTime(inputs.Boundary),
		Variables: variables, SecretVersions: secrets, AllSecrets: inputs.AllSecrets,
	})
	if err != nil {
		return mapErr(err)
	}
	if count != 1 {
		return ErrConflict
	}
	return nil
}

func (s *PgStore) PublishInstanceRuntimeWithConfig(ctx context.Context, id, expectedState, netns, hostIP string, uid int, wakeID string, inputs RuntimeConfigInputs) (Instance, error) {
	if err := validateRuntimeConfigInputs(inputs); err != nil {
		return Instance{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Instance{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	row, err := sqlc.New().PublishInstanceRuntimeConfig(ctx, tx, sqlc.PublishInstanceRuntimeConfigParams{
		InstanceID: mustPgUUID(id), ExpectedState: expectedState, Netns: pgtype.Text{String: netns, Valid: true},
		HostIp: hostIP, GuestUid: pgtype.Int4{Int32: int32(uid), Valid: true}, WakeID: mustPgUUID(wakeID), Scope: inputs.Scope,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Instance{}, ErrConflict
	}
	if err != nil {
		return Instance{}, mapErr(err)
	}
	if err := recordInstanceRuntimeConfigReceipt(ctx, tx, id, wakeID, inputs); err != nil {
		return Instance{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Instance{}, err
	}
	instance := Instance{ID: pgUUIDString(row.ID), AppID: pgUUIDString(row.AppID), DeploymentID: pgUUIDString(row.DeploymentID),
		State: row.State, Netns: row.Netns.String, GuestUID: int(row.GuestUid.Int32), RAMMB: int(row.RamMb),
		StartedAt: row.StartedAt.Time, LastRequestAt: row.LastRequestAt.Time, ParkedAt: row.ParkedAt.Time,
		NodeID: pgUUIDString(row.NodeID), WakeID: pgUUIDString(row.WakeID), TailCount: int(row.TailCount), Mode: row.Mode, RequestCount: row.RequestCount}
	if row.HostIp != nil {
		instance.HostIP = row.HostIp.String()
	}
	if row.FrameworkReadyAt.Valid {
		instance.FrameworkReadyAt = &row.FrameworkReadyAt.Time
	}
	return instance, nil
}

func readRuntimeConfigInputsFresh(ctx context.Context, db sqlc.DBTX, appID string, inputs RuntimeConfigInputs) (bool, error) {
	variables, secrets := runtimeConfigInputsJSON(inputs)
	fresh, err := sqlc.New().RuntimeConfigInputsFresh(ctx, db, sqlc.RuntimeConfigInputsFreshParams{
		AppID: mustPgUUID(appID), Scope: inputs.Scope, BoundaryAt: gitOpsTime(inputs.Boundary),
		Variables: variables, SecretVersions: secrets, AllSecrets: inputs.AllSecrets,
	})
	return fresh, mapErr(err)
}

func (s *PgStore) RuntimeConfigInputsFresh(ctx context.Context, appID string, inputs RuntimeConfigInputs) (bool, error) {
	if err := validateRuntimeConfigInputs(inputs); err != nil {
		return false, err
	}
	return readRuntimeConfigInputsFresh(ctx, s.pool, appID, inputs)
}

func (s *PgStore) RuntimeConfigReceiptRequired(ctx context.Context, appID, scope string) (bool, error) {
	required, err := sqlc.New().RuntimeConfigReceiptRequired(ctx, s.pool, sqlc.RuntimeConfigReceiptRequiredParams{
		AppID: mustPgUUID(appID), Scope: normalizedDeploymentScope(scope),
	})
	return required, mapErr(err)
}
