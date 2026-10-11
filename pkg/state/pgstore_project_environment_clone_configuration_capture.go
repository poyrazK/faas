package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Readers authenticate a marked operation against its root before returning
// any captured workload to a provider worker or publication/materialization.
// Missing roots never downgrade a marked operation to the legacy contract.
func verifyCloneConfigurationCaptureDB(ctx context.Context, db sqlc.DBTX, accountID, projectID, operationID string, records []projectCloneWorkloadRecord) (ProjectEnvironmentCloneConfigurationCapture, error) {
	q := new(sqlc.Queries)
	r, err := q.ReadProjectEnvironmentCloneConfigurationCaptureIdentity(ctx, db, sqlc.ReadProjectEnvironmentCloneConfigurationCaptureIdentityParams{
		AccountID: mustPgUUID(accountID), ProjectID: mustPgUUID(projectID), OperationID: mustPgUUID(operationID)})
	if err != nil {
		return ProjectEnvironmentCloneConfigurationCapture{}, mapErr(err)
	}
	if r.ConfigurationCaptureVersion == 0 {
		if r.HasCapture {
			return ProjectEnvironmentCloneConfigurationCapture{}, ErrConflict
		}
		return ProjectEnvironmentCloneConfigurationCapture{}, nil
	}
	stored, err := q.LockProjectEnvironmentCloneConfigurationCapture(ctx, db, mustPgUUID(operationID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			err = ErrConflict
		}
		return ProjectEnvironmentCloneConfigurationCapture{}, mapErr(err)
	}
	op := ProjectEnvironmentCloneOperation{ID: operationID, AccountID: accountID, ProjectID: projectID, SourceEnvironment: r.SourceEnvironment, SourceReleaseSetID: r.SourceReleaseSetID}
	captured, _, err := cloneConfigurationRoot(op, records)
	storedHash := sha256.Sum256(stored.Configuration)
	if err != nil || r.ConfigurationCaptureVersion != 1 || stored.Version != 1 ||
		captured.Hash != r.SourceRevisionHash || captured.Hash != stored.ConfigurationHash || captured.Hash != hex.EncodeToString(storedHash[:]) {
		return ProjectEnvironmentCloneConfigurationCapture{}, ErrConflict
	}
	return captured, nil
}

func (s *PgStore) CreateCapturedProjectEnvironmentCloneOperation(ctx context.Context, request ProjectEnvironmentCloneCaptureRequest) (ProjectEnvironmentCloneOperation, error) {
	if err := validateCloneCaptureRequest(request); err != nil {
		return ProjectEnvironmentCloneOperation{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return ProjectEnvironmentCloneOperation{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := new(sqlc.Queries)
	if _, err := q.LockProjectEnvironmentCloneProject(ctx, tx, sqlc.LockProjectEnvironmentCloneProjectParams{AccountID: mustPgUUID(request.AccountID), ProjectID: mustPgUUID(request.ProjectID)}); err != nil {
		return ProjectEnvironmentCloneOperation{}, mapProjectCloneSnapshotErr(mapErr(err))
	}
	existingID, err := q.ReadProjectEnvironmentCloneOperationIDByKey(ctx, tx, sqlc.ReadProjectEnvironmentCloneOperationIDByKeyParams{
		AccountID: mustPgUUID(request.AccountID), ProjectID: mustPgUUID(request.ProjectID), IdempotencyKey: request.IdempotencyKey})
	if err == nil {
		existing, err := cloneWorkerOperationDB(ctx, tx, request.AccountID, request.ProjectID, pgUUIDString(existingID))
		if err != nil {
			return existing, err
		}
		if existing.SourceEnvironment != request.SourceEnvironment || existing.TargetEnvironment != request.TargetEnvironment {
			return ProjectEnvironmentCloneOperation{}, ErrConflict
		}
		records, err := cloneWorkloadRecordsDB(ctx, tx, existing.AccountID, existing.ProjectID, existing.ID)
		if err != nil {
			return ProjectEnvironmentCloneOperation{}, err
		}
		capture, err := verifyCloneConfigurationCaptureDB(ctx, tx, existing.AccountID, existing.ProjectID, existing.ID, records)
		if err != nil || capture.Version != 1 {
			if err == nil {
				err = ErrConflict
			}
			return ProjectEnvironmentCloneOperation{}, err
		}
		return existing, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return ProjectEnvironmentCloneOperation{}, mapErr(err)
	}
	coverage, err := readCloneSchemaCoverageDB(ctx, tx)
	if err != nil {
		return ProjectEnvironmentCloneOperation{}, err
	}
	if err := requireKnownCloneSchema(coverage); err != nil {
		return ProjectEnvironmentCloneOperation{}, err
	}
	presence, err := q.ReadProjectEnvironmentCloneEnvironmentPresence(ctx, tx, sqlc.ReadProjectEnvironmentCloneEnvironmentPresenceParams{
		ProjectID: mustPgUUID(request.ProjectID), SourceEnvironment: request.SourceEnvironment, TargetEnvironment: request.TargetEnvironment})
	if err != nil {
		return ProjectEnvironmentCloneOperation{}, mapErr(err)
	}
	if !presence.SourceExists {
		return ProjectEnvironmentCloneOperation{}, ErrNotFound
	}
	if presence.TargetExists {
		return ProjectEnvironmentCloneOperation{}, ErrConflict
	}
	op := ProjectEnvironmentCloneOperation{ID: uuid.NewString(), AccountID: request.AccountID, ProjectID: request.ProjectID, SourceEnvironment: request.SourceEnvironment,
		TargetEnvironment: request.TargetEnvironment, IdempotencyKey: request.IdempotencyKey}
	op.SourceReleaseSetID, err = q.ReadProjectEnvironmentCloneSourceRelease(ctx, tx, sqlc.ReadProjectEnvironmentCloneSourceReleaseParams{ProjectID: mustPgUUID(op.ProjectID), Environment: op.SourceEnvironment})
	if err != nil {
		return op, mapErr(err)
	}
	records, err := captureCloneConfigurationWorkloadsTx(ctx, tx, op)
	if err != nil {
		return ProjectEnvironmentCloneOperation{}, mapProjectCloneSnapshotErr(err)
	}
	capture, raw, err := cloneConfigurationRoot(op, records)
	if err != nil {
		return ProjectEnvironmentCloneOperation{}, err
	}
	op.SourceRevisionHash = capture.Hash
	var releaseID pgtype.UUID
	if op.SourceReleaseSetID != "" {
		releaseID = mustPgUUID(op.SourceReleaseSetID)
	}
	if err := q.CreateCapturedProjectEnvironmentCloneOperation(ctx, tx, sqlc.CreateCapturedProjectEnvironmentCloneOperationParams{
		ID: mustPgUUID(op.ID), AccountID: mustPgUUID(op.AccountID), ProjectID: mustPgUUID(op.ProjectID), SourceEnvironment: op.SourceEnvironment,
		TargetEnvironment: op.TargetEnvironment, IdempotencyKey: op.IdempotencyKey, SourceRevisionHash: op.SourceRevisionHash, SourceReleaseSetID: releaseID}); err != nil {
		return ProjectEnvironmentCloneOperation{}, mapProjectCloneSnapshotErr(mapErr(err))
	}
	if err := insertCapturedCloneConfigurationTx(ctx, tx, op, records, capture, raw); err != nil {
		return ProjectEnvironmentCloneOperation{}, mapProjectCloneSnapshotErr(err)
	}
	created, err := cloneWorkerOperationDB(ctx, tx, op.AccountID, op.ProjectID, op.ID)
	if err != nil {
		return ProjectEnvironmentCloneOperation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ProjectEnvironmentCloneOperation{}, mapProjectCloneSnapshotErr(err)
	}
	return created, nil
}

func captureCloneConfigurationWorkloadsTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) ([]projectCloneWorkloadRecord, error) {
	if err := lockProjectEnvironmentCloneSource(ctx, tx, ProjectEnvironmentClone{AccountID: op.AccountID, ProjectID: op.ProjectID, SourceSlug: op.SourceEnvironment}); err != nil {
		return nil, err
	}
	scopesRaw, err := projectCloneValueScopesTx(ctx, tx, ProjectEnvironmentClone{AccountID: op.AccountID, ProjectID: op.ProjectID, SourceSlug: op.SourceEnvironment})
	if err != nil {
		return nil, err
	}
	return captureCloneConfigurationWorkloadsForScopesTx(ctx, tx, op, scopesRaw, true)
}

func captureCloneConfigurationWorkloadsForScopesTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, scopesRaw []byte, lockFlags bool) ([]projectCloneWorkloadRecord, error) {
	var scopes map[string]string
	if json.Unmarshal(scopesRaw, &scopes) != nil || len(scopes) == 0 {
		return nil, ErrProjectEnvironmentCloneBindingCaptureUnavailable
	}
	records := make([]projectCloneWorkloadRecord, 0, len(scopes))
	for appID, scope := range scopes {
		snapshot, err := captureCloneWorkloadDB(ctx, tx, op, appID, scope, lockFlags)
		if err != nil {
			return nil, fmt.Errorf("capture clone workload %q: %w", appID, err)
		}
		work, err := captureCloneWorkPoliciesTx(ctx, tx, op, appID, scope)
		if err != nil {
			return nil, err
		}
		if snapshot.Policies == nil {
			return nil, ErrProjectEnvironmentClonePolicyCaptureUnavailable
		}
		if snapshot.Settings.WorkPolicies != nil {
			work.Policies = snapshot.Settings.WorkPolicies.Policies
			work, err = normalizeCloneWorkPolicyDefinitions(work)
			if err != nil {
				return nil, fmt.Errorf("clone workload %q producer bindings have no scoped policy definition: %w", snapshot.WorkloadSlug, ErrProjectEnvironmentCloneWorkPolicyIsolationUnavailable)
			}
		}
		policies, err := normalizeEnvironmentWorkPolicies(work.Policies)
		if err != nil {
			return nil, err
		}
		if snapshot.Settings.WorkPolicies == nil {
			clock := int64(1)
			for _, policy := range policies {
				if policy.Revision > clock {
					clock = policy.Revision
				}
			}
			snapshot.Settings.WorkPolicies = &ProjectEnvironmentWorkPolicySettings{Revision: clock, Policies: policies}
		}
		snapshot.Policies.Work = &work
		if err := captureCloneQueuesTx(ctx, tx, op, &snapshot); err != nil {
			return nil, err
		}
		raw, hash, err := encodeCloneWorkloadSnapshot(snapshot)
		if err != nil {
			return nil, err
		}
		record, err := decodeCloneWorkloadRecord(op.ID, appID, snapshot.Artifact.ID, hash, "", "", raw)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

func insertCapturedCloneConfigurationTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, records []projectCloneWorkloadRecord, capture ProjectEnvironmentCloneConfigurationCapture, root []byte) error {
	q := new(sqlc.Queries)
	for _, record := range records {
		raw, hash, err := encodeCloneWorkloadSnapshot(record.snapshot)
		if err != nil {
			return err
		}
		if err := q.InsertProjectEnvironmentCloneWorkload(ctx, tx, sqlc.InsertProjectEnvironmentCloneWorkloadParams{OperationID: mustPgUUID(op.ID), AppID: mustPgUUID(record.AppID),
			SourceDeploymentID: mustPgUUID(record.SourceDeploymentID), SourceHash: hash, Snapshot: raw}); err != nil {
			return mapErr(err)
		}
	}
	if err := pinCloneLayerArtifactsTx(ctx, tx, records); err != nil {
		return err
	}
	return q.InsertProjectEnvironmentCloneConfigurationCapture(ctx, tx, sqlc.InsertProjectEnvironmentCloneConfigurationCaptureParams{OperationID: mustPgUUID(op.ID), ConfigurationHash: capture.Hash, Configuration: root})
}

func (s *PgStore) ProjectEnvironmentCloneConfigurationForLease(ctx context.Context, lease ProjectEnvironmentCloneLease) (ProjectEnvironmentCloneConfigurationCapture, error) {
	if !validCloneLeaseIdentity(lease) {
		return ProjectEnvironmentCloneConfigurationCapture{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectEnvironmentCloneConfigurationCapture{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	op, err := lockCloneWorkloadOperationTx(ctx, tx, lease.Operation.AccountID, lease.Operation.ProjectID, lease.Operation.ID)
	if err != nil {
		return ProjectEnvironmentCloneConfigurationCapture{}, err
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, op, &lease); err != nil {
		return ProjectEnvironmentCloneConfigurationCapture{}, err
	}
	records, err := cloneWorkloadRecordsDB(ctx, tx, op.AccountID, op.ProjectID, op.ID)
	if err != nil {
		return ProjectEnvironmentCloneConfigurationCapture{}, err
	}
	capture, err := verifyCloneConfigurationCaptureDB(ctx, tx, op.AccountID, op.ProjectID, op.ID, records)
	if err != nil {
		return capture, err
	}
	if capture.Version != 1 {
		return capture, ErrProjectEnvironmentCloneBindingCaptureUnavailable
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, op, &lease); err != nil {
		return capture, err
	}
	return capture, tx.Commit(ctx)
}

func (s *PgStore) ValidateProjectEnvironmentCloneSourceConfigurationForLease(ctx context.Context, lease ProjectEnvironmentCloneLease) (ProjectEnvironmentCloneConfigurationCapture, error) {
	var zero ProjectEnvironmentCloneConfigurationCapture
	if !validCloneLeaseIdentity(lease) {
		return zero, ErrInvalidArgument
	}
	ctx, cancel := context.WithDeadline(ctx, lease.ExpiresAt)
	defer cancel()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return zero, mapProjectCloneSnapshotErr(err)
	}
	// pgx discards a connection if bounded rollback cannot finish.
	defer func() { _ = tx.Rollback(ctx) }()
	op, err := lockCloneWorkloadOperationTx(ctx, tx, lease.Operation.AccountID, lease.Operation.ProjectID, lease.Operation.ID)
	if err != nil {
		return zero, mapProjectCloneSnapshotErr(err)
	}
	if op.Status != CloneOperationCapturing {
		return zero, ErrConflict
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, op, &lease); err != nil {
		return zero, err
	}
	records, err := cloneWorkloadRecordsDB(ctx, tx, op.AccountID, op.ProjectID, op.ID)
	if err != nil {
		return zero, err
	}
	capture, err := verifyCloneConfigurationCaptureDB(ctx, tx, op.AccountID, op.ProjectID, op.ID, records)
	if err != nil {
		return zero, err
	}
	if capture.Version != 1 {
		return zero, ErrProjectEnvironmentCloneBindingCaptureUnavailable
	}
	coverage, err := readCloneSchemaCoverageDB(ctx, tx)
	if err != nil {
		return zero, err
	}
	if err := requireKnownCloneSchema(coverage); err != nil {
		return zero, err
	}
	// Select the live release, rather than reading the retained release again.
	// The capture readers below also include values, secrets, flags, settings,
	// policies, bindings, artifacts and the complete live workload roster.
	op.SourceReleaseSetID, err = sqlc.New().ReadProjectEnvironmentCloneSourceRelease(ctx, tx, sqlc.ReadProjectEnvironmentCloneSourceReleaseParams{
		ProjectID: mustPgUUID(op.ProjectID), Environment: op.SourceEnvironment})
	if err != nil {
		return zero, mapErr(err)
	}
	liveRecords, err := captureCloneConfigurationWorkloadsTx(ctx, tx, op)
	if err != nil {
		return zero, mapProjectCloneSnapshotErr(err)
	}
	live, _, err := cloneConfigurationRoot(op, liveRecords)
	if err != nil {
		return zero, err
	}
	if live != capture {
		return zero, ErrConflict
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, op, &lease); err != nil {
		return zero, err
	}
	if err := tx.Commit(ctx); err != nil {
		return zero, mapProjectCloneSnapshotErr(err)
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	return capture, nil
}
