package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ EnvironmentGitOpsQualificationRuntimeStore = (*PgStore)(nil)

func (s *PgStore) PublishEnvironmentWorkloadQualificationRuntime(ctx context.Context, claimed EnvironmentWorkloadQualificationRequest, runtime EnvironmentWorkloadQualificationRuntime) (Instance, error) {
	if !qualificationRuntimeValid(runtime) {
		return Instance{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Instance{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	row, err := s.qualificationCurrentTx(ctx, tx, claimed.ID)
	if err != nil {
		return Instance{}, err
	}
	current := qualificationRequestFromSQL(row)
	if !qualificationLeaseMatches(current, claimed, time.Now()) || current.ReservedInstanceID == "" {
		return Instance{}, ErrConflict
	}
	q := sqlc.New()
	inputs, err := q.EnvironmentQualificationAdmissionInputs(ctx, tx, sqlc.EnvironmentQualificationAdmissionInputsParams{
		AppID: row.AppID, NodeID: mustPgUUID(runtime.NodeID)})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Instance{}, ErrConflict
		}
		return Instance{}, mapErr(err)
	}
	mayDeploy, err := q.LockEnvironmentQualificationAccount(ctx, tx, row.AppID)
	if err != nil {
		return Instance{}, mapErr(err)
	}
	if !mayDeploy.Valid || !mayDeploy.Bool {
		return Instance{}, ErrConflict
	}
	instance, err := q.LockEnvironmentQualificationRuntimeInstance(ctx, tx, row.ReservedInstanceID)
	if err != nil {
		return Instance{}, mapErr(err)
	}
	ins := qualificationInstanceFromSQL(instance)
	if !qualificationRuntimeMatches(ins, current, runtime) || ins.RAMMB != int(inputs.RamMb) || (State(ins.State) != StateColdBooting && !qualificationRuntimeAlreadyPublished(ins, runtime)) {
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
	if State(ins.State) == StateColdBooting {
		updated, err := q.PublishEnvironmentQualificationRuntime(ctx, tx, sqlc.PublishEnvironmentQualificationRuntimeParams{
			ID: row.ReservedInstanceID, Netns: runtime.Netns, HostIp: runtime.HostIP, GuestUid: int32(runtime.GuestUID)})
		if err != nil {
			return Instance{}, mapErr(err)
		}
		ins = qualificationInstanceFromSQL(updated)
	}
	if err := recordInstanceRuntimeConfigReceipt(ctx, tx, ins.ID, ins.WakeID, runtime.Inputs); err != nil {
		return Instance{}, err
	}
	if !qualificationLeaseMatches(current, claimed, time.Now()) {
		return Instance{}, ErrConflict
	}
	return ins, mapErr(tx.Commit(ctx))
}
