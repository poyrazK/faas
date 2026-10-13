package state

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ProjectEnvironmentCloneCheckpointAdoptionStore = (*PgStore)(nil)

// One committing lease check covers all receipts, private target reservations
// and inventory. A failed/uncertain transaction never authorizes provider IO.
func (s *PgStore) AdoptProjectEnvironmentCloneCheckpoint(ctx context.Context, lease ProjectEnvironmentCloneLease, adoption ProjectEnvironmentCloneCheckpointAdoption, bucketLimit int) (ProjectEnvironmentCloneOperation, error) {
	var zero ProjectEnvironmentCloneOperation
	if !validCloneLeaseIdentity(lease) || bucketLimit < 1 {
		return zero, ErrInvalidArgument
	}
	adoption = canonicalCloneCheckpointAdoption(adoption)
	ctx, cancel := context.WithDeadline(ctx, lease.ExpiresAt)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return zero, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	op, err := lockCloneWorkloadOperationTx(ctx, tx, lease.Operation.AccountID, lease.Operation.ProjectID, lease.Operation.ID)
	if err != nil {
		return zero, err
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, op, &lease); err != nil {
		return zero, err
	}
	if err := validateCloneCheckpointAdoptionReceipt(op, adoption); err != nil {
		return zero, err
	}
	records, err := cloneWorkloadRecordsDB(ctx, tx, op.AccountID, op.ProjectID, op.ID)
	if err != nil {
		return zero, err
	}
	views, err := cloneBindingViews(records)
	if err != nil {
		return zero, err
	}
	databases, buckets, err := validateCloneCheckpointAdoptionSources(adoption, views, lease.ExpiresAt)
	if err != nil {
		return zero, err
	}
	if err := validateCloneCheckpointAdoptionHoldsTx(ctx, tx, op, adoption, databases, buckets, views); err != nil {
		return zero, err
	}
	resources := append([]ProjectEnvironmentCloneResource(nil), op.Resources[:2]...)
	for _, snapshot := range adoption.Snapshots {
		source := databases[snapshot.SourceDatabaseID]
		hash, err := ProjectEnvironmentCloneDatabaseSourceHash(source)
		if err != nil {
			return zero, err
		}
		resource := ProjectEnvironmentCloneResource{Kind: "managed_postgres", Name: source.DatabaseID, SourceID: source.DatabaseID,
			SourceVersion: hash, CapturePoint: op.Resources[0].CapturePoint, Status: "captured"}
		if err := adoptCloneCheckpointSnapshotTx(ctx, tx, op, lease, source, resource, snapshot); err != nil {
			return zero, err
		}
		resources = append(resources, resource)
	}
	for _, captured := range adoption.Manifests {
		appID := buckets[captured.SourceBucketID]
		want, source, err := capturedCloneBucketReservation(op, views, appID, captured.SourceBucketID)
		if err != nil {
			return zero, err
		}
		existing, err := projectEnvironmentCloneObjectManifestDB(ctx, tx, op.AccountID, op.ProjectID, op.ID, captured.SourceBucketID)
		if err == nil {
			want.ID = existing.TargetBucketID
			want.PhysicalName = "gregale-" + strings.ReplaceAll(want.ID, "-", "")
		} else if !errors.Is(err, ErrNotFound) {
			return zero, err
		} else if len(op.Resources) != 2 {
			return zero, ErrConflict
		} else {
			// An earlier private reservation may already own the deterministic
			// name. Authenticate it before choosing the manifest's target ID.
			row, readErr := sqlc.New().ObjectBucketByName(ctx, tx, sqlc.ObjectBucketByNameParams{AppID: mustPgUUID(appID), AccountID: mustPgUUID(op.AccountID), Name: want.Name, Scope: want.Scope})
			if readErr == nil {
				reserved := objectBucketFromSQL(row)
				if err := validateCloneBucketReservation(want, reserved, source); err != nil {
					return zero, err
				}
				want.ID, want.PhysicalName = reserved.ID, reserved.PhysicalName
			} else if !errors.Is(readErr, pgx.ErrNoRows) {
				return zero, mapErr(readErr)
			}
		}
		manifest := ProjectEnvironmentCloneObjectManifest{OperationID: op.ID, SourceBucketID: captured.SourceBucketID, TargetBucketID: want.ID,
			CapturedAt: captured.CapturedAt, Hash: captured.Hash, Objects: make([]ProjectEnvironmentCloneObjectCheckpoint, len(captured.Objects))}
		for i, item := range captured.Objects {
			manifest.Objects[i].Source = item
		}
		if err := validateProjectEnvironmentCloneObjectManifest(manifest); err != nil {
			return zero, err
		}
		if errors.Is(err, ErrNotFound) {
			// The original complete manifest is the target's private admission
			// intent. Both header/entries and the bucket row commit together.
			if err := insertCloneObjectManifestTx(ctx, tx, manifest); err != nil {
				return zero, err
			}
		} else if !reflect.DeepEqual(existing, manifest) {
			return zero, ErrConflict
		}
		target, _, err := reserveObjectBucketTx(ctx, tx, want, bucketLimit)
		if err != nil {
			return zero, err
		}
		if target.ID != want.ID || validateCloneBucketReservation(want, target, source) != nil {
			return zero, ErrConflict
		}
		resources = append(resources, ProjectEnvironmentCloneResource{Kind: "object_storage", Name: source.ID, SourceID: source.ID, TargetID: target.ID,
			SourceVersion: manifest.Hash, CapturePoint: op.Resources[0].CapturePoint, Status: "captured"})
	}
	resources = append(resources, ProjectEnvironmentCloneResource{Kind: "data_checkpoint_adoption", Name: op.SourceEnvironment, SourceID: op.Resources[0].TargetID,
		SourceVersion: cloneCheckpointAdoptionSHA(adoption), CapturePoint: op.Resources[0].CapturePoint, Status: "captured"})
	if len(op.Resources) != 2 && !reflect.DeepEqual(op.Resources, resources) {
		return zero, ErrConflict
	}
	if err := validateCloneResourceTransition(op, CloneOperationCapturing, resources, ""); err != nil {
		return zero, err
	}
	if reflect.DeepEqual(op.Resources, resources) {
		// Replay still verifies the original journals and complete held roster.
		if err := authorizeCloneObjectMutationTx(ctx, tx, op, &lease); err != nil {
			return zero, err
		}
		return op, mapErr(tx.Commit(ctx))
	}
	raw, err := json.Marshal(resources)
	if err != nil {
		return zero, ErrInvalidArgument
	}
	count, err := sqlc.New().AdvanceProjectEnvironmentCloneCheckpointIntent(ctx, tx, sqlc.AdvanceProjectEnvironmentCloneCheckpointIntentParams{
		OperationID: mustPgUUID(op.ID), AccountID: mustPgUUID(op.AccountID), ProjectID: mustPgUUID(op.ProjectID), ExpectedRevision: op.Revision,
		WorkerToken: mustPgUUID(lease.Token), Resources: raw})
	if err != nil {
		return zero, mapErr(err)
	}
	if count != 1 {
		return zero, ErrConflict
	}
	updated, err := cloneWorkerOperationDB(ctx, tx, op.AccountID, op.ProjectID, op.ID)
	if err != nil {
		return zero, err
	}
	return updated, mapErr(tx.Commit(ctx))
}

func adoptCloneCheckpointSnapshotTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, lease ProjectEnvironmentCloneLease,
	source ProjectEnvironmentClonePostgresBinding, resource ProjectEnvironmentCloneResource, snapshot ProjectEnvironmentCloneCheckpointSnapshot) error {
	actual := snapshot.Observation
	receipt, err := readClonePostgresSnapshotTx(ctx, tx, op, source.DatabaseID)
	if errors.Is(err, ErrNotFound) {
		if len(op.Resources) != 2 {
			return ErrConflict
		}
		row, err := sqlc.New().AdoptProjectEnvironmentCloneCheckpointSnapshot(ctx, tx, sqlc.AdoptProjectEnvironmentCloneCheckpointSnapshotParams{
			OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(source.DatabaseID), SourceVersion: resource.SourceVersion, BackendID: source.BackendID,
			BackendFingerprint: source.BackendFingerprint, SourceProviderResourceID: source.ProviderResourceID, SourceDataResourceID: source.DataResourceID,
			CapturePoint: pgtype.Timestamptz{Time: actual.CapturePoint, Valid: true}, ProviderSnapshotID: actual.ProviderSnapshotID,
			SnapshotCreatedAt: pgtype.Timestamptz{Time: actual.CreatedAt, Valid: true}, RequestStartedAt: pgtype.Timestamptz{Time: snapshot.RequestStartedAt, Valid: true}, ExpectedRevision: op.Revision, WorkerToken: mustPgUUID(lease.Token)})
		if err != nil {
			return mapErr(err)
		}
		receipt = clonePostgresSnapshotFromSQL(row)
	} else if err != nil {
		return err
	}
	if receipt.State != "retained" || receipt.ProviderSnapshotID != actual.ProviderSnapshotID || !receipt.RequestStartedAt.Equal(snapshot.RequestStartedAt) ||
		validateClonePostgresSnapshotSource(receipt, op, source, resource, actual.CapturePoint) != nil || validateCloneSnapshotObservation(receipt, actual) != nil {
		return ErrConflict
	}
	return nil
}

func validateCloneCheckpointAdoptionHoldsTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, in ProjectEnvironmentCloneCheckpointAdoption,
	databases map[string]ProjectEnvironmentClonePostgresBinding, buckets map[string]string, views []ProjectEnvironmentCloneBindings) error {
	q := sqlc.New()
	guard, err := q.ReadProjectEnvironmentCloneConfigurationGuard(ctx, tx, sqlc.ReadProjectEnvironmentCloneConfigurationGuardParams{
		ProjectID: mustPgUUID(op.ProjectID), AccountID: mustPgUUID(op.AccountID)})
	if err != nil {
		return mapErr(err)
	}
	fence, err := cloneConfigurationFenceFromSQL(op, guard)
	if err != nil || in.Receipt.CapturedAt.Before(fence.HeldAt) {
		return ErrConflict
	}
	for _, snapshot := range in.Snapshots {
		if snapshot.RequestStartedAt.Before(fence.HeldAt) {
			return ErrConflict
		}
	}
	postgres, err := q.ListClonePostgresWriteFences(ctx, tx, mustPgUUID(op.ID))
	if err != nil {
		return mapErr(err)
	}
	if len(postgres) != len(databases) {
		return ErrConflict
	}
	for _, row := range postgres {
		held := clonePostgresWriteFenceFromSQL(row)
		source, ok := databases[held.SourceDatabaseID]
		hash, err := ProjectEnvironmentCloneDatabaseSourceHash(source)
		if !ok || err != nil || held.State != "held" || held.SourceVersion != hash || held.BackendID != source.BackendID ||
			held.BackendFingerprint != source.BackendFingerprint || held.SourceProviderResourceID != source.ProviderResourceID || held.SourceDataResourceID != source.DataResourceID {
			return ErrConflict
		}
		if err := lockClonePostgresFenceSource(ctx, tx, op, held); err != nil {
			return err
		}
	}
	objects, err := cloneObjectWriteFencesTx(ctx, tx, op)
	if err != nil {
		return err
	}
	if len(objects) != len(buckets) {
		return ErrConflict
	}
	for _, held := range objects {
		_, source, err := capturedCloneBucketReservation(op, views, buckets[held.Bucket.ID], held.Bucket.ID)
		if err != nil || held.Bucket.BackendID != source.BackendID || held.Bucket.BackendFingerprint != source.BackendFingerprint || held.Bucket.PhysicalName != source.PhysicalName {
			return ErrConflict
		}
		if _, ok := buckets[held.Bucket.ID]; !ok || held.Requests != 0 || held.NativeGrants != 0 || held.Deletions != 0 ||
			held.Protections != 0 || held.Uploads != 0 || held.Multipart != 0 {
			return ErrConflict
		}
	}
	return nil
}
