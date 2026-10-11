package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ EnvironmentQualificationRestoreRuntimeStore = (*PgStore)(nil)

func (s *PgStore) PublishEnvironmentQualificationRestoreRuntime(ctx context.Context, claimed EnvironmentWorkloadQualificationRequest, frame EnvironmentQualificationExecution, runtime EnvironmentWorkloadQualificationRuntime) (Instance, error) {
	if !qualificationRuntimeValid(runtime) {
		return Instance{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Instance{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	row, _, err := s.qualificationRestoreCurrentTx(ctx, tx, claimed)
	if err != nil {
		return Instance{}, err
	}
	current := qualificationRequestFromSQL(row)
	if frame.CaptureInstanceID != current.ReservedInstanceID || !qualificationLeaseMatches(current, claimed, time.Now()) {
		return Instance{}, ErrConflict
	}
	q := sqlc.New()
	reservation, err := q.EnvironmentQualificationRestoreReservation(ctx, tx, sqlc.EnvironmentQualificationRestoreReservationParams{RequestID: row.ID, Attempt: row.Attempt})
	if errors.Is(err, pgx.ErrNoRows) {
		return Instance{}, ErrConflict
	}
	if err != nil {
		return Instance{}, mapErr(err)
	}
	if pgUUIDString(reservation.InstanceID) != frame.InstanceID || pgUUIDString(reservation.CaptureInstanceID) != current.ReservedInstanceID {
		return Instance{}, ErrConflict
	}
	if err := q.LockEnvironmentQualificationNode(ctx, tx, sqlc.LockEnvironmentQualificationNodeParams{LockClass: nodeReservationLockClass, NodeID: runtime.NodeID}); err != nil {
		return Instance{}, mapErr(err)
	}
	inputs, err := q.EnvironmentQualificationAdmissionInputs(ctx, tx, sqlc.EnvironmentQualificationAdmissionInputsParams{AppID: row.AppID, NodeID: mustPgUUID(runtime.NodeID)})
	if err != nil || int(inputs.RamMb) != frame.RAMMB {
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return Instance{}, mapErr(err)
		}
		return Instance{}, ErrConflict
	}
	mayDeploy, err := q.LockEnvironmentQualificationAccount(ctx, tx, row.AppID)
	if err != nil {
		return Instance{}, mapErr(err)
	}
	if !mayDeploy.Valid || !mayDeploy.Bool {
		return Instance{}, ErrConflict
	}
	stored, err := q.LockEnvironmentQualificationExecution(ctx, tx, mustPgUUID(frame.InstanceID))
	if err != nil {
		return Instance{}, mapErr(err)
	}
	status, err := qualificationExecutionFromSQL(stored)
	if err != nil || status.Execution != frame || !qualificationRestoreRuntimeExecutionCurrent(current, status, Instance{ID: frame.InstanceID, AppID: frame.AppID,
		DeploymentID: frame.DeploymentID, NodeID: frame.NodeID, WakeID: frame.WakeID, RAMMB: frame.RAMMB}) {
		return Instance{}, ErrConflict
	}
	instance, err := q.LockEnvironmentQualificationRuntimeInstance(ctx, tx, stored.InstanceID)
	if err != nil {
		return Instance{}, mapErr(err)
	}
	ins := qualificationInstanceFromSQL(instance)
	if !qualificationRestoreRuntimeExecutionCurrent(current, status, ins) ||
		!qualificationRuntimeMatches(ins, qualificationRestoreRequestForInstance(current, ins.ID), runtime) {
		return Instance{}, ErrConflict
	}
	fresh, err := readRuntimeConfigInputsFresh(ctx, tx, current.AppID, runtime.Inputs)
	if err != nil {
		return Instance{}, err
	}
	if !fresh {
		return Instance{}, ErrConflict
	}
	if _, err := q.SetEnvironmentWorkloadQualificationContext(ctx, tx, claimed.LeaseToken); err != nil {
		return Instance{}, mapErr(err)
	}
	prior := ins
	if State(ins.State) == StateColdBooting {
		updated, err := q.PublishEnvironmentQualificationRuntime(ctx, tx, sqlc.PublishEnvironmentQualificationRuntimeParams{
			ID: stored.InstanceID, Netns: runtime.Netns, HostIp: runtime.HostIP, GuestUid: int32(runtime.GuestUID)})
		if err != nil {
			return Instance{}, mapErr(err)
		}
		ins = qualificationInstanceFromSQL(updated)
	} else {
		config, err := q.InstanceRuntimeConfigReceipt(ctx, tx, stored.InstanceID)
		if err != nil {
			return Instance{}, mapErr(err)
		}
		recorded, err := runtimeConfigInputsFromSQL(config.Scope, config.BoundaryAt, config.Variables, config.SecretVersions, config.SecretRefs, config.SidecarSecretVersions, config.AllSecrets)
		if err != nil || !qualificationRuntimeAlreadyPublished(ins, runtime) || pgUUIDString(config.WakeID) != ins.WakeID || !runtimeConfigInputsPostgresEqual(recorded, runtime.Inputs) {
			return Instance{}, ErrConflict
		}
	}
	if err := recordInstanceRuntimeConfigReceipt(ctx, tx, ins.ID, ins.WakeID, runtime.Inputs); err != nil {
		return Instance{}, err
	}
	if !qualificationLeaseMatches(current, claimed, time.Now()) {
		return Instance{}, ErrConflict
	}
	if State(prior.State) != StateColdBooting && ins != prior {
		return Instance{}, ErrConflict
	}
	return ins, mapErr(tx.Commit(ctx))
}
