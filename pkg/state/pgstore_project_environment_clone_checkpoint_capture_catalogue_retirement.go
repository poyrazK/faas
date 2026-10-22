package state

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func checkpointAdoptedCaptureReadyTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, p ProjectEnvironmentCloneCheckpointCaptureRetirementPlan) error {
	q := sqlc.New()
	blocked, err := q.CloneCheckpointCaptureRetirementHasBorrowers(ctx, tx, mustPgUUID(op.ID))
	if err != nil {
		return mapErr(err)
	}
	if !blocked.Valid || blocked.Bool {
		return ErrConflict
	}
	counts, err := q.CloneCheckpointCaptureRetirementCatalogueCounts(ctx, tx, mustPgUUID(op.ID))
	if err != nil {
		return mapErr(err)
	}
	var pg, objects int64
	for _, source := range p.SourceCompensation.Plan.Capture.Sources {
		if source.Kind == "managed_postgres" {
			pg++
			if err = checkpointCapturePostgresTargetRetiredTx(ctx, tx, op, source.ID); err != nil {
				return err
			}
		} else {
			objects++
			if err = checkpointCaptureObjectTargetRetiredTx(ctx, tx, op, source); err != nil {
				return err
			}
		}
	}
	var retired bool
	r, err := q.ReadCloneCheckpointCaptureRetirement(ctx, tx, mustPgUUID(op.ID))
	if err == nil {
		in, err := checkpointCaptureRetirementFromSQL(r)
		if err != nil || !reflect.DeepEqual(in.Plan, p) {
			return ErrConflict
		}
		retired = in.State == "retired"
	} else if !errors.Is(mapErr(err), ErrNotFound) {
		return mapErr(err)
	}
	wantRetirements := int64(0)
	if retired {
		wantRetirements = pg + objects
		for _, source := range p.SourceCompensation.Plan.Capture.Sources {
			proof, err := checkpointCaptureCatalogueProofTx(ctx, tx, op, p.SourceCompensation.Plan.Capture, *p.SourceCompensation.Plan.Receipt, source.Kind, source.ID)
			if err != nil {
				return err
			}
			if source.Kind == "managed_postgres" {
				snapshot, err := readClonePostgresSnapshotTx(ctx, tx, op, source.ID)
				if err != nil || snapshot.State != "deleted" || snapshot.ProviderSnapshotID != proof.CaptureIdentity || !snapshot.CleanupObservedAt.Equal(proof.RetiredAt.Time) {
					return ErrConflict
				}
			}
		}
	}
	if counts.Snapshots != pg || counts.Manifests != objects || counts.Retirements != wantRetirements || counts.TargetBuckets != objects {
		return ErrConflict
	}
	return nil
}

// Local deleted restore labels cannot close native restore/isolation requests.
// Authenticate the original plan and exact proof digest attached to the restore,
// and recheck every credential retirement and local publication withdrawal.
func checkpointCapturePostgresTargetRetiredTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, id string) error {
	snapshot, err := readClonePostgresSnapshotTx(ctx, tx, op, id)
	if err != nil {
		return err
	}
	// Only absence of the original restore row means no request authority.
	// Missing dependencies of an existing restore must remain conflicts.
	q := sqlc.New()
	_, err = q.ReadProjectEnvironmentClonePostgresSnapshotRestore(ctx, tx, sqlc.ReadProjectEnvironmentClonePostgresSnapshotRestoreParams{OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(id)})
	if errors.Is(mapErr(err), ErrNotFound) {
		return nil
	}
	if err != nil {
		return mapErr(err)
	}
	row, err := q.ReadCloneCheckpointPgTargetCompensation(ctx, tx, sqlc.ReadCloneCheckpointPgTargetCompensationParams{OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(id)})
	if err != nil {
		return checkpointPgCompensationDependencyError(mapErr(err))
	}
	in, err := checkpointPgTargetCompensationFromSQL(row)
	if err != nil {
		return err
	}
	if in.State != "compensated" {
		return ErrConflict
	}
	actual, err := checkpointPgTargetCompensationOriginalPlanTx(ctx, tx, op, id, snapshot)
	if err != nil {
		return checkpointPgCompensationDependencyError(err)
	}
	if err = checkpointPgTargetCompensationMatchesTx(ctx, tx, op, in, actual); err != nil {
		return err
	}
	return checkpointPgTargetCompensationReadyTx(ctx, tx, op, in.Plan)
}

// An original native creation intent requires its complete authenticated
// retirement journal, even when no creation/copy reply was ever observed.
func checkpointCaptureObjectTargetRetiredTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, source ProjectEnvironmentCloneCheckpointSource) error {
	creation, err := readCheckpointObjectTargetTx(ctx, tx, op, source.ID)
	if errors.Is(err, ErrNotFound) {
		return checkpointCaptureUnusedObjectTargetTx(ctx, tx, op, source)
	}
	if err != nil {
		return err
	}
	plan, bucket, err := checkpointObjectRetirementPlanTx(ctx, tx, op, creation)
	if err != nil {
		return checkpointPgCompensationDependencyError(err)
	}
	retirement, err := readCheckpointObjectRetirementTx(ctx, tx, op, plan, bucket)
	if err != nil {
		return checkpointPgCompensationDependencyError(err)
	}
	if retirement.State != "retired" || validateCheckpointObjectRetirementObservation(retirement, retirement.Observation) != nil {
		return ErrConflict
	}
	return nil
}

// Frozen manifests and unused target completion evidence survive native capture
// retirement. Neither deleted bucket labels nor missing native intents suffice.
func checkpointCaptureUnusedObjectTargetTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, source ProjectEnvironmentCloneCheckpointSource) error {
	records, err := cloneWorkloadRecordsDB(ctx, tx, op.AccountID, op.ProjectID, op.ID)
	if err != nil {
		return err
	}
	views, err := cloneBindingViews(records)
	if err != nil {
		return err
	}
	want, frozen, err := capturedCloneBucketReservation(op, views, source.AppID, source.ID)
	if err != nil {
		return err
	}
	manifest, err := projectEnvironmentCloneObjectManifestDB(ctx, tx, op.AccountID, op.ProjectID, op.ID, source.ID)
	if err != nil {
		return ErrConflict
	}
	r, err := readCloneCheckpointReleaseTargetTx(ctx, tx, op, source.AppID, manifest.TargetBucketID, true)
	if err != nil {
		return ErrConflict
	}
	bucket := objectBucketFromSQL(r)
	if bucket.State != "deleted" || r.LeaseToken.Valid || r.LeaseUntil.Valid {
		return ErrConflict
	}
	want.ID, want.PhysicalName = bucket.ID, "gregale-"+strings.ReplaceAll(bucket.ID, "-", "")
	placement := bucket
	placement.State = "provisioning"
	if validateCloneBucketReservation(want, placement, frozen) != nil {
		return ErrConflict
	}
	saved, err := sqlc.New().ReadCloneCheckpointUnusedObjectReservation(ctx, tx, sqlc.ReadCloneCheckpointUnusedObjectReservationParams{OperationID: mustPgUUID(op.ID), SourceBucketID: mustPgUUID(source.ID)})
	if err != nil {
		return ErrConflict
	}
	var original ObjectBucket
	if json.Unmarshal(saved.Reservation, &original) != nil || !reflect.DeepEqual(original, want) || pgUUIDString(saved.TargetBucketID) != bucket.ID || !saved.CompletedAt.Valid {
		return ErrConflict
	}
	return checkpointObjectRetirementWithdrawalTx(ctx, tx, op, ProjectEnvironmentCloneCheckpointObjectTargetPlan{SourceBucketID: source.ID, TargetBucketID: bucket.ID, AppID: source.AppID})
}

// This helper validates saved terminal evidence without recursively rebuilding
// adoption. Its caller authenticates the full frozen adoption catalogue. It is
// used only by compensating reads and grants no capture/copy/publication authority.
func checkpointCaptureCatalogueProofTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, capture ProjectEnvironmentCloneCheckpointPlan, receipt ProjectEnvironmentCloneCheckpointReceipt, kind, id string) (sqlc.ProjectEnvironmentCloneCheckpointCaptureCatalogue, error) {
	var zero sqlc.ProjectEnvironmentCloneCheckpointCaptureCatalogue
	if op.Status != CloneOperationCompensating {
		return zero, ErrConflict
	}
	q := sqlc.New()
	r, err := q.ReadCloneCheckpointCaptureCatalogueRetirement(ctx, tx, sqlc.ReadCloneCheckpointCaptureCatalogueRetirementParams{OperationID: mustPgUUID(op.ID), SourceKind: kind, SourceID: mustPgUUID(id)})
	if err != nil {
		return zero, ErrConflict
	}
	journal, err := q.ReadCloneCheckpointCaptureRetirement(ctx, tx, mustPgUUID(op.ID))
	if err != nil {
		return zero, ErrConflict
	}
	in, err := checkpointCaptureRetirementFromSQL(journal)
	if err != nil || in.State != "retired" || !reflect.DeepEqual(in.Plan.SourceCompensation.Plan.Capture, capture) || in.Plan.SourceCompensation.Plan.Receipt == nil || !reflect.DeepEqual(*in.Plan.SourceCompensation.Plan.Receipt, receipt) || r.ObservationSha256 != in.ObservationSHA256 || !r.RetiredAt.Valid || !r.RetiredAt.Time.Equal(in.Observation.RetiredAt) {
		return zero, ErrConflict
	}
	saved, err := q.ReadCloneCheckpointSourceCompensation(ctx, tx, mustPgUUID(op.ID))
	if err != nil {
		return zero, ErrConflict
	}
	sourceProof, err := checkpointSourceCompensationFromSQL(saved)
	if err != nil || !reflect.DeepEqual(sourceProof, in.Plan.SourceCompensation) {
		return zero, ErrConflict
	}
	for _, source := range in.Observation.Sources {
		if source.Kind == kind && source.ID == id && source.CaptureIdentity == r.CaptureIdentity && source.SourceResourceID == r.SourceResourceID {
			return r, checkpointSourceCompensationNoHoldsTx(ctx, tx, op)
		}
	}
	return zero, ErrConflict
}
