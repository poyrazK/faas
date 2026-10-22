package state

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ProjectEnvironmentCloneCheckpointObjectCopyStore = (*PgStore)(nil)

func checkpointObjectCopyFromSQL(r sqlc.ProjectEnvironmentCloneCheckpointObjectCopy) (ProjectEnvironmentCloneCheckpointObjectCopy, error) {
	in := ProjectEnvironmentCloneCheckpointObjectCopy{State: r.State, RequestedAt: r.RequestedAt.Time.UTC(), ObservationSHA256: r.ObservationSha256.String}
	if json.Unmarshal(r.Plan, &in.Plan) != nil || in.Plan.Inventory.Target.OperationID != pgUUIDString(r.OperationID) || in.Plan.Inventory.Target.SourceBucketID != pgUUIDString(r.SourceBucketID) {
		return in, ErrConflict
	}
	return in, nil
}
func readCheckpointObjectCopyTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, id string) (ProjectEnvironmentCloneCheckpointObjectCopy, error) {
	r, err := sqlc.New().ReadCloneCheckpointObjectCopy(ctx, tx, sqlc.ReadCloneCheckpointObjectCopyParams{OperationID: mustPgUUID(op.ID), SourceBucketID: mustPgUUID(id)})
	if err != nil {
		return ProjectEnvironmentCloneCheckpointObjectCopy{}, mapErr(err)
	}
	return checkpointObjectCopyFromSQL(r)
}
func checkpointObjectCopyPlanTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, l ProjectEnvironmentCloneLease, id string, retention *ProjectEnvironmentCloneCheckpointReleaseObservation) (ProjectEnvironmentCloneCheckpointObjectCopyPlan, error) {
	var out ProjectEnvironmentCloneCheckpointObjectCopyPlan
	// A comparison reservation freezes the target. Never open a writer afterwards.
	_, err := readCheckpointObjectVerificationTx(ctx, tx, op, id)
	if !errors.Is(err, ErrNotFound) {
		if err == nil {
			err = ErrConflict
		}
		return out, err
	}
	out.Inventory, err = checkpointObjectInventoryPlanTx(ctx, tx, op, l, id, retention)
	out.WriteUntil = l.ExpiresAt.UTC().Truncate(time.Microsecond)
	return out, err
}
func validateCheckpointObjectCopyTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, l ProjectEnvironmentCloneLease, in ProjectEnvironmentCloneCheckpointObjectCopy, retention *ProjectEnvironmentCloneCheckpointReleaseObservation) error {
	p, err := checkpointObjectInventoryPlanTx(ctx, tx, op, l, in.Plan.Inventory.Target.SourceBucketID, retention)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(p, in.Plan.Inventory) || in.Plan.WriteUntil.IsZero() || in.Plan.WriteUntil.Location() != time.UTC || in.Plan.WriteUntil.Nanosecond()%1000 != 0 {
		return ErrConflict
	}
	return nil
}
func (s *PgStore) ReserveProjectEnvironmentCloneCheckpointObjectCopy(ctx context.Context, l ProjectEnvironmentCloneLease, id string) (ProjectEnvironmentCloneCheckpointObjectCopy, error) {
	var out ProjectEnvironmentCloneCheckpointObjectCopy
	if !validCloneCredentialSourceID(id) {
		return out, ErrInvalidArgument
	}
	err := s.checkpointReleaseTx(ctx, l, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		var err error
		out, err = readCheckpointObjectCopyTx(ctx, tx, op, id)
		if err == nil {
			return validateCheckpointObjectCopyTx(ctx, tx, op, l, out, nil)
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		p, err := checkpointObjectCopyPlanTx(ctx, tx, op, l, id, nil)
		if err != nil {
			return err
		}
		encoded, err := json.Marshal(p)
		if err != nil {
			return err
		}
		if len(encoded) > api.ObjectCheckpointVerificationPlanMaxBytes {
			return ErrQuotaExceeded
		}
		r, err := sqlc.New().InsertCloneCheckpointObjectCopy(ctx, tx, sqlc.InsertCloneCheckpointObjectCopyParams{OperationID: mustPgUUID(op.ID), SourceBucketID: mustPgUUID(id), Plan: encoded, ExpectedRevision: l.Operation.Revision, WorkerToken: mustPgUUID(l.Token)})
		if err != nil {
			return mapErr(err)
		}
		out, err = checkpointObjectCopyFromSQL(r)
		return err
	})
	return out, err
}
func (s *PgStore) ClaimProjectEnvironmentCloneCheckpointObjectCopy(ctx context.Context, l ProjectEnvironmentCloneLease, id string, retention ProjectEnvironmentCloneCheckpointReleaseObservation) (ProjectEnvironmentCloneCheckpointObjectCopy, bool, error) {
	var out ProjectEnvironmentCloneCheckpointObjectCopy
	dispatch := false
	if !validCloneCredentialSourceID(id) {
		return out, false, ErrInvalidArgument
	}
	err := s.checkpointReleaseTx(ctx, l, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		var err error
		out, err = readCheckpointObjectCopyTx(ctx, tx, op, id)
		if err != nil {
			return err
		}
		if err = validateCheckpointObjectCopyTx(ctx, tx, op, l, out, &retention); err != nil {
			return err
		}
		if out.State != "planned" {
			return nil
		}
		if !out.Plan.WriteUntil.Equal(l.ExpiresAt.UTC().Truncate(time.Microsecond)) || !out.Plan.WriteUntil.After(time.Now()) {
			return ErrConflict
		}
		r, err := sqlc.New().ClaimCloneCheckpointObjectCopy(ctx, tx, sqlc.ClaimCloneCheckpointObjectCopyParams{OperationID: mustPgUUID(op.ID), SourceBucketID: mustPgUUID(id), ExpectedRevision: l.Operation.Revision, WorkerToken: mustPgUUID(l.Token)})
		if err != nil {
			return mapErr(err)
		}
		out, err = checkpointObjectCopyFromSQL(r)
		dispatch = err == nil
		return err
	})
	return out, dispatch && err == nil, err
}
func (s *PgStore) FinishProjectEnvironmentCloneCheckpointObjectCopy(ctx context.Context, l ProjectEnvironmentCloneLease, id string, o ProjectEnvironmentCloneCheckpointObjectCopyObservation, retention ProjectEnvironmentCloneCheckpointReleaseObservation) error {
	if !validCloneCredentialSourceID(id) {
		return ErrInvalidArgument
	}
	return s.checkpointReleaseTx(ctx, l, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		in, err := readCheckpointObjectCopyTx(ctx, tx, op, id)
		if err != nil {
			return err
		}
		if err = validateCheckpointObjectCopyTx(ctx, tx, op, l, in, &retention); err != nil {
			return err
		}
		if err = validateCheckpointObjectCopyObservation(in, o, l.ExpiresAt); err != nil {
			return err
		}
		if in.State == "copied" {
			return nil
		}
		n, err := sqlc.New().FinishCloneCheckpointObjectCopy(ctx, tx, sqlc.FinishCloneCheckpointObjectCopyParams{OperationID: mustPgUUID(op.ID), SourceBucketID: mustPgUUID(id), ObservationSha256: checkpointObjectCopySHA(o), ExpectedRevision: l.Operation.Revision, WorkerToken: mustPgUUID(l.Token)})
		if err != nil {
			return mapErr(err)
		}
		if n != 1 {
			return ErrConflict
		}
		return nil
	})
}
