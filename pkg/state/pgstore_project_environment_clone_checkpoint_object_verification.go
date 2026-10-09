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

var _ ProjectEnvironmentCloneCheckpointObjectVerificationStore = (*PgStore)(nil)

func checkpointObjectVerificationFromSQL(r sqlc.ProjectEnvironmentCloneCheckpointObjectVerification) (ProjectEnvironmentCloneCheckpointObjectVerification, error) {
	in := ProjectEnvironmentCloneCheckpointObjectVerification{State: r.State, RequestedAt: r.RequestedAt.Time.UTC(), ObservationSHA256: r.ObservationSha256.String}
	if json.Unmarshal(r.Plan, &in.Plan) != nil || in.Plan.Target.OperationID != pgUUIDString(r.OperationID) || in.Plan.Target.SourceBucketID != pgUUIDString(r.SourceBucketID) {
		return in, ErrConflict
	}
	return in, nil
}
func readCheckpointObjectVerificationTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, id string) (ProjectEnvironmentCloneCheckpointObjectVerification, error) {
	r, err := sqlc.New().ReadCloneCheckpointObjectVerification(ctx, tx, sqlc.ReadCloneCheckpointObjectVerificationParams{OperationID: mustPgUUID(op.ID), SourceBucketID: mustPgUUID(id)})
	if err != nil {
		return ProjectEnvironmentCloneCheckpointObjectVerification{}, mapErr(err)
	}
	return checkpointObjectVerificationFromSQL(r)
}
func checkpointObjectInventoryPlanTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, l ProjectEnvironmentCloneLease, id string, retention *ProjectEnvironmentCloneCheckpointReleaseObservation) (ProjectEnvironmentCloneCheckpointObjectVerificationPlan, error) {
	var p ProjectEnvironmentCloneCheckpointObjectVerificationPlan
	creation, err := readCheckpointObjectTargetTx(ctx, tx, op, id)
	if err != nil {
		return p, err
	}
	if creation.State != "ready" || !validCloneObjectSHA256(creation.ObservationSHA256) {
		return p, ErrConflict
	}
	bucket, err := validateCheckpointObjectTargetTx(ctx, tx, op, creation, retention, l)
	if err != nil {
		return p, err
	}
	if bucket.PublicRead || bucket.ServeAt != "" {
		return p, ErrConflict
	}
	grants, err := sqlc.New().CloneCheckpointObjectTargetHasGrants(ctx, tx, mustPgUUID(bucket.ID))
	if err != nil {
		return p, mapErr(err)
	}
	if grants {
		owned, err := checkpointObjectPublicationOwnedTx(ctx, tx, op)
		if err != nil {
			return p, err
		}
		if !owned {
			return p, ErrConflict
		}
		publication, err := readCheckpointObjectPublicationTx(ctx, tx, op)
		if err != nil {
			return p, err
		}
		member := false
		for _, b := range publication.Plan.Buckets {
			if b.SourceBucketID == id && b.TargetBucketID == bucket.ID && b.AppID == creation.Plan.AppID {
				member = true
			}
		}
		if !member {
			return p, ErrConflict
		}
	}
	release, err := readCloneCheckpointReleaseTx(ctx, tx, op)
	if err != nil {
		return p, err
	}
	for _, s := range release.Plan.Sources {
		if s.Kind == "object_storage" && s.ID == id && s.AppID == creation.Plan.AppID {
			p.SourceResourceID = s.ProviderResourceID
		}
	}
	if p.SourceResourceID == "" || p.SourceResourceID == creation.Plan.PhysicalName {
		return p, ErrConflict
	}
	manifest, err := projectEnvironmentCloneObjectManifestDB(ctx, tx, op.AccountID, op.ProjectID, op.ID, id)
	if err != nil {
		return p, err
	}
	p.Target, p.CreationSHA256 = creation.Plan, creation.ObservationSHA256
	p.Objects = make([]ProjectEnvironmentCloneObjectVersion, 0, len(manifest.Objects))
	for _, item := range manifest.Objects {
		if item.Source.Size < 0 || item.Source.Size > api.ObjectCheckpointVerificationBytesPerSideMax-p.TotalBytes {
			return p, ErrQuotaExceeded
		}
		p.TotalBytes += item.Source.Size
		p.Objects = append(p.Objects, item.Source)
	}
	hash, err := ProjectEnvironmentCloneObjectManifestHash(p.Objects)
	if err != nil || hash != p.Target.ManifestSHA256 {
		return p, ErrConflict
	}
	encoded, err := json.Marshal(p)
	if err != nil {
		return p, err
	}
	if len(encoded) > api.ObjectCheckpointVerificationPlanMaxBytes {
		return p, ErrQuotaExceeded
	}
	return p, nil
}
func checkpointObjectVerificationPlanTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, l ProjectEnvironmentCloneLease, id string, retention *ProjectEnvironmentCloneCheckpointReleaseObservation) (ProjectEnvironmentCloneCheckpointObjectVerificationPlan, error) {
	p, err := checkpointObjectInventoryPlanTx(ctx, tx, op, l, id, retention)
	if err != nil {
		return p, err
	}
	copy, err := readCheckpointObjectCopyTx(ctx, tx, op, id)
	if err != nil {
		return p, err
	}
	if copy.State != "copied" || !validCloneObjectSHA256(copy.ObservationSHA256) {
		return p, ErrConflict
	}
	if err = validateCheckpointObjectCopyTx(ctx, tx, op, l, copy, retention); err != nil {
		return p, err
	}
	p.CopySHA256 = copy.ObservationSHA256
	return p, nil
}
func validateCheckpointObjectVerificationTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, l ProjectEnvironmentCloneLease, in ProjectEnvironmentCloneCheckpointObjectVerification, retention *ProjectEnvironmentCloneCheckpointReleaseObservation) error {
	p, err := checkpointObjectVerificationPlanTx(ctx, tx, op, l, in.Plan.Target.SourceBucketID, retention)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(p, in.Plan) {
		return ErrConflict
	}
	return nil
}
func (s *PgStore) ReserveProjectEnvironmentCloneCheckpointObjectVerification(ctx context.Context, l ProjectEnvironmentCloneLease, id string) (ProjectEnvironmentCloneCheckpointObjectVerification, error) {
	var out ProjectEnvironmentCloneCheckpointObjectVerification
	if !validCloneCredentialSourceID(id) {
		return out, ErrInvalidArgument
	}
	err := s.checkpointReleaseTx(ctx, l, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		var err error
		out, err = readCheckpointObjectVerificationTx(ctx, tx, op, id)
		if err == nil {
			return validateCheckpointObjectVerificationTx(ctx, tx, op, l, out, nil)
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		p, err := checkpointObjectVerificationPlanTx(ctx, tx, op, l, id, nil)
		if err != nil {
			return err
		}
		encoded, err := json.Marshal(p)
		if err != nil {
			return err
		}
		r, err := sqlc.New().InsertCloneCheckpointObjectVerification(ctx, tx, sqlc.InsertCloneCheckpointObjectVerificationParams{OperationID: mustPgUUID(op.ID), SourceBucketID: mustPgUUID(id), Plan: encoded, ExpectedRevision: l.Operation.Revision, WorkerToken: mustPgUUID(l.Token)})
		if err != nil {
			return mapErr(err)
		}
		out, err = checkpointObjectVerificationFromSQL(r)
		return err
	})
	return out, err
}
func (s *PgStore) ClaimProjectEnvironmentCloneCheckpointObjectVerification(ctx context.Context, l ProjectEnvironmentCloneLease, id string, retention ProjectEnvironmentCloneCheckpointReleaseObservation) (ProjectEnvironmentCloneCheckpointObjectVerification, bool, error) {
	var out ProjectEnvironmentCloneCheckpointObjectVerification
	dispatch := false
	if !validCloneCredentialSourceID(id) {
		return out, false, ErrInvalidArgument
	}
	err := s.checkpointReleaseTx(ctx, l, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		var err error
		out, err = readCheckpointObjectVerificationTx(ctx, tx, op, id)
		if err != nil {
			return err
		}
		if err = validateCheckpointObjectVerificationTx(ctx, tx, op, l, out, &retention); err != nil {
			return err
		}
		if out.State != "planned" {
			return nil
		}
		r, err := sqlc.New().ClaimCloneCheckpointObjectVerification(ctx, tx, sqlc.ClaimCloneCheckpointObjectVerificationParams{OperationID: mustPgUUID(op.ID), SourceBucketID: mustPgUUID(id), ExpectedRevision: l.Operation.Revision, WorkerToken: mustPgUUID(l.Token)})
		if err != nil {
			return mapErr(err)
		}
		out, err = checkpointObjectVerificationFromSQL(r)
		dispatch = err == nil
		return err
	})
	return out, dispatch && err == nil, err
}
func (s *PgStore) FinishProjectEnvironmentCloneCheckpointObjectVerification(ctx context.Context, l ProjectEnvironmentCloneLease, id string, o ProjectEnvironmentCloneCheckpointObjectVerificationObservation, retention ProjectEnvironmentCloneCheckpointReleaseObservation) error {
	if !validCloneCredentialSourceID(id) {
		return ErrInvalidArgument
	}
	return s.checkpointReleaseTx(ctx, l, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		in, err := readCheckpointObjectVerificationTx(ctx, tx, op, id)
		if err != nil {
			return err
		}
		if err = validateCheckpointObjectVerificationTx(ctx, tx, op, l, in, &retention); err != nil {
			return err
		}
		if err = validateCheckpointObjectVerificationObservation(in, o, l.ExpiresAt); err != nil {
			return err
		}
		if in.State == "verified" {
			return nil
		}
		n, err := sqlc.New().FinishCloneCheckpointObjectVerification(ctx, tx, sqlc.FinishCloneCheckpointObjectVerificationParams{OperationID: mustPgUUID(op.ID), SourceBucketID: mustPgUUID(id), ObservationSha256: checkpointObjectVerificationSHA(o), ExpectedRevision: l.Operation.Revision, WorkerToken: mustPgUUID(l.Token)})
		if err != nil {
			return mapErr(err)
		}
		if n != 1 {
			return ErrConflict
		}
		return nil
	})
}
