package state

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ReleaseProjectEnvironmentCloneCheckpointUnusedObjectReservation completes only
// an authenticated private reservation that never acquired a creation journal.
// Even a planned native creation belongs to the qualified retirement protocol.
// This deliberately requires the original released checkpoint: incomplete
// capture compensation needs a separate proof of reservation provenance.
func (s *PgStore) ReleaseProjectEnvironmentCloneCheckpointUnusedObjectReservation(ctx context.Context, l ProjectEnvironmentCloneLease, id string) error {
	if !validCloneLeaseIdentity(l) || !validCloneCredentialSourceID(id) {
		return ErrInvalidArgument
	}
	ctx, cancel := context.WithDeadline(ctx, l.ExpiresAt)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	op, err := lockCloneWorkloadOperationTx(ctx, tx, l.Operation.AccountID, l.Operation.ProjectID, l.Operation.ID)
	if err != nil {
		return err
	}
	if op.Status != CloneOperationCompensating {
		return ErrConflict
	}
	if err = authorizeCloneObjectMutationTx(ctx, tx, op, &l); err != nil {
		return err
	}
	q := sqlc.New()
	if _, err = q.LockProjectEnvironmentCloneApps(ctx, tx, sqlc.LockProjectEnvironmentCloneAppsParams{AccountID: mustPgUUID(op.AccountID), ProjectID: mustPgUUID(op.ProjectID)}); err != nil {
		return mapErr(err)
	}
	if _, err = readCheckpointObjectTargetTx(ctx, tx, op, id); !errors.Is(err, ErrNotFound) {
		if err != nil {
			return err
		}
		return ErrConflict
	}
	release, err := readCloneCheckpointReleaseTx(ctx, tx, op)
	if err != nil {
		return ErrConflict
	}
	if err = validateCloneCheckpointReleaseJournalsWithRetiredTargetsTx(ctx, tx, op, release, false, true); err != nil {
		return err
	}
	records, err := cloneWorkloadRecordsDB(ctx, tx, op.AccountID, op.ProjectID, op.ID)
	if err != nil {
		return err
	}
	views, err := cloneBindingViews(records)
	if err != nil {
		return err
	}
	var appID string
	for _, v := range views {
		for _, b := range v.Buckets {
			if b.ID == id {
				if appID != "" {
					return ErrConflict
				}
				appID = v.AppID
			}
		}
	}
	if appID == "" {
		return ErrConflict
	}
	want, source, err := capturedCloneBucketReservation(op, views, appID, id)
	if err != nil {
		return err
	}
	manifest, err := projectEnvironmentCloneObjectManifestDB(ctx, tx, op.AccountID, op.ProjectID, op.ID, id)
	if err != nil {
		return ErrConflict
	}
	row, err := readCloneCheckpointReleaseTargetTx(ctx, tx, op, appID, manifest.TargetBucketID, true)
	if err != nil {
		return ErrConflict
	}
	bucket := objectBucketFromSQL(row)
	if row.LeaseToken.Valid || row.LeaseUntil.Valid {
		return ErrConflict
	}
	want.ID = bucket.ID
	want.PhysicalName = "gregale-" + strings.ReplaceAll(bucket.ID, "-", "")
	placement := bucket
	if placement.State == "deleted" {
		placement.State = "provisioning"
	}
	if validateCloneBucketReservation(want, placement, source) != nil || (bucket.State != "provisioning" && bucket.State != "deleted") {
		return ErrConflict
	}
	// Missing native authority is not enough if local access was ever published.
	if err = checkpointObjectRetirementWithdrawalTx(ctx, tx, op, ProjectEnvironmentCloneCheckpointObjectTargetPlan{SourceBucketID: id, TargetBucketID: bucket.ID, AppID: appID}); err != nil {
		return err
	}
	saved, err := q.ReadCloneCheckpointUnusedObjectReservation(ctx, tx, sqlc.ReadCloneCheckpointUnusedObjectReservationParams{OperationID: mustPgUUID(op.ID), SourceBucketID: mustPgUUID(id)})
	switch {
	case err == nil:
		var original ObjectBucket
		if json.Unmarshal(saved.Reservation, &original) != nil || !reflect.DeepEqual(original, want) || pgUUIDString(saved.TargetBucketID) != bucket.ID || !saved.CompletedAt.Valid || bucket.State != "deleted" {
			return ErrConflict
		}
	case errors.Is(mapErr(err), ErrNotFound):
		if bucket.State != "provisioning" {
			return ErrConflict
		}
		raw, err := json.Marshal(want)
		if err != nil {
			return ErrInvalidArgument
		}
		n, err := q.CompleteCloneCheckpointUnusedObjectReservation(ctx, tx, sqlc.CompleteCloneCheckpointUnusedObjectReservationParams{OperationID: mustPgUUID(op.ID), SourceBucketID: mustPgUUID(id), TargetBucketID: mustPgUUID(bucket.ID), Reservation: raw, ExpectedRevision: op.Revision, WorkerToken: mustPgUUID(l.Token)})
		if err != nil {
			return mapErr(err)
		}
		if n != 1 {
			return ErrConflict
		}
		n, err = q.DeleteCloneCheckpointUnusedObjectReservationBucket(ctx, tx, sqlc.DeleteCloneCheckpointUnusedObjectReservationBucketParams{OperationID: mustPgUUID(op.ID), SourceBucketID: mustPgUUID(id), TargetBucketID: mustPgUUID(bucket.ID), ExpectedRevision: op.Revision, WorkerToken: mustPgUUID(l.Token)})
		if err != nil {
			return mapErr(err)
		}
		if n != 1 {
			return ErrConflict
		}
	default:
		return mapErr(err)
	}
	if err = authorizeCloneObjectMutationTx(ctx, tx, op, &l); err != nil {
		return err
	}
	return mapErr(tx.Commit(ctx))
}
