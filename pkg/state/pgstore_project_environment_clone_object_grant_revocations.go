package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/objectstorage/grantrevocation"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ProjectEnvironmentCloneObjectGrantRevocationStore = (*PgStore)(nil)

func cloneObjectGrantRevocationScopeTx(ctx context.Context, tx pgx.Tx, l ProjectEnvironmentCloneLease, sourceID string) (grantrevocation.Scope, ObjectBucket, error) {
	op, err := cloneWriteFenceLeaseTx(ctx, tx, l)
	if err != nil {
		return grantrevocation.Scope{}, ObjectBucket{}, err
	}
	records, err := cloneWorkloadRecordsDB(ctx, tx, op.AccountID, op.ProjectID, op.ID)
	if err != nil {
		return grantrevocation.Scope{}, ObjectBucket{}, err
	}
	views, err := cloneBindingViews(records)
	if err != nil {
		return grantrevocation.Scope{}, ObjectBucket{}, err
	}
	b, err := capturedCloneWriteFenceBucket(op, views, sourceID)
	if err != nil {
		return grantrevocation.Scope{}, b, err
	}
	frozenScope := b.Scope
	b, err = lockObjectMutationBucket(ctx, tx, b)
	if err != nil {
		return grantrevocation.Scope{}, b, err
	}
	if b.Scope != frozenScope {
		return grantrevocation.Scope{}, b, ErrConflict
	}
	if _, err := readOwnedObjectWriteFenceTx(ctx, tx, b, op.ID, op.ID); err != nil {
		return grantrevocation.Scope{}, b, err
	}
	return grantrevocation.Scope{OperationID: op.ID, AccountID: op.AccountID, ProjectID: op.ProjectID, BucketID: b.ID, AppID: b.AppID,
		SourceRevisionHash: op.SourceRevisionHash, SourceScope: b.Scope, BackendID: b.BackendID, BackendFingerprint: b.BackendFingerprint, PhysicalName: b.PhysicalName}, b, nil
}

func cloneObjectGrantRevocationFromSQL(row sqlc.ProjectEnvironmentCloneObjectGrantRevocation, scope grantrevocation.Scope) (ProjectEnvironmentCloneObjectGrantRevocation, error) {
	var p grantrevocation.Plan
	decoder := json.NewDecoder(bytes.NewReader(row.Plan))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&p) != nil || decoder.Decode(new(any)) != io.EOF || p.Scope != scope ||
		pgUUIDString(row.OperationID) != scope.OperationID || pgUUIDString(row.SourceBucketID) != scope.BucketID || pgUUIDString(row.RequestID) != p.RequestID {
		return ProjectEnvironmentCloneObjectGrantRevocation{}, ErrConflict
	}
	hash, err := p.SHA256()
	if err != nil || hash != row.PlanSha256 || !row.RetainedAt.Valid {
		return ProjectEnvironmentCloneObjectGrantRevocation{}, ErrConflict
	}
	return ProjectEnvironmentCloneObjectGrantRevocation{Plan: p, State: row.State, RevocationID: row.RevocationID,
		RetainedAt: row.RetainedAt.Time, RequestStartedAt: row.RequestStartedAt.Time, ObservedAt: row.ObservedAt.Time, DrainedAt: row.DrainedAt.Time}, nil
}

func readCloneObjectGrantRevocationTx(ctx context.Context, tx pgx.Tx, scope grantrevocation.Scope) (ProjectEnvironmentCloneObjectGrantRevocation, error) {
	row, err := sqlc.New().CloneObjectGrantRevocationRead(ctx, tx, sqlc.CloneObjectGrantRevocationReadParams{
		OperationID: mustPgUUID(scope.OperationID), SourceBucketID: mustPgUUID(scope.BucketID)})
	if err != nil {
		return ProjectEnvironmentCloneObjectGrantRevocation{}, mapErr(err)
	}
	return cloneObjectGrantRevocationFromSQL(row, scope)
}

func cloneObjectNativeGrantsTx(ctx context.Context, tx pgx.Tx, b ObjectBucket) ([]string, error) {
	rows, err := sqlc.New().ObjectBucketNativeGrants(ctx, tx, mustPgUUID(b.ID))
	if err != nil {
		return nil, mapErr(err)
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.BackendID != b.BackendID || row.BackendFingerprint != b.BackendFingerprint || row.PhysicalName != b.PhysicalName {
			return nil, ErrConflict
		}
		ids = append(ids, pgUUIDString(row.ID))
	}
	return ids, nil
}

func validateCloneObjectGrantRosterTx(ctx context.Context, tx pgx.Tx, b ObjectBucket, r ProjectEnvironmentCloneObjectGrantRevocation) error {
	ids, err := cloneObjectNativeGrantsTx(ctx, tx, b)
	if err != nil {
		return err
	}
	if r.State == "drained" {
		if len(ids) != 0 {
			return ErrConflict
		}
	} else if !slices.Equal(ids, r.Plan.GrantIDs) {
		return ErrConflict
	}
	return nil
}

// Reauthorize after all source-row waits and again before commit. The SQL
// clock, rather than the caller's lease timestamp, remains authoritative.
func (s *PgStore) cloneObjectGrantRevocationTx(ctx context.Context, l ProjectEnvironmentCloneLease, sourceID string,
	fn func(pgx.Tx, grantrevocation.Scope, ObjectBucket) (ProjectEnvironmentCloneObjectGrantRevocation, error)) (ProjectEnvironmentCloneObjectGrantRevocation, error) {
	if !validCloneObjectFenceLease(l) || !validCloneCredentialSourceID(sourceID) {
		return ProjectEnvironmentCloneObjectGrantRevocation{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectEnvironmentCloneObjectGrantRevocation{}, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	scope, b, err := cloneObjectGrantRevocationScopeTx(ctx, tx, l, sourceID)
	if err == nil {
		err = authorizeCloneObjectMutationTx(ctx, tx, l.Operation, &l)
	}
	var r ProjectEnvironmentCloneObjectGrantRevocation
	if err == nil {
		r, err = fn(tx, scope, b)
	}
	if err == nil {
		err = authorizeCloneObjectMutationTx(ctx, tx, l.Operation, &l)
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		return ProjectEnvironmentCloneObjectGrantRevocation{}, mapErr(err)
	}
	return r, nil
}

func (s *PgStore) ReserveProjectEnvironmentCloneObjectGrantRevocation(ctx context.Context, l ProjectEnvironmentCloneLease, sourceID string) (ProjectEnvironmentCloneObjectGrantRevocation, error) {
	return s.cloneObjectGrantRevocationTx(ctx, l, sourceID, func(tx pgx.Tx, scope grantrevocation.Scope, b ObjectBucket) (ProjectEnvironmentCloneObjectGrantRevocation, error) {
		if l.Operation.Status != CloneOperationCapturing {
			return ProjectEnvironmentCloneObjectGrantRevocation{}, ErrConflict
		}
		r, err := readCloneObjectGrantRevocationTx(ctx, tx, scope)
		if err == nil {
			return r, validateCloneObjectGrantRosterTx(ctx, tx, b, r)
		}
		if !errors.Is(err, ErrNotFound) {
			return r, err
		}
		ids, err := cloneObjectNativeGrantsTx(ctx, tx, b)
		if err != nil {
			return r, err
		}
		if len(ids) == 0 {
			return r, ErrNotFound
		}
		p := grantrevocation.Plan{Scope: scope, RequestID: uuid.NewString(), GrantIDs: ids}
		hash, err := p.SHA256()
		if err != nil {
			return r, ErrConflict
		}
		data, err := json.Marshal(p)
		if err != nil {
			return r, err
		}
		row, err := sqlc.New().CloneObjectGrantRevocationInsert(ctx, tx, sqlc.CloneObjectGrantRevocationInsertParams{
			OperationID: mustPgUUID(scope.OperationID), SourceBucketID: mustPgUUID(b.ID), RequestID: mustPgUUID(p.RequestID), Plan: data, PlanSha256: hash})
		if err != nil {
			return r, mapErr(err)
		}
		return cloneObjectGrantRevocationFromSQL(row, scope)
	})
}

func (s *PgStore) ProjectEnvironmentCloneObjectGrantRevocationForLease(ctx context.Context, l ProjectEnvironmentCloneLease, sourceID string) (ProjectEnvironmentCloneObjectGrantRevocation, error) {
	return s.cloneObjectGrantRevocationTx(ctx, l, sourceID, func(tx pgx.Tx, scope grantrevocation.Scope, b ObjectBucket) (ProjectEnvironmentCloneObjectGrantRevocation, error) {
		r, err := readCloneObjectGrantRevocationTx(ctx, tx, scope)
		if err == nil {
			err = validateCloneObjectGrantRosterTx(ctx, tx, b, r)
		}
		return r, err
	})
}

func (s *PgStore) DispatchProjectEnvironmentCloneObjectGrantRevocation(ctx context.Context, l ProjectEnvironmentCloneLease, p grantrevocation.Plan) (ProjectEnvironmentCloneObjectGrantRevocation, error) {
	hash, err := p.SHA256()
	if err != nil {
		return ProjectEnvironmentCloneObjectGrantRevocation{}, ErrInvalidArgument
	}
	return s.cloneObjectGrantRevocationTx(ctx, l, p.Scope.BucketID, func(tx pgx.Tx, scope grantrevocation.Scope, b ObjectBucket) (ProjectEnvironmentCloneObjectGrantRevocation, error) {
		r, err := readCloneObjectGrantRevocationTx(ctx, tx, scope)
		if err != nil {
			return r, err
		}
		storedHash, _ := r.Plan.SHA256()
		if hash != storedHash || l.Operation.Status == CloneOperationCompensating && r.RequestStartedAt.IsZero() {
			return r, ErrConflict
		}
		if err := validateCloneObjectGrantRosterTx(ctx, tx, b, r); err != nil {
			return r, err
		}
		row, err := sqlc.New().CloneObjectGrantRevocationDispatch(ctx, tx, sqlc.CloneObjectGrantRevocationDispatchParams{
			OperationID: mustPgUUID(scope.OperationID), SourceBucketID: mustPgUUID(b.ID), RequestID: mustPgUUID(p.RequestID), PlanSha256: hash})
		if err != nil {
			return r, mapErr(err)
		}
		return cloneObjectGrantRevocationFromSQL(row, scope)
	})
}

func (s *PgStore) RecordProjectEnvironmentCloneObjectGrantRevocation(ctx context.Context, l ProjectEnvironmentCloneLease, p grantrevocation.Plan, observation grantrevocation.Observation) (ProjectEnvironmentCloneObjectGrantRevocation, error) {
	if observation.Validate(p) != nil {
		return ProjectEnvironmentCloneObjectGrantRevocation{}, ErrInvalidArgument
	}
	return s.cloneObjectGrantRevocationTx(ctx, l, p.Scope.BucketID, func(tx pgx.Tx, scope grantrevocation.Scope, b ObjectBucket) (ProjectEnvironmentCloneObjectGrantRevocation, error) {
		r, err := readCloneObjectGrantRevocationTx(ctx, tx, scope)
		if err != nil {
			return r, err
		}
		if observation.Validate(r.Plan) != nil || r.State == "reserved" || r.RevocationID != "" && r.RevocationID != observation.RevocationID ||
			r.State == "drained" && !observation.Drained() {
			return r, ErrConflict
		}
		if err := validateCloneObjectGrantRosterTx(ctx, tx, b, r); err != nil {
			return r, err
		}
		if observation.Drained() && r.State != "drained" {
			ids := make([]pgtype.UUID, 0, len(r.Plan.GrantIDs))
			for _, id := range r.Plan.GrantIDs {
				ids = append(ids, mustPgUUID(id))
			}
			n, err := sqlc.New().CloneObjectNativeGrantsFinish(ctx, tx, sqlc.CloneObjectNativeGrantsFinishParams{
				BucketID: mustPgUUID(b.ID), GrantIds: ids, BackendID: b.BackendID, BackendFingerprint: b.BackendFingerprint, PhysicalName: b.PhysicalName})
			if err != nil {
				return r, mapErr(err)
			}
			if n != int64(len(ids)) {
				return r, ErrConflict
			}
		}
		row, err := sqlc.New().CloneObjectGrantRevocationObserve(ctx, tx, sqlc.CloneObjectGrantRevocationObserveParams{
			OperationID: mustPgUUID(scope.OperationID), SourceBucketID: mustPgUUID(b.ID), RequestID: mustPgUUID(p.RequestID), PlanSha256: observation.PlanSHA256,
			RevocationID: observation.RevocationID, Drained: observation.Drained()})
		if err != nil {
			return r, mapErr(err)
		}
		return cloneObjectGrantRevocationFromSQL(row, scope)
	})
}
