package state

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ProjectEnvironmentCloneCheckpointObjectRetirementStore = (*PgStore)(nil)

func (s *PgStore) checkpointObjectRetirementTx(ctx context.Context, l ProjectEnvironmentCloneLease, id string, work func(context.Context, pgx.Tx, ProjectEnvironmentCloneOperation, ProjectEnvironmentCloneCheckpointObjectRetirementPlan, ObjectBucket) error) error {
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
	if _, err = sqlc.New().LockProjectEnvironmentCloneApps(ctx, tx, sqlc.LockProjectEnvironmentCloneAppsParams{AccountID: mustPgUUID(op.AccountID), ProjectID: mustPgUUID(op.ProjectID)}); err != nil {
		return mapErr(err)
	}
	creation, err := readCheckpointObjectTargetTx(ctx, tx, op, id)
	// Only absence of the original creation journal may mean no native request
	// was authorized. Missing release/manifest/bucket/publication dependencies
	// after this point are corruption, never permission to skip cleanup.
	if err != nil {
		return err
	}
	p, bucket, err := checkpointObjectRetirementPlanTx(ctx, tx, op, creation)
	if errors.Is(err, ErrNotFound) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	if err = work(ctx, tx, op, p, bucket); err != nil {
		return err
	}
	if err = authorizeCloneObjectMutationTx(ctx, tx, op, &l); err != nil {
		return err
	}
	return mapErr(tx.Commit(ctx))
}

func checkpointObjectRetirementPlanTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, creation ProjectEnvironmentCloneCheckpointObjectTarget) (ProjectEnvironmentCloneCheckpointObjectRetirementPlan, ObjectBucket, error) {
	p := ProjectEnvironmentCloneCheckpointObjectRetirementPlan{Creation: creation}
	var bucket ObjectBucket
	release, err := readCloneCheckpointReleaseTx(ctx, tx, op)
	if err != nil {
		return p, bucket, err
	}
	if err = validateCloneCheckpointReleaseJournalsWithRetiredTargetsTx(ctx, tx, op, release, false, true); err != nil {
		return p, bucket, err
	}
	c := creation.Plan
	for _, s := range release.Plan.Sources {
		if s.Kind == "object_storage" && s.ID == c.SourceBucketID && s.AppID == c.AppID {
			p.SourceResourceID = s.ProviderResourceID
		}
	}
	manifest, err := projectEnvironmentCloneObjectManifestDB(ctx, tx, op.AccountID, op.ProjectID, op.ID, c.SourceBucketID)
	if err != nil {
		return p, bucket, err
	}
	if manifest.TargetBucketID != c.TargetBucketID {
		return p, bucket, ErrConflict
	}
	row, err := readCloneCheckpointReleaseTargetTx(ctx, tx, op, c.AppID, c.TargetBucketID, true)
	if err != nil {
		return p, bucket, mapErr(err)
	}
	bucket = objectBucketFromSQL(row)
	expected := ProjectEnvironmentCloneCheckpointObjectTargetPlan{OperationID: op.ID, AccountID: op.AccountID, AppID: c.AppID, SourceBucketID: c.SourceBucketID, TargetBucketID: manifest.TargetBucketID, BackendID: bucket.BackendID, BackendFingerprint: bucket.BackendFingerprint, Region: bucket.Region, PhysicalName: bucket.PhysicalName, ManifestSHA256: manifest.Hash, CheckpointID: release.Receipt.CheckpointID}
	if expected != c || bucket.LeaseToken != "" || !bucket.LeaseUntil.IsZero() {
		return p, bucket, ErrConflict
	}
	if err = checkpointObjectRetirementWithdrawalTx(ctx, tx, op, c); err != nil {
		return p, bucket, err
	}
	copy, err := readCheckpointObjectCopyTx(ctx, tx, op, c.SourceBucketID)
	if !errors.Is(err, ErrNotFound) {
		if err != nil {
			return p, bucket, err
		}
		inventory := ProjectEnvironmentCloneCheckpointObjectVerificationPlan{Target: c, CreationSHA256: creation.ObservationSHA256, SourceResourceID: p.SourceResourceID, Objects: make([]ProjectEnvironmentCloneObjectVersion, 0, len(manifest.Objects))}
		for _, item := range manifest.Objects {
			if item.Source.Size < 0 || item.Source.Size > api.ObjectCheckpointVerificationBytesPerSideMax-inventory.TotalBytes {
				return p, bucket, ErrQuotaExceeded
			}
			inventory.Objects = append(inventory.Objects, item.Source)
			inventory.TotalBytes += item.Source.Size
		}
		if !reflect.DeepEqual(copy.Plan.Inventory, inventory) {
			return p, bucket, ErrConflict
		}
		p.Copy = &copy
	}
	if err = validateCheckpointObjectRetirementPlan(p); err != nil {
		return p, bucket, err
	}
	if err = checkpointObjectRetirementVerificationTx(ctx, tx, op, p); err != nil {
		return p, bucket, err
	}
	return p, bucket, nil
}

// A saved comparison is also an immutable witness of completed original copy.
// Do not accept a rewound/replaced copy journal after comparison reservation.
func checkpointObjectRetirementVerificationTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, p ProjectEnvironmentCloneCheckpointObjectRetirementPlan) error {
	in, err := readCheckpointObjectVerificationTx(ctx, tx, op, p.Creation.Plan.SourceBucketID)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if p.Copy == nil || p.Copy.State != "copied" || !validCloneObjectSHA256(p.Copy.ObservationSHA256) {
		return ErrConflict
	}
	expected := p.Copy.Plan.Inventory
	expected.CopySHA256 = p.Copy.ObservationSHA256
	if !reflect.DeepEqual(in.Plan, expected) || !checkpointObjectRetirementJournalShape(in.State, "verified", in.RequestedAt, in.ObservationSHA256) {
		return ErrConflict
	}
	return nil
}

// Owned revoked audit rows are permitted only after exact whole-matrix local
// withdrawal. Foreign credentials (even revoked) and surviving grants fail closed.
func checkpointObjectRetirementWithdrawalTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, c ProjectEnvironmentCloneCheckpointObjectTargetPlan) error {
	// Distinguish an absent publication from missing dependencies of an existing
	// journal. Both can surface as ErrNotFound from the authenticated reader.
	_, err := sqlc.New().ReadCloneCheckpointObjectPublication(ctx, tx, mustPgUUID(op.ID))
	if errors.Is(mapErr(err), ErrNotFound) {
		grants, err := sqlc.New().CloneCheckpointObjectTargetHasGrants(ctx, tx, mustPgUUID(c.TargetBucketID))
		if err != nil {
			return mapErr(err)
		}
		if grants {
			return ErrConflict
		}
		return nil
	}
	if err != nil {
		return mapErr(err)
	}
	in, err := readCheckpointObjectPublicationTx(ctx, tx, op)
	if err != nil {
		return err
	}
	if in.State != "withdrawn" {
		return ErrConflict
	}
	if err = checkpointObjectPublicationRowsTx(ctx, tx, in); err != nil {
		return err
	}
	for _, b := range in.Plan.Buckets {
		if b.SourceBucketID == c.SourceBucketID && b.TargetBucketID == c.TargetBucketID && b.AppID == c.AppID {
			return nil
		}
	}
	return ErrConflict
}

func checkpointObjectRetirementFromSQL(r sqlc.ProjectEnvironmentCloneCheckpointObjectRetirement, p ProjectEnvironmentCloneCheckpointObjectRetirementPlan) (ProjectEnvironmentCloneCheckpointObjectRetirement, error) {
	in := ProjectEnvironmentCloneCheckpointObjectRetirement{State: r.State, RequestedAt: r.RequestedAt.Time.UTC(), ObservationSHA256: r.ObservationSha256.String}
	if json.Unmarshal(r.Plan, &in.Plan) != nil || pgUUIDString(r.OperationID) != p.Creation.Plan.OperationID || pgUUIDString(r.SourceBucketID) != p.Creation.Plan.SourceBucketID || !reflect.DeepEqual(in.Plan, p) || validateCheckpointObjectRetirementPlan(in.Plan) != nil || !checkpointObjectRetirementJournalShape(in.State, "retired", in.RequestedAt, in.ObservationSHA256) {
		return in, ErrConflict
	}
	if (in.State != "planned") != r.RequestedAt.Valid || (in.State == "retired") != r.RetiredAt.Valid || (in.State == "retired") != r.ObservationSha256.Valid || (in.State == "retired") != (len(r.Observation) > 0) {
		return in, ErrConflict
	}
	if in.State == "retired" && (json.Unmarshal(r.Observation, &in.Observation) != nil || validateCheckpointObjectRetirementObservation(in, in.Observation) != nil) {
		return in, ErrConflict
	}
	return in, nil
}
func readCheckpointObjectRetirementTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, p ProjectEnvironmentCloneCheckpointObjectRetirementPlan, bucket ObjectBucket) (ProjectEnvironmentCloneCheckpointObjectRetirement, error) {
	r, err := sqlc.New().ReadCloneCheckpointObjectRetirement(ctx, tx, sqlc.ReadCloneCheckpointObjectRetirementParams{OperationID: mustPgUUID(op.ID), SourceBucketID: mustPgUUID(p.Creation.Plan.SourceBucketID)})
	if err != nil {
		return ProjectEnvironmentCloneCheckpointObjectRetirement{}, mapErr(err)
	}
	in, err := checkpointObjectRetirementFromSQL(r, p)
	if err != nil {
		return in, err
	}
	if (in.State == "retired") != (bucket.State == "deleted") || bucket.State != "deleting" && bucket.State != "deleted" && (p.Creation.State == "ready") != (bucket.State == "ready") {
		return in, ErrConflict
	}
	return in, nil
}

func (s *PgStore) ReserveProjectEnvironmentCloneCheckpointObjectRetirement(ctx context.Context, l ProjectEnvironmentCloneLease, id string) (ProjectEnvironmentCloneCheckpointObjectRetirement, error) {
	var out ProjectEnvironmentCloneCheckpointObjectRetirement
	err := s.checkpointObjectRetirementTx(ctx, l, id, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, p ProjectEnvironmentCloneCheckpointObjectRetirementPlan, bucket ObjectBucket) error {
		var err error
		out, err = readCheckpointObjectRetirementTx(ctx, tx, op, p, bucket)
		if err == nil {
			return nil
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		if bucket.State == "deleted" || bucket.State != "deleting" && (p.Creation.State == "ready") != (bucket.State == "ready") {
			return ErrConflict
		}
		encoded, err := json.Marshal(p)
		if err != nil {
			return err
		}
		if len(encoded) > api.ObjectCheckpointRetirementPlanMaxBytes {
			return ErrQuotaExceeded
		}
		r, err := sqlc.New().InsertCloneCheckpointObjectRetirement(ctx, tx, sqlc.InsertCloneCheckpointObjectRetirementParams{OperationID: mustPgUUID(op.ID), SourceBucketID: mustPgUUID(id), Plan: encoded, ExpectedRevision: op.Revision, WorkerToken: mustPgUUID(l.Token)})
		if err != nil {
			return mapErr(err)
		}
		out, err = checkpointObjectRetirementFromSQL(r, p)
		return err
	})
	if err != nil {
		return ProjectEnvironmentCloneCheckpointObjectRetirement{}, err
	}
	return out, nil
}
func (s *PgStore) ClaimProjectEnvironmentCloneCheckpointObjectRetirement(ctx context.Context, l ProjectEnvironmentCloneLease, id string) (ProjectEnvironmentCloneCheckpointObjectRetirement, bool, error) {
	var out ProjectEnvironmentCloneCheckpointObjectRetirement
	dispatch := false
	err := s.checkpointObjectRetirementTx(ctx, l, id, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, p ProjectEnvironmentCloneCheckpointObjectRetirementPlan, bucket ObjectBucket) error {
		var err error
		out, err = readCheckpointObjectRetirementTx(ctx, tx, op, p, bucket)
		if err != nil || out.State != "planned" {
			return err
		}
		r, err := sqlc.New().ClaimCloneCheckpointObjectRetirement(ctx, tx, sqlc.ClaimCloneCheckpointObjectRetirementParams{OperationID: mustPgUUID(op.ID), SourceBucketID: mustPgUUID(id), ExpectedRevision: op.Revision, WorkerToken: mustPgUUID(l.Token)})
		if err != nil {
			return mapErr(err)
		}
		out, err = checkpointObjectRetirementFromSQL(r, p)
		dispatch = err == nil
		return err
	})
	// Commit errors, including lost acknowledgements, convey no dispatch authority.
	if err != nil {
		return ProjectEnvironmentCloneCheckpointObjectRetirement{}, false, err
	}
	return out, dispatch, nil
}
func (s *PgStore) FinishProjectEnvironmentCloneCheckpointObjectRetirement(ctx context.Context, l ProjectEnvironmentCloneLease, id string, o ProjectEnvironmentCloneCheckpointObjectRetirementObservation) error {
	return s.checkpointObjectRetirementTx(ctx, l, id, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, p ProjectEnvironmentCloneCheckpointObjectRetirementPlan, bucket ObjectBucket) error {
		in, err := readCheckpointObjectRetirementTx(ctx, tx, op, p, bucket)
		if err != nil {
			return err
		}
		if err = validateCheckpointObjectRetirementObservation(in, o); err != nil {
			return err
		}
		if in.State == "retired" {
			return nil
		}
		encoded, err := json.Marshal(o)
		if err != nil {
			return err
		}
		q := sqlc.New()
		n, err := q.FinishCloneCheckpointObjectRetirement(ctx, tx, sqlc.FinishCloneCheckpointObjectRetirementParams{OperationID: mustPgUUID(op.ID), SourceBucketID: mustPgUUID(id), Observation: encoded, ObservationSha256: cloneCheckpointSHA(o), ExpectedRevision: op.Revision, WorkerToken: mustPgUUID(l.Token)})
		if err != nil {
			return mapErr(err)
		}
		if n != 1 {
			return ErrConflict
		}
		n, err = q.FinishCloneCheckpointObjectRetirementBucket(ctx, tx, sqlc.FinishCloneCheckpointObjectRetirementBucketParams{OperationID: mustPgUUID(op.ID), SourceBucketID: mustPgUUID(id), TargetBucketID: mustPgUUID(p.Creation.Plan.TargetBucketID), ExpectedRevision: op.Revision, WorkerToken: mustPgUUID(l.Token)})
		if err != nil {
			return mapErr(err)
		}
		if n != 1 {
			return ErrConflict
		}
		return nil
	})
}
