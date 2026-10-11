package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) MaterializeProjectEnvironmentCloneForLease(ctx context.Context, lease ProjectEnvironmentCloneLease, bindingIDs []string, secretCount int, limits api.Limits) (ProjectEnvironment, error) {
	if !validCloneLeaseIdentity(lease) || lease.Operation.Status != CloneOperationCopying || secretCount < 0 || (len(bindingIDs) == 0) != (secretCount == 0) {
		return ProjectEnvironment{}, ErrInvalidArgument
	}
	op := lease.Operation
	clone := ProjectEnvironmentClone{AccountID: op.AccountID, ProjectID: op.ProjectID, SourceSlug: op.SourceEnvironment, TargetSlug: op.TargetEnvironment,
		CloneOperationID: op.ID, CloneOperationRevision: op.Revision, ManagedBindingsPrepared: len(bindingIDs) > 0,
		PreparedManagedBindingIDs: append([]string(nil), bindingIDs...), PreparedManagedSecretCount: secretCount}
	created, _, err := s.cloneProjectEnvironment(ctx, clone, limits, &lease)
	return created, err
}

func requireCloneMaterializationCaptureTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
	records, err := cloneWorkloadRecordsDB(ctx, tx, op.AccountID, op.ProjectID, op.ID)
	if err != nil {
		return err
	}
	capture, err := verifyCloneConfigurationCaptureDB(ctx, tx, op.AccountID, op.ProjectID, op.ID, records)
	if err != nil {
		return err
	}
	if capture.Version != 1 || capture.Hash != op.SourceRevisionHash || capture.WorkloadCount == 0 {
		return ErrProjectEnvironmentCloneBindingCaptureUnavailable
	}
	return nil
}

func cloneMaterializedWorkloadsTx(ctx context.Context, tx pgx.Tx, environmentID string) (map[string]projectCloneMaterializedWorkload, error) {
	rows, err := new(sqlc.Queries).ReadProjectEnvironmentCloneMaterializedWorkloads(ctx, tx, mustPgUUID(environmentID))
	if err != nil {
		return nil, mapErr(err)
	}
	result := make(map[string]projectCloneMaterializedWorkload, len(rows))
	for _, row := range rows {
		var settings ProjectEnvironmentWorkloadSettings
		decoder := json.NewDecoder(bytes.NewReader(row.Settings))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&settings); err != nil {
			return nil, ErrConflict
		}
		hash, err := WorkloadSettingsHash(settings)
		if err != nil || hash != row.ConfigHash {
			return nil, ErrConflict
		}
		result[pgUUIDString(row.AppID)] = projectCloneMaterializedWorkload{SpecID: pgUUIDString(row.SpecID), Hash: hash}
	}
	if len(result) == 0 {
		return nil, ErrConflict
	}
	return result, nil
}

func saveCloneMaterializationTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, environment ProjectEnvironment) error {
	workloads, err := cloneMaterializedWorkloadsTx(ctx, tx, environment.ID)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(workloads)
	if err != nil {
		return err
	}
	return new(sqlc.Queries).InsertProjectEnvironmentCloneMaterialization(ctx, tx, sqlc.InsertProjectEnvironmentCloneMaterializationParams{
		OperationID: mustPgUUID(op.ID), EnvironmentID: mustPgUUID(environment.ID), WorkloadSettings: raw})
}

func replayCloneMaterializationTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) (ProjectEnvironment, error) {
	q := new(sqlc.Queries)
	row, err := q.ReadProjectEnvironmentCloneMaterialization(ctx, tx, mustPgUUID(op.ID))
	if err != nil {
		return ProjectEnvironment{}, mapErr(err)
	}
	env, err := q.LockProjectEnvironmentCloneMaterializedEnvironment(ctx, tx, sqlc.LockProjectEnvironmentCloneMaterializedEnvironmentParams{
		ID: row.EnvironmentID, AccountID: mustPgUUID(op.AccountID), ProjectID: mustPgUUID(op.ProjectID), Slug: op.TargetEnvironment})
	if errors.Is(err, pgx.ErrNoRows) {
		return ProjectEnvironment{}, ErrConflict
	}
	if err != nil {
		return ProjectEnvironment{}, mapErr(err)
	}
	var expected map[string]projectCloneMaterializedWorkload
	if err := json.Unmarshal(row.WorkloadSettings, &expected); err != nil || len(expected) == 0 {
		return ProjectEnvironment{}, ErrConflict
	}
	actual, err := cloneMaterializedWorkloadsTx(ctx, tx, pgUUIDString(env.ID))
	if err != nil {
		return ProjectEnvironment{}, err
	}
	if len(actual) != len(expected) || env.Protected {
		return ProjectEnvironment{}, ErrConflict
	}
	for appID, head := range expected {
		if actual[appID] != head {
			return ProjectEnvironment{}, ErrConflict
		}
	}
	return ProjectEnvironment{ID: pgUUIDString(env.ID), AccountID: op.AccountID, ProjectID: op.ProjectID, Slug: env.Slug,
		Protected: env.Protected, CreatedAt: env.CreatedAt.Time, UpdatedAt: env.UpdatedAt.Time}, nil
}
