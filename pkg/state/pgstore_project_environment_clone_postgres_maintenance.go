package state

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ProjectEnvironmentClonePostgresMaintenanceStore = (*PgStore)(nil)

func clonePostgresMaintenanceFromSQL(row sqlc.ManagedPostgresCheckpointMaintenance) ProjectEnvironmentClonePostgresMaintenance {
	return ProjectEnvironmentClonePostgresMaintenance{ID: pgUUIDString(row.ID), SourceDatabaseID: pgUUIDString(row.SourceDatabaseID),
		ReservedByOperationID: pgUUIDString(row.ReservedByOperationID), BackendID: row.BackendID, BackendFingerprint: row.BackendFingerprint,
		SourceProviderResourceID: row.SourceProviderResourceID, SourceDataResourceID: row.SourceDataResourceID, State: row.State,
		OwnerOID: uint32(row.OwnerOid.Int64), DatabaseOID: uint32(row.DatabaseOid.Int64), RoleRequestedAt: row.RoleRequestedAt.Time,
		DatabaseRequestedAt: row.DatabaseRequestedAt.Time, ActivationRequestedAt: row.ActivationRequestedAt.Time, ReadyAt: row.ReadyAt.Time, CreatedAt: row.CreatedAt.Time}
}

// The source recovery hold is mandatory. Its lock order agrees with source
// deletion: operation, fence, source, maintenance. A replacement worker keeps
// the original private UUID and pins; another operation needs its own fence.
func clonePostgresMaintenanceTx(ctx context.Context, tx pgx.Tx, lease ProjectEnvironmentCloneLease, sourceID string) (ProjectEnvironmentCloneOperation, ProjectEnvironmentClonePostgresWriteFence, error) {
	op, err := cloneWriteFenceLeaseTx(ctx, tx, lease)
	if err != nil {
		return op, ProjectEnvironmentClonePostgresWriteFence{}, err
	}
	fence, err := readClonePostgresWriteFence(ctx, tx, op, sourceID)
	if err != nil {
		return op, fence, err
	}
	if op.Status == CloneOperationCapturing && fence.State != "held" || op.Status == CloneOperationCompensating && fence.State != "abandoning" {
		return op, fence, ErrConflict
	}
	return op, fence, lockClonePostgresFenceSource(ctx, tx, op, fence)
}

func readClonePostgresMaintenanceTx(ctx context.Context, tx pgx.Tx, fence ProjectEnvironmentClonePostgresWriteFence) (ProjectEnvironmentClonePostgresMaintenance, error) {
	row, err := sqlc.New().ReadClonePostgresMaintenance(ctx, tx, mustPgUUID(fence.SourceDatabaseID))
	if err != nil {
		return ProjectEnvironmentClonePostgresMaintenance{}, mapErr(err)
	}
	r := clonePostgresMaintenanceFromSQL(row)
	if r.BackendID != fence.BackendID || r.BackendFingerprint != fence.BackendFingerprint || r.SourceProviderResourceID != fence.SourceProviderResourceID ||
		r.SourceDataResourceID != fence.SourceDataResourceID || r.SourceDatabaseID != fence.SourceDatabaseID {
		return r, ErrConflict
	}
	return r, nil
}

func (s *PgStore) ReserveProjectEnvironmentClonePostgresMaintenance(ctx context.Context, lease ProjectEnvironmentCloneLease, sourceID string) (ProjectEnvironmentClonePostgresMaintenance, error) {
	return s.readOrReserveClonePostgresMaintenance(ctx, lease, sourceID, true)
}

func (s *PgStore) ProjectEnvironmentClonePostgresMaintenanceForLease(ctx context.Context, lease ProjectEnvironmentCloneLease, sourceID string) (ProjectEnvironmentClonePostgresMaintenance, error) {
	return s.readOrReserveClonePostgresMaintenance(ctx, lease, sourceID, false)
}

func (s *PgStore) readOrReserveClonePostgresMaintenance(ctx context.Context, lease ProjectEnvironmentCloneLease, sourceID string, reserve bool) (ProjectEnvironmentClonePostgresMaintenance, error) {
	if !validClonePostgresFenceLease(lease) || !validCloneCredentialSourceID(sourceID) {
		return ProjectEnvironmentClonePostgresMaintenance{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectEnvironmentClonePostgresMaintenance{}, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	op, fence, err := clonePostgresMaintenanceTx(ctx, tx, lease, sourceID)
	if err != nil {
		return ProjectEnvironmentClonePostgresMaintenance{}, err
	}
	r, err := readClonePostgresMaintenanceTx(ctx, tx, fence)
	if reserve && errors.Is(err, ErrNotFound) {
		row, insertErr := sqlc.New().InsertClonePostgresMaintenance(ctx, tx, sqlc.InsertClonePostgresMaintenanceParams{
			OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(sourceID), ExpectedRevision: op.Revision, WorkerToken: lease.Token})
		if insertErr != nil {
			return r, cloneMaintenanceSQLError(insertErr)
		}
		r, err = clonePostgresMaintenanceFromSQL(row), nil
	}
	if err != nil {
		return r, err
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, op, &lease); err != nil {
		return r, err
	}
	return r, mapErr(tx.Commit(ctx))
}

func (s *PgStore) ClaimProjectEnvironmentClonePostgresMaintenanceDispatch(ctx context.Context, lease ProjectEnvironmentCloneLease, sourceID, phase string) (ProjectEnvironmentClonePostgresMaintenance, bool, error) {
	before, requested, complete := postgresMaintenancePhase(phase)
	if !validClonePostgresFenceLease(lease) || !validCloneCredentialSourceID(sourceID) || before == "" {
		return ProjectEnvironmentClonePostgresMaintenance{}, false, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectEnvironmentClonePostgresMaintenance{}, false, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	op, fence, err := clonePostgresMaintenanceTx(ctx, tx, lease, sourceID)
	if err != nil {
		return ProjectEnvironmentClonePostgresMaintenance{}, false, err
	}
	r, err := readClonePostgresMaintenanceTx(ctx, tx, fence)
	if err != nil {
		return r, false, err
	}
	claimed := false
	if r.State == before {
		row, claimErr := sqlc.New().ClaimClonePostgresMaintenanceDispatch(ctx, tx, sqlc.ClaimClonePostgresMaintenanceDispatchParams{
			OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(sourceID), ExpectedRevision: op.Revision, WorkerToken: lease.Token,
			BeforeState: before, RequestedState: requested})
		if claimErr != nil {
			return r, false, cloneMaintenanceSQLError(claimErr)
		}
		r, claimed = clonePostgresMaintenanceFromSQL(row), true
	} else if r.State != requested && postgresMaintenanceStateOrder(r.State) < postgresMaintenanceStateOrder(complete) {
		return r, false, ErrConflict
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, op, &lease); err != nil {
		return r, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return r, false, mapErr(err)
	}
	return r, claimed, nil
}

func (s *PgStore) RecordProjectEnvironmentClonePostgresMaintenance(ctx context.Context, lease ProjectEnvironmentCloneLease, sourceID, phase string, observation ProjectEnvironmentClonePostgresMaintenanceObservation) (ProjectEnvironmentClonePostgresMaintenance, error) {
	_, requested, complete := postgresMaintenancePhase(phase)
	if !validClonePostgresFenceLease(lease) || !validCloneCredentialSourceID(sourceID) || requested == "" {
		return ProjectEnvironmentClonePostgresMaintenance{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectEnvironmentClonePostgresMaintenance{}, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	op, fence, err := clonePostgresMaintenanceTx(ctx, tx, lease, sourceID)
	if err != nil {
		return ProjectEnvironmentClonePostgresMaintenance{}, err
	}
	r, err := readClonePostgresMaintenanceTx(ctx, tx, fence)
	if err != nil {
		return r, err
	}
	if !cloneMaintenanceObservationMatches(r, phase, observation) {
		return r, ErrConflict
	}
	if r.State == requested {
		row, recordErr := sqlc.New().RecordClonePostgresMaintenance(ctx, tx, sqlc.RecordClonePostgresMaintenanceParams{
			OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(sourceID), ExpectedRevision: op.Revision, WorkerToken: lease.Token,
			RequestedState: requested, CompleteState: complete, OwnerOid: int64(observation.OwnerOID),
			DatabaseOid: pgtype.Int8{Int64: int64(observation.DatabaseOID), Valid: observation.DatabaseOID != 0}})
		if recordErr != nil {
			return r, cloneMaintenanceSQLError(recordErr)
		}
		r = clonePostgresMaintenanceFromSQL(row)
	} else if postgresMaintenanceStateOrder(r.State) < postgresMaintenanceStateOrder(complete) {
		return r, ErrConflict
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, op, &lease); err != nil {
		return r, err
	}
	return r, mapErr(tx.Commit(ctx))
}

func cloneMaintenanceObservationMatches(r ProjectEnvironmentClonePostgresMaintenance, phase string, o ProjectEnvironmentClonePostgresMaintenanceObservation) bool {
	if o.OwnerToken != r.ID || o.SourceDataResourceID != r.SourceDataResourceID || o.OwnerOID == 0 ||
		r.OwnerOID != 0 && r.OwnerOID != o.OwnerOID || r.DatabaseOID != 0 && phase != "role" && r.DatabaseOID != o.DatabaseOID {
		return false
	}
	return phase == "role" && o.State == "reserved" && o.DatabaseOID == 0 ||
		phase == "database" && o.State == "reserved" && o.DatabaseOID != 0 ||
		phase == "activation" && o.State == "ready" && o.DatabaseOID != 0
}

func cloneMaintenanceSQLError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	return mapErr(err)
}
