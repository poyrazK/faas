package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ EnvironmentGitOpsQualificationInstanceStore = (*PgStore)(nil)

func qualificationInstanceFromSQL(row sqlc.Instance) Instance {
	ins := Instance{ID: pgUUIDString(row.ID), AppID: pgUUIDString(row.AppID), DeploymentID: pgUUIDString(row.DeploymentID), State: row.State,
		RAMMB: int(row.RamMb), NodeID: pgUUIDString(row.NodeID), WakeID: pgUUIDString(row.WakeID), StartedAt: row.StartedAt.Time,
		Mode: row.Mode, Netns: row.Netns.String, GuestUID: int(row.GuestUid.Int32), LastRequestAt: row.LastRequestAt.Time, ParkedAt: row.ParkedAt.Time,
		Kind: row.Kind, TailCount: int(row.TailCount), RequestCount: row.RequestCount}
	if row.HostIp != nil {
		ins.HostIP = row.HostIp.String()
	}
	if row.FrameworkReadyAt.Valid {
		at := row.FrameworkReadyAt.Time
		ins.FrameworkReadyAt = &at
	}
	if row.StartupCpuBoostUntil.Valid {
		at := row.StartupCpuBoostUntil.Time
		ins.StartupCPUBoostUntil = &at
	}
	return ins
}

func (s *PgStore) CreateEnvironmentWorkloadQualificationInstance(ctx context.Context, claimed EnvironmentWorkloadQualificationRequest, placement EnvironmentWorkloadQualificationPlacement) (EnvironmentWorkloadQualificationAdmission, error) {
	if !qualificationPlacementValid(placement) {
		return EnvironmentWorkloadQualificationAdmission{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return EnvironmentWorkloadQualificationAdmission{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	row, err := s.qualificationCurrentTx(ctx, tx, claimed.ID)
	if err != nil {
		return EnvironmentWorkloadQualificationAdmission{}, err
	}
	current := qualificationRequestFromSQL(row)
	if !qualificationLeaseMatches(current, claimed, time.Now()) || current.ExecutionMode == api.ExecutionModeJob || current.ReservedInstanceID == "" {
		return EnvironmentWorkloadQualificationAdmission{}, ErrConflict
	}
	q := sqlc.New()
	// Match ordinary admission's node -> account reservation order after the
	// reviewed source/app authority locks, including an idempotent retry.
	if err := q.LockEnvironmentQualificationNode(ctx, tx, sqlc.LockEnvironmentQualificationNodeParams{
		LockClass: nodeReservationLockClass, NodeID: placement.NodeID}); err != nil {
		return EnvironmentWorkloadQualificationAdmission{}, mapErr(err)
	}
	inputs, err := q.EnvironmentQualificationAdmissionInputs(ctx, tx, sqlc.EnvironmentQualificationAdmissionInputsParams{
		AppID: mustPgUUID(current.AppID), NodeID: mustPgUUID(placement.NodeID)})
	if err != nil || int(inputs.RamMb) != placement.RAMMB {
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return EnvironmentWorkloadQualificationAdmission{}, mapErr(err)
		}
		return EnvironmentWorkloadQualificationAdmission{}, ErrConflict
	}
	// Keep account status stable through both initial admission and a retry.
	// An UPDATE lock avoids a later worker quota lock upgrade across sources.
	mayDeploy, err := q.LockEnvironmentQualificationAccount(ctx, tx, row.AppID)
	if err != nil {
		return EnvironmentWorkloadQualificationAdmission{}, mapErr(err)
	}
	if !mayDeploy.Valid || !mayDeploy.Bool {
		return EnvironmentWorkloadQualificationAdmission{}, ErrConflict
	}
	prior, err := q.EnvironmentQualificationInstance(ctx, tx, mustPgUUID(current.ReservedInstanceID))
	if err == nil {
		ins := qualificationInstanceFromSQL(prior)
		if !qualificationAdmissionMatches(ins, current, placement) || !qualificationLeaseMatches(current, claimed, time.Now()) {
			return EnvironmentWorkloadQualificationAdmission{}, ErrConflict
		}
		return EnvironmentWorkloadQualificationAdmission{Instance: ins}, mapErr(tx.Commit(ctx))
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return EnvironmentWorkloadQualificationAdmission{}, mapErr(err)
	}
	used, err := q.EnvironmentQualificationNodeUsedMB(ctx, tx, sqlc.EnvironmentQualificationNodeUsedMBParams{
		NodeID: mustPgUUID(placement.NodeID), OverheadMb: api.PerVMOverheadMB})
	if err != nil {
		return EnvironmentWorkloadQualificationAdmission{}, mapErr(err)
	}
	if used+int64(placement.RAMMB+api.PerVMOverheadMB) > int64(inputs.AdmissionCeilingMb) {
		return EnvironmentWorkloadQualificationAdmission{}, ErrNodeCapacity
	}
	if current.ExecutionMode == api.ExecutionModeWorker {
		if err := reserveAccountWorker(ctx, tx, current.AppID, current.DeploymentID); err != nil {
			return EnvironmentWorkloadQualificationAdmission{}, err
		}
	}
	if _, err := q.SetEnvironmentWorkloadQualificationContext(ctx, tx, claimed.LeaseToken); err != nil {
		return EnvironmentWorkloadQualificationAdmission{}, mapErr(err)
	}
	ins, err := q.CreateEnvironmentQualificationInstance(ctx, tx, sqlc.CreateEnvironmentQualificationInstanceParams{
		ID: mustPgUUID(current.ReservedInstanceID), AppID: row.AppID, DeploymentID: row.DeploymentID,
		NodeID: mustPgUUID(placement.NodeID), WakeID: mustPgUUID(placement.WakeID), RamMb: int32(placement.RAMMB), Mode: qualificationInstanceMode(current)})
	if err != nil {
		return EnvironmentWorkloadQualificationAdmission{}, mapErr(err)
	}
	if !qualificationLeaseMatches(current, claimed, time.Now()) {
		return EnvironmentWorkloadQualificationAdmission{}, ErrConflict
	}
	return EnvironmentWorkloadQualificationAdmission{Instance: qualificationInstanceFromSQL(ins), Created: true}, mapErr(tx.Commit(ctx))
}
