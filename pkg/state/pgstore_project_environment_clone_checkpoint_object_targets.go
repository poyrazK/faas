package state

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ProjectEnvironmentCloneCheckpointObjectTargetStore = (*PgStore)(nil)

func checkpointObjectTargetFromSQL(row sqlc.ProjectEnvironmentCloneCheckpointObjectTarget) (ProjectEnvironmentCloneCheckpointObjectTarget, error) {
	in := ProjectEnvironmentCloneCheckpointObjectTarget{State: row.State, RequestedAt: row.RequestedAt.Time.UTC(), ObservationSHA256: row.ObservationSha256.String}
	if json.Unmarshal(row.Plan, &in.Plan) != nil || in.Plan.OperationID != pgUUIDString(row.OperationID) || in.Plan.SourceBucketID != pgUUIDString(row.SourceBucketID) || in.Plan.TargetBucketID != pgUUIDString(row.TargetBucketID) {
		return in, ErrConflict
	}
	return in, nil
}

func readCheckpointObjectTargetTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, sourceID string) (ProjectEnvironmentCloneCheckpointObjectTarget, error) {
	row, err := sqlc.New().ReadCloneCheckpointObjectTarget(ctx, tx, sqlc.ReadCloneCheckpointObjectTargetParams{OperationID: mustPgUUID(op.ID), SourceBucketID: mustPgUUID(sourceID)})
	if err != nil {
		return ProjectEnvironmentCloneCheckpointObjectTarget{}, mapErr(err)
	}
	return checkpointObjectTargetFromSQL(row)
}

// Authenticate the original released inventory, manifest and private reservation
// under the operation lock. Current source configuration is deliberately absent.
func checkpointObjectTargetPlanTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, appID, sourceID, targetID string, retention *ProjectEnvironmentCloneCheckpointReleaseObservation, lease ProjectEnvironmentCloneLease) (ProjectEnvironmentCloneCheckpointObjectTargetPlan, ObjectBucket, error) {
	var plan ProjectEnvironmentCloneCheckpointObjectTargetPlan
	var bucket ObjectBucket
	release, err := readCloneCheckpointReleaseTx(ctx, tx, op)
	if err != nil {
		return plan, bucket, err
	}
	if release.State != "released" {
		return plan, bucket, ErrConflict
	}
	if err := validateCloneCheckpointReleaseJournalsTx(ctx, tx, op, release, false); err != nil {
		return plan, bucket, err
	}
	if retention != nil {
		if err := validateCloneCheckpointReleaseObservation(release, *retention, lease.ExpiresAt); err != nil {
			return plan, bucket, err
		}
	}
	found := false
	for _, source := range release.Plan.Sources {
		if source.Kind == "object_storage" && source.ID == sourceID && source.AppID == appID {
			found = true
		}
	}
	if !found {
		return plan, bucket, ErrConflict
	}
	manifest, err := projectEnvironmentCloneObjectManifestDB(ctx, tx, op.AccountID, op.ProjectID, op.ID, sourceID)
	if err != nil || manifest.TargetBucketID != targetID {
		return plan, bucket, ErrConflict
	}
	row, err := sqlc.New().ReadProjectEnvironmentCloneObjectBucket(ctx, tx, sqlc.ReadProjectEnvironmentCloneObjectBucketParams{AccountID: mustPgUUID(op.AccountID), AppID: mustPgUUID(appID), ID: mustPgUUID(targetID), EnvironmentCloneOperationID: mustPgUUID(op.ID)})
	if err != nil {
		return plan, bucket, mapErr(err)
	}
	bucket = objectBucketFromSQL(row)
	if bucket.LeaseToken != "" || !bucket.LeaseUntil.IsZero() {
		return plan, bucket, ErrConflict
	}
	plan = ProjectEnvironmentCloneCheckpointObjectTargetPlan{OperationID: op.ID, AccountID: op.AccountID, AppID: appID, SourceBucketID: sourceID, TargetBucketID: targetID,
		BackendID: bucket.BackendID, BackendFingerprint: bucket.BackendFingerprint, Region: bucket.Region, PhysicalName: bucket.PhysicalName, ManifestSHA256: manifest.Hash, CheckpointID: release.Receipt.CheckpointID}
	return plan, bucket, nil
}

func validateCheckpointObjectTargetTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, in ProjectEnvironmentCloneCheckpointObjectTarget, retention *ProjectEnvironmentCloneCheckpointReleaseObservation, lease ProjectEnvironmentCloneLease) (ObjectBucket, error) {
	plan, bucket, err := checkpointObjectTargetPlanTx(ctx, tx, op, in.Plan.AppID, in.Plan.SourceBucketID, in.Plan.TargetBucketID, retention, lease)
	if err != nil {
		return bucket, err
	}
	if !reflect.DeepEqual(plan, in.Plan) || (in.State == "ready") != (bucket.State == "ready") {
		return bucket, ErrConflict
	}
	return bucket, nil
}

func (s *PgStore) ReserveProjectEnvironmentCloneCheckpointObjectTarget(ctx context.Context, lease ProjectEnvironmentCloneLease, appID, sourceID, targetID string) (ProjectEnvironmentCloneCheckpointObjectTarget, error) {
	var out ProjectEnvironmentCloneCheckpointObjectTarget
	if appID == "" || sourceID == "" || targetID == "" {
		return out, ErrInvalidArgument
	}
	err := s.checkpointReleaseTx(ctx, lease, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		var err error
		out, err = readCheckpointObjectTargetTx(ctx, tx, op, sourceID)
		if err == nil {
			_, err = validateCheckpointObjectTargetTx(ctx, tx, op, out, nil, lease)
			if out.Plan.AppID != appID || out.Plan.TargetBucketID != targetID {
				return ErrConflict
			}
			return err
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		plan, bucket, err := checkpointObjectTargetPlanTx(ctx, tx, op, appID, sourceID, targetID, nil, lease)
		if err != nil {
			return err
		}
		if bucket.State != "provisioning" {
			return ErrConflict
		}
		encoded, err := json.Marshal(plan)
		if err != nil {
			return err
		}
		row, err := sqlc.New().InsertCloneCheckpointObjectTarget(ctx, tx, sqlc.InsertCloneCheckpointObjectTargetParams{OperationID: mustPgUUID(op.ID), SourceBucketID: mustPgUUID(sourceID), TargetBucketID: mustPgUUID(targetID), Plan: encoded, ExpectedRevision: lease.Operation.Revision, WorkerToken: mustPgUUID(lease.Token)})
		if err != nil {
			return mapErr(err)
		}
		out, err = checkpointObjectTargetFromSQL(row)
		return err
	})
	return out, err
}

func (s *PgStore) ClaimProjectEnvironmentCloneCheckpointObjectTarget(ctx context.Context, lease ProjectEnvironmentCloneLease, sourceID string, retention ProjectEnvironmentCloneCheckpointReleaseObservation) (ProjectEnvironmentCloneCheckpointObjectTarget, bool, error) {
	var out ProjectEnvironmentCloneCheckpointObjectTarget
	dispatch := false
	if sourceID == "" {
		return out, false, ErrInvalidArgument
	}
	err := s.checkpointReleaseTx(ctx, lease, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		var err error
		out, err = readCheckpointObjectTargetTx(ctx, tx, op, sourceID)
		if err != nil {
			return err
		}
		if _, err = validateCheckpointObjectTargetTx(ctx, tx, op, out, &retention, lease); err != nil {
			return err
		}
		if out.State != "planned" {
			return nil
		}
		row, err := sqlc.New().ClaimCloneCheckpointObjectTarget(ctx, tx, sqlc.ClaimCloneCheckpointObjectTargetParams{OperationID: mustPgUUID(op.ID), SourceBucketID: mustPgUUID(sourceID), ExpectedRevision: lease.Operation.Revision, WorkerToken: mustPgUUID(lease.Token)})
		if err != nil {
			return mapErr(err)
		}
		out, err = checkpointObjectTargetFromSQL(row)
		dispatch = err == nil
		return err
	})
	// A lost/failed commit never conveys native dispatch authority.
	return out, dispatch && err == nil, err
}

func (s *PgStore) FinishProjectEnvironmentCloneCheckpointObjectTarget(ctx context.Context, lease ProjectEnvironmentCloneLease, sourceID string, observed ProjectEnvironmentCloneCheckpointObjectTargetObservation, retention ProjectEnvironmentCloneCheckpointReleaseObservation) (ObjectBucket, error) {
	var bucket ObjectBucket
	if sourceID == "" {
		return bucket, ErrInvalidArgument
	}
	err := s.checkpointReleaseTx(ctx, lease, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		in, err := readCheckpointObjectTargetTx(ctx, tx, op, sourceID)
		if err != nil {
			return err
		}
		bucket, err = validateCheckpointObjectTargetTx(ctx, tx, op, in, &retention, lease)
		if err != nil {
			return err
		}
		if err := validateCloneCheckpointObjectTargetObservation(in, observed); err != nil {
			return err
		}
		if in.State == "ready" {
			return nil
		}
		q := sqlc.New()
		n, err := q.RecordCloneCheckpointObjectTarget(ctx, tx, sqlc.RecordCloneCheckpointObjectTargetParams{OperationID: mustPgUUID(op.ID), SourceBucketID: mustPgUUID(sourceID), ObservationSha256: cloneCheckpointSHA(observed), ExpectedRevision: lease.Operation.Revision, WorkerToken: mustPgUUID(lease.Token)})
		if err != nil {
			return mapErr(err)
		}
		if n != 1 {
			return ErrConflict
		}
		n, err = q.FinishCloneCheckpointObjectTargetBucket(ctx, tx, sqlc.FinishCloneCheckpointObjectTargetBucketParams{OperationID: mustPgUUID(op.ID), TargetBucketID: mustPgUUID(in.Plan.TargetBucketID), ExpectedRevision: lease.Operation.Revision, WorkerToken: mustPgUUID(lease.Token)})
		if err != nil {
			return mapErr(err)
		}
		if n != 1 {
			return ErrConflict
		}
		row, err := q.ReadProjectEnvironmentCloneObjectBucket(ctx, tx, sqlc.ReadProjectEnvironmentCloneObjectBucketParams{AccountID: mustPgUUID(op.AccountID), AppID: mustPgUUID(in.Plan.AppID), ID: mustPgUUID(in.Plan.TargetBucketID), EnvironmentCloneOperationID: mustPgUUID(op.ID)})
		if err != nil {
			return mapErr(err)
		}
		bucket = objectBucketFromSQL(row)
		return nil
	})
	return bucket, err
}
