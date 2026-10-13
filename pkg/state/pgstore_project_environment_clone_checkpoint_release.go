package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ProjectEnvironmentCloneCheckpointReleaseStore = (*PgStore)(nil)

func cloneCheckpointReleaseFromSQL(row sqlc.ProjectEnvironmentCloneCheckpointRelease) (ProjectEnvironmentCloneCheckpointRelease, error) {
	in := ProjectEnvironmentCloneCheckpointRelease{AdoptionSHA256: row.AdoptionSha256, State: row.State, RequestedAt: row.RequestedAt.Time.UTC(), ObservationSHA256: row.ObservationSha256.String}
	if json.Unmarshal(row.Plan, &in.Plan) != nil || json.Unmarshal(row.Receipt, &in.Receipt) != nil {
		return in, ErrConflict
	}
	return in, nil
}

func (s *PgStore) checkpointReleaseTx(ctx context.Context, lease ProjectEnvironmentCloneLease, work func(context.Context, pgx.Tx, ProjectEnvironmentCloneOperation) error) error {
	if !validCloneLeaseIdentity(lease) {
		return ErrInvalidArgument
	}
	ctx, cancel := context.WithDeadline(ctx, lease.ExpiresAt)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	op, err := lockCloneWorkloadOperationTx(ctx, tx, lease.Operation.AccountID, lease.Operation.ProjectID, lease.Operation.ID)
	if err != nil {
		return err
	}
	if op.Status != CloneOperationCapturing {
		return ErrConflict
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, op, &lease); err != nil {
		return err
	}
	if err := work(ctx, tx, op); err != nil {
		return err
	}
	// Even read-only recovery must reject an expired or replaced worker.
	if err := authorizeCloneObjectMutationTx(ctx, tx, op, &lease); err != nil {
		return err
	}
	return mapErr(tx.Commit(ctx))
}

func readCloneCheckpointReleaseTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) (ProjectEnvironmentCloneCheckpointRelease, error) {
	row, err := sqlc.New().ReadCloneCheckpointRelease(ctx, tx, mustPgUUID(op.ID))
	if err != nil {
		return ProjectEnvironmentCloneCheckpointRelease{}, mapErr(err)
	}
	return cloneCheckpointReleaseFromSQL(row)
}

// Reconstruct the original adoption from retained local journals, never from
// current source configuration. Missing or modified captures cannot be repaired.
func validateCloneCheckpointReleaseJournalsTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, in ProjectEnvironmentCloneCheckpointRelease, requireHolds bool) error {
	return validateCloneCheckpointReleaseJournalsWithRetiredTargetsTx(ctx, tx, op, in, requireHolds, false)
}

// Only compensation can authenticate original placement metadata after target
// retirement. This permits no new capture, creation, copy or publication.
func validateCloneCheckpointReleaseJournalsWithRetiredTargetsTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, in ProjectEnvironmentCloneCheckpointRelease, requireHolds, allowRetired bool) error {
	return validateCloneCheckpointAdoptionJournalsTx(ctx, tx, op, in, requireHolds, allowRetired, false)
}

// Incomplete-release authentication is restricted to unused local reservation
// compensation. It grants no native retirement or capturing write authority.
func validateCloneCheckpointAdoptionJournalsTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, in ProjectEnvironmentCloneCheckpointRelease, requireHolds, allowRetired, allowIncomplete bool) error {
	if allowIncomplete && (!allowRetired || op.Status != CloneOperationCompensating || requireHolds) {
		return ErrConflict
	}
	if allowRetired && (op.Status != CloneOperationCompensating || requireHolds || !allowIncomplete && in.State != "released") {
		return ErrConflict
	}
	records, err := cloneWorkloadRecordsDB(ctx, tx, op.AccountID, op.ProjectID, op.ID)
	if err != nil {
		return err
	}
	views, err := cloneBindingViews(records)
	if err != nil {
		return err
	}
	validatePlan := validateCloneCheckpointReleasePlan
	if allowRetired {
		validatePlan = validateCloneCheckpointReleasePlanContents
	}
	if err := validatePlan(op, in, views); err != nil {
		return fmt.Errorf("checkpoint release original plan: %w", err)
	}
	adoption := ProjectEnvironmentCloneCheckpointAdoption{Receipt: in.Receipt}
	resources := append([]ProjectEnvironmentCloneResource(nil), op.Resources[:2]...)
	for _, source := range in.Plan.Sources {
		switch source.Kind {
		case "managed_postgres":
			snapshot, err := readClonePostgresSnapshotTx(ctx, tx, op, source.ID)
			if err != nil {
				return ErrConflict
			}
			if allowRetired && snapshot.State == "deleted" {
				proof, err := checkpointCaptureCatalogueProofTx(ctx, tx, op, in.Plan, in.Receipt, source.Kind, source.ID)
				if err != nil || snapshot.ProviderSnapshotID != proof.CaptureIdentity || !snapshot.CleanupObservedAt.Equal(proof.RetiredAt.Time) {
					return ErrConflict
				}
				// Only original immutable metadata is reconstructed below. This
				// normalization cannot authorize new native IO or source holds.
				snapshot.State = "retained"
			}
			if snapshot.State != "retained" || snapshot.SourceVersion != source.DefinitionSHA256 || snapshot.BackendID != source.BackendID ||
				snapshot.BackendFingerprint != source.BackendFingerprint || snapshot.SourceProviderResourceID != source.ProviderResourceID || snapshot.SourceDataResourceID != source.DataResourceID {
				return ErrConflict
			}
			adoption.Snapshots = append(adoption.Snapshots, ProjectEnvironmentCloneCheckpointSnapshot{SourceDatabaseID: source.ID, RequestStartedAt: snapshot.RequestStartedAt.UTC(),
				Observation: ProjectEnvironmentClonePostgresSnapshotObservation{ProviderSnapshotID: snapshot.ProviderSnapshotID, SourceDataResourceID: snapshot.SourceDataResourceID,
					CapturePoint: snapshot.CapturePoint.UTC(), CreatedAt: snapshot.SnapshotCreatedAt.UTC()}})
			resources = append(resources, ProjectEnvironmentCloneResource{Kind: source.Kind, Name: source.ID, SourceID: source.ID, SourceVersion: source.DefinitionSHA256, CapturePoint: op.Resources[0].CapturePoint, Status: "captured"})
		case "object_storage":
			manifest, err := projectEnvironmentCloneObjectManifestDB(ctx, tx, op.AccountID, op.ProjectID, op.ID, source.ID)
			if err != nil || validateProjectEnvironmentCloneObjectManifest(manifest) != nil {
				return ErrConflict
			}
			want, frozen, err := capturedCloneBucketReservation(op, views, source.AppID, source.ID)
			if err != nil {
				return err
			}
			row, err := readCloneCheckpointReleaseTargetTx(ctx, tx, op, source.AppID, manifest.TargetBucketID, allowRetired)
			if err != nil {
				return ErrConflict
			}
			target := objectBucketFromSQL(row)
			want.ID = target.ID
			want.PhysicalName = "gregale-" + strings.ReplaceAll(target.ID, "-", "")
			placement := target
			if allowRetired && (placement.State == "deleting" || placement.State == "deleted") {
				placement.State = "ready"
			}
			if validateCloneBucketReservation(want, placement, frozen) != nil {
				return ErrConflict
			}
			captured := ProjectEnvironmentCloneCheckpointManifest{SourceBucketID: source.ID, Hash: manifest.Hash, CapturedAt: manifest.CapturedAt, Objects: make([]ProjectEnvironmentCloneObjectVersion, len(manifest.Objects))}
			for i, item := range manifest.Objects {
				captured.Objects[i] = item.Source
			}
			adoption.Manifests = append(adoption.Manifests, captured)
			resources = append(resources, ProjectEnvironmentCloneResource{Kind: source.Kind, Name: source.ID, SourceID: source.ID, TargetID: target.ID, SourceVersion: manifest.Hash, CapturePoint: op.Resources[0].CapturePoint, Status: "captured"})
		default:
			return ErrConflict
		}
	}
	databases, buckets, err := validateCloneCheckpointAdoptionSources(adoption, views, time.Time{})
	if err != nil {
		return fmt.Errorf("checkpoint release original datasets: %w", err)
	}
	digest := cloneCheckpointAdoptionSHA(adoption)
	if in.AdoptionSHA256 != "" && in.AdoptionSHA256 != digest {
		return fmt.Errorf("checkpoint release adoption digest: %w", ErrConflict)
	}
	resources = append(resources, ProjectEnvironmentCloneResource{Kind: "data_checkpoint_adoption", Name: op.SourceEnvironment, SourceID: in.Receipt.CheckpointID, SourceVersion: digest, CapturePoint: op.Resources[0].CapturePoint, Status: "captured"})
	if in.State == "released" {
		if !validCloneObjectSHA256(in.ObservationSHA256) {
			return ErrConflict
		}
		resources = append(resources, ProjectEnvironmentCloneResource{Kind: "data_checkpoint_release", Name: op.SourceEnvironment, SourceID: in.Receipt.CheckpointID, SourceVersion: in.ObservationSHA256, CapturePoint: op.Resources[0].CapturePoint, Status: "captured"})
	}
	if !reflect.DeepEqual(op.Resources, resources) {
		return fmt.Errorf("checkpoint release adopted inventory: %w", ErrConflict)
	}
	if requireHolds {
		if err := validateCloneCheckpointAdoptionHoldsTx(ctx, tx, op, adoption, databases, buckets, views); err != nil {
			return err
		}
		row, err := sqlc.New().ReadProjectEnvironmentCloneConfigurationGuard(ctx, tx, sqlc.ReadProjectEnvironmentCloneConfigurationGuardParams{ProjectID: mustPgUUID(op.ProjectID), AccountID: mustPgUUID(op.AccountID)})
		if err != nil {
			return mapErr(err)
		}
		fence, err := cloneConfigurationFenceFromSQL(op, row)
		if err != nil || fence.Generation != in.Plan.ConfigurationGeneration || !fence.HeldAt.Equal(in.Plan.ConfigurationHeldAt) {
			return ErrConflict
		}
	}
	return nil
}

func readCloneCheckpointReleaseTargetTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, appID, targetID string, allowRetired bool) (sqlc.ObjectBucket, error) {
	if allowRetired {
		return sqlc.New().LockCloneCheckpointObjectPublicationBucket(ctx, tx, sqlc.LockCloneCheckpointObjectPublicationBucketParams{AccountID: mustPgUUID(op.AccountID), AppID: mustPgUUID(appID), ID: mustPgUUID(targetID), EnvironmentCloneOperationID: mustPgUUID(op.ID)})
	}
	return sqlc.New().ReadProjectEnvironmentCloneObjectBucket(ctx, tx, sqlc.ReadProjectEnvironmentCloneObjectBucketParams{AccountID: mustPgUUID(op.AccountID), AppID: mustPgUUID(appID), ID: mustPgUUID(targetID), EnvironmentCloneOperationID: mustPgUUID(op.ID)})
}

func (s *PgStore) ProjectEnvironmentCloneCheckpointReleaseForLease(ctx context.Context, lease ProjectEnvironmentCloneLease) (ProjectEnvironmentCloneCheckpointRelease, error) {
	var out ProjectEnvironmentCloneCheckpointRelease
	err := s.checkpointReleaseTx(ctx, lease, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		var err error
		out, err = readCloneCheckpointReleaseTx(ctx, tx, op)
		if err != nil {
			return err
		}
		return validateCloneCheckpointReleaseJournalsTx(ctx, tx, op, out, out.State != "released")
	})
	return out, err
}

func (s *PgStore) ReserveProjectEnvironmentCloneCheckpointRelease(ctx context.Context, lease ProjectEnvironmentCloneLease, plan ProjectEnvironmentCloneCheckpointPlan, receipt ProjectEnvironmentCloneCheckpointReceipt) (ProjectEnvironmentCloneCheckpointRelease, error) {
	var out ProjectEnvironmentCloneCheckpointRelease
	err := s.checkpointReleaseTx(ctx, lease, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		var err error
		out, err = readCloneCheckpointReleaseTx(ctx, tx, op)
		if err == nil {
			if cloneCheckpointSHA(out.Plan) != cloneCheckpointSHA(plan) || cloneCheckpointSHA(out.Receipt) != cloneCheckpointSHA(receipt) {
				return ErrConflict
			}
			return validateCloneCheckpointReleaseJournalsTx(ctx, tx, op, out, out.State != "released")
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		out = ProjectEnvironmentCloneCheckpointRelease{Plan: plan, Receipt: receipt, State: "planned"}
		if err := validateCloneCheckpointReleaseJournalsTx(ctx, tx, op, out, true); err != nil {
			return err
		}
		out.AdoptionSHA256 = op.Resources[len(op.Resources)-1].SourceVersion
		rawPlan, err := json.Marshal(plan)
		if err != nil {
			return ErrInvalidArgument
		}
		rawReceipt, err := json.Marshal(receipt)
		if err != nil {
			return ErrInvalidArgument
		}
		row, err := sqlc.New().InsertCloneCheckpointRelease(ctx, tx, sqlc.InsertCloneCheckpointReleaseParams{OperationID: mustPgUUID(op.ID), Plan: rawPlan, Receipt: rawReceipt,
			AdoptionSha256: out.AdoptionSHA256, ExpectedRevision: op.Revision, WorkerToken: mustPgUUID(lease.Token)})
		if err != nil {
			return mapErr(err)
		}
		out, err = cloneCheckpointReleaseFromSQL(row)
		return err
	})
	return out, err
}

func (s *PgStore) ClaimProjectEnvironmentCloneCheckpointRelease(ctx context.Context, lease ProjectEnvironmentCloneLease) (ProjectEnvironmentCloneCheckpointRelease, bool, error) {
	var out ProjectEnvironmentCloneCheckpointRelease
	dispatch := false
	err := s.checkpointReleaseTx(ctx, lease, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		var err error
		out, err = readCloneCheckpointReleaseTx(ctx, tx, op)
		if err != nil {
			return err
		}
		if err := validateCloneCheckpointReleaseJournalsTx(ctx, tx, op, out, out.State != "released"); err != nil {
			return err
		}
		if out.State != "planned" {
			return nil
		}
		row, err := sqlc.New().ClaimCloneCheckpointRelease(ctx, tx, sqlc.ClaimCloneCheckpointReleaseParams{OperationID: mustPgUUID(op.ID), ExpectedRevision: op.Revision, WorkerToken: mustPgUUID(lease.Token)})
		if err != nil {
			return mapErr(err)
		}
		out, err = cloneCheckpointReleaseFromSQL(row)
		dispatch = err == nil
		return err
	})
	return out, dispatch && err == nil, err
}

func cloneCheckpointReleaseWriteResult(count int64, err error) error {
	if err != nil {
		return mapErr(err)
	}
	if count != 1 {
		return ErrConflict
	}
	return nil
}

func (s *PgStore) FinishProjectEnvironmentCloneCheckpointRelease(ctx context.Context, lease ProjectEnvironmentCloneLease, observed ProjectEnvironmentCloneCheckpointReleaseObservation) (ProjectEnvironmentCloneOperation, error) {
	var updated ProjectEnvironmentCloneOperation
	// Finish updates the revision, so its final exact-token SQL check replaces
	// the pre-commit read authorization used by the other release transactions.
	if !validCloneLeaseIdentity(lease) {
		return updated, ErrInvalidArgument
	}
	ctx, cancel := context.WithDeadline(ctx, lease.ExpiresAt)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return updated, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	op, err := lockCloneWorkloadOperationTx(ctx, tx, lease.Operation.AccountID, lease.Operation.ProjectID, lease.Operation.ID)
	if err != nil {
		return updated, err
	}
	if op.Status != CloneOperationCapturing {
		return updated, ErrConflict
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, op, &lease); err != nil {
		return updated, err
	}
	in, err := readCloneCheckpointReleaseTx(ctx, tx, op)
	if err != nil {
		return updated, err
	}
	if err := validateCloneCheckpointReleaseObservation(in, observed, lease.ExpiresAt); err != nil {
		return updated, err
	}
	if err := validateCloneCheckpointReleaseJournalsTx(ctx, tx, op, in, in.State != "released"); err != nil {
		return updated, err
	}
	if in.State == "released" {
		// A later capture may own fresh holds. Replay never reads or clears them.
		updated, err = cloneWorkerOperationDB(ctx, tx, op.AccountID, op.ProjectID, op.ID)
		if err != nil {
			return updated, err
		}
		if err := authorizeCloneObjectMutationTx(ctx, tx, op, &lease); err != nil {
			return updated, err
		}
		return updated, mapErr(tx.Commit(ctx))
	}
	q := sqlc.New()
	digest := cloneCheckpointReleaseSHA(observed)
	if err := cloneCheckpointReleaseWriteResult(q.RecordCloneCheckpointRelease(ctx, tx, sqlc.RecordCloneCheckpointReleaseParams{OperationID: mustPgUUID(op.ID), ObservationSha256: digest, ExpectedRevision: op.Revision, WorkerToken: mustPgUUID(lease.Token)})); err != nil {
		return updated, err
	}
	for _, source := range observed.Sources {
		if source.Kind == "managed_postgres" {
			err = cloneCheckpointReleaseWriteResult(q.ReleaseCloneCheckpointPostgresFence(ctx, tx, sqlc.ReleaseCloneCheckpointPostgresFenceParams{OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(source.ID), RemoteReleasedAt: pgtype.Timestamptz{Time: source.ReleasedAt, Valid: true}, ExpectedRevision: op.Revision, WorkerToken: mustPgUUID(lease.Token)}))
		} else {
			err = cloneCheckpointReleaseWriteResult(q.ReleaseCloneCheckpointObjectFence(ctx, tx, sqlc.ReleaseCloneCheckpointObjectFenceParams{OperationID: mustPgUUID(op.ID), BucketID: mustPgUUID(source.ID), ExpectedRevision: op.Revision, WorkerToken: mustPgUUID(lease.Token)}))
		}
		if err != nil {
			return updated, err
		}
	}
	if err := cloneCheckpointReleaseWriteResult(q.ReleaseCloneCheckpointConfiguration(ctx, tx, sqlc.ReleaseCloneCheckpointConfigurationParams{OperationID: mustPgUUID(op.ID), ProjectID: mustPgUUID(op.ProjectID), AccountID: mustPgUUID(op.AccountID), Generation: in.Plan.ConfigurationGeneration, HeldAt: pgtype.Timestamptz{Time: in.Plan.ConfigurationHeldAt, Valid: true}, ExpectedRevision: op.Revision, WorkerToken: mustPgUUID(lease.Token)})); err != nil {
		return updated, err
	}
	resources := append([]ProjectEnvironmentCloneResource(nil), op.Resources...)
	resources = append(resources, ProjectEnvironmentCloneResource{Kind: "data_checkpoint_release", Name: op.SourceEnvironment, SourceID: in.Receipt.CheckpointID, SourceVersion: digest, CapturePoint: op.Resources[0].CapturePoint, Status: "captured"})
	if err := validateCloneResourceTransition(op, CloneOperationCapturing, resources, ""); err != nil {
		return updated, err
	}
	raw, err := json.Marshal(resources)
	if err != nil {
		return updated, ErrInvalidArgument
	}
	if err := cloneCheckpointReleaseWriteResult(q.FinishCloneCheckpointRelease(ctx, tx, sqlc.FinishCloneCheckpointReleaseParams{OperationID: mustPgUUID(op.ID), AccountID: mustPgUUID(op.AccountID), ProjectID: mustPgUUID(op.ProjectID), ExpectedRevision: op.Revision, WorkerToken: mustPgUUID(lease.Token), Resources: raw, ObservationSha256: digest, ReleasedGeneration: in.Plan.ConfigurationGeneration + 1})); err != nil {
		return updated, err
	}
	updated, err = cloneWorkerOperationDB(ctx, tx, op.AccountID, op.ProjectID, op.ID)
	if err != nil {
		return updated, err
	}
	return updated, mapErr(tx.Commit(ctx))
}
