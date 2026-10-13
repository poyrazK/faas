package state

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ManagedPostgresDrainStore = (*PgStore)(nil)
var _ ManagedPostgresDrainAuthority = (*PgStore)(nil)

func (s *PgStore) ReserveManagedPostgresDrain(ctx context.Context, cutoverID, instanceID, ownerNodeID string) (ManagedPostgresDrain, error) {
	for _, id := range []string{cutoverID, instanceID, ownerNodeID} {
		if !qualificationRecoveryUUIDValid(id) {
			return ManagedPostgresDrain{}, ErrInvalidArgument
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ManagedPostgresDrain{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := sqlc.New()
	row, err := q.LockManagedPostgresDrainReservation(ctx, tx, sqlc.LockManagedPostgresDrainReservationParams{CutoverID: cutoverID, InstanceID: instanceID, OwnerNodeID: ownerNodeID})
	if err != nil {
		return ManagedPostgresDrain{}, drainAuthorityError(err)
	}
	record, err := q.InsertManagedPostgresDrain(ctx, tx, sqlc.InsertManagedPostgresDrainParams{ID: uuid.NewString(), CutoverID: cutoverID,
		AppID: row.AppID, DeploymentID: row.DeploymentID, InstanceID: row.InstanceID, NodeID: row.NodeID, WakeID: row.WakeID,
		OwnerNodeID: row.OwnerNodeID, OwnerRevision: row.OwnerRevision, FencedAt: row.FencedAt})
	if err != nil {
		return ManagedPostgresDrain{}, mapErr(err)
	}
	drain, err := managedPostgresDrainFromSQL(record)
	if err != nil {
		return ManagedPostgresDrain{}, err
	}
	return drain, mapErr(tx.Commit(ctx))
}

func (s *PgStore) ManagedPostgresDrain(ctx context.Context, id string) (ManagedPostgresDrain, error) {
	if !qualificationRecoveryUUIDValid(id) {
		return ManagedPostgresDrain{}, ErrInvalidArgument
	}
	row, err := sqlc.New().GetManagedPostgresDrain(ctx, s.pool, id)
	if err != nil {
		return ManagedPostgresDrain{}, mapErr(err)
	}
	return managedPostgresDrainFromSQL(row)
}

func managedPostgresDrainFromSQL(row sqlc.ManagedPostgresCutoverDrain) (ManagedPostgresDrain, error) {
	f := ManagedPostgresDrainFrame{ID: pgUUIDString(row.ID), CutoverID: pgUUIDString(row.CutoverID), AppID: pgUUIDString(row.AppID),
		DeploymentID: pgUUIDString(row.DeploymentID), InstanceID: pgUUIDString(row.InstanceID), NodeID: pgUUIDString(row.NodeID),
		WakeID: pgUUIDString(row.WakeID), OwnerNodeID: pgUUIDString(row.OwnerNodeID), OwnerRevision: row.OwnerRevision, FencedAt: row.FencedAt.Time.UTC()}
	d := ManagedPostgresDrain{Frame: f}
	if !f.Valid() || !row.FencedAt.Valid {
		return d, ErrConflict
	}
	if row.State == "retired" {
		p := ManagedPostgresDrainProof{DrainID: f.ID, NativeGeneration: pgUUIDString(row.NativeGeneration), KernelBootID: pgUUIDString(row.KernelBootID), ProcessesExited: true, ResourcesRemoved: true}
		if !p.ValidFor(f) || !row.RetiredAt.Valid {
			return d, ErrConflict
		}
		d.Proof = &p
		stamp := row.RetiredAt.Time.UTC()
		d.RetiredAt = &stamp
	} else if row.State != "pending" || row.NativeGeneration.Valid || row.KernelBootID.Valid || row.RetiredAt.Valid {
		return d, ErrConflict
	}
	return d, nil
}

// Lock order is app/cutover, member, instance. Keep these locks through native
// teardown, including joining a boot that has not yet published runtime state.
func lockManagedPostgresDrain(ctx context.Context, tx pgx.Tx, frame ManagedPostgresDrainFrame) (ManagedPostgresDrain, error) {
	q := sqlc.New()
	app, err := q.LockManagedPostgresDrainApp(ctx, tx, frame.AppID)
	if err != nil {
		return ManagedPostgresDrain{}, drainAuthorityError(err)
	}
	if app.OwnerNodeID != frame.OwnerNodeID || app.OwnerRevision != frame.OwnerRevision || app.CutoverID != frame.CutoverID ||
		!app.FencedAt.Valid || !app.FencedAt.Time.Equal(frame.FencedAt) {
		return ManagedPostgresDrain{}, ErrConflict
	}
	row, err := q.LockManagedPostgresDrainMember(ctx, tx, frame.ID)
	if err != nil {
		return ManagedPostgresDrain{}, drainAuthorityError(err)
	}
	drain, err := managedPostgresDrainFromSQL(row)
	if err != nil || !drain.Frame.Equal(frame) {
		return ManagedPostgresDrain{}, errors.Join(ErrConflict, err)
	}
	ins, err := q.LockManagedPostgresDrainInstance(ctx, tx, frame.InstanceID)
	if err != nil {
		return ManagedPostgresDrain{}, drainAuthorityError(err)
	}
	if ins.AppID != frame.AppID || ins.DeploymentID != frame.DeploymentID || ins.NodeID != frame.NodeID || ins.WakeID != frame.WakeID {
		return ManagedPostgresDrain{}, ErrConflict
	}
	if ins.State != string(StateStopped) && !CanTransition(State(ins.State), StateStopped) {
		return ManagedPostgresDrain{}, ErrConflict
	}
	if drain.Proof != nil && ins.State != string(StateStopped) {
		return ManagedPostgresDrain{}, ErrConflict
	}
	return drain, nil
}

func drainAuthorityError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	return mapErr(err)
}

func (s *PgStore) WithManagedPostgresDrainAuthority(ctx context.Context, frame ManagedPostgresDrainFrame,
	retire func(context.Context) (ManagedPostgresDrainProof, error)) (ManagedPostgresDrainProof, error) {
	if !frame.Valid() || retire == nil {
		return ManagedPostgresDrainProof{}, ErrInvalidArgument
	}
	ctx, cancel := context.WithTimeout(ctx, api.ManagedPostgresCutoverDrainTimeout)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ManagedPostgresDrainProof{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	drain, err := lockManagedPostgresDrain(ctx, tx, frame)
	if err != nil {
		return ManagedPostgresDrainProof{}, err
	}
	if drain.Proof != nil {
		return *drain.Proof, mapErr(tx.Commit(ctx))
	}
	proof, err := retire(ctx)
	if err != nil {
		return ManagedPostgresDrainProof{}, err
	}
	if !proof.ValidFor(frame) {
		return ManagedPostgresDrainProof{}, ErrConflict
	}
	return proof, mapErr(tx.Commit(ctx))
}

// A receipt and terminal lifecycle state commit atomically. No scheduler
// reservation is released on an RPC error or a failed/stale database write.
func (s *PgStore) CompleteManagedPostgresDrain(ctx context.Context, frame ManagedPostgresDrainFrame, proof ManagedPostgresDrainProof) error {
	if !proof.ValidFor(frame) {
		return ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	drain, err := lockManagedPostgresDrain(ctx, tx, frame)
	if err != nil {
		return err
	}
	if drain.Proof != nil {
		if *drain.Proof != proof {
			return ErrConflict
		}
		return mapErr(tx.Commit(ctx))
	}
	q := sqlc.New()
	count, err := q.SaveManagedPostgresDrainProof(ctx, tx, sqlc.SaveManagedPostgresDrainProofParams{ID: frame.ID, NativeGeneration: proof.NativeGeneration, KernelBootID: proof.KernelBootID})
	if err != nil {
		return mapErr(err)
	}
	if count != 1 {
		return ErrConflict
	}
	count, err = q.StopManagedPostgresDrainInstance(ctx, tx, sqlc.StopManagedPostgresDrainInstanceParams{InstanceID: frame.InstanceID, AppID: frame.AppID, DeploymentID: frame.DeploymentID, NodeID: frame.NodeID, WakeID: frame.WakeID})
	if err != nil {
		return mapErr(err)
	}
	if count != 1 {
		return ErrConflict
	}
	if err := q.ReleaseManagedPostgresDrainHostPorts(ctx, tx, sqlc.ReleaseManagedPostgresDrainHostPortsParams{NodeID: frame.NodeID, InstanceID: frame.InstanceID}); err != nil {
		return mapErr(err)
	}
	return mapErr(tx.Commit(ctx))
}

func (s *PgStore) ListPendingManagedPostgresDrains(ctx context.Context, ownerNodeID, afterID string, limit int) ([]ManagedPostgresDrain, error) {
	if !qualificationRecoveryUUIDValid(ownerNodeID) || (afterID != "" && !qualificationRecoveryUUIDValid(afterID)) || limit < 1 || limit > api.ManagedPostgresCutoverDrainBatchMax {
		return nil, ErrInvalidArgument
	}
	rows, err := sqlc.New().ListPendingManagedPostgresDrains(ctx, s.pool, sqlc.ListPendingManagedPostgresDrainsParams{OwnerNodeID: ownerNodeID, AfterID: afterID, PageLimit: int32(limit)})
	if err != nil {
		return nil, mapErr(err)
	}
	drains := make([]ManagedPostgresDrain, 0, len(rows))
	for _, row := range rows {
		d, err := managedPostgresDrainFromSQL(row)
		if err != nil {
			return nil, err
		}
		drains = append(drains, d)
	}
	return drains, nil
}
