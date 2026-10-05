package state

// adr: 634
import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) BeginRegionalRuntimeLaunchFailure(ctx context.Context, source runtimequota.LaunchSource, fence string) (RegionalRuntimeCapacityReceipt, error) {
	if source.Validate() != nil || !validRegionalReleaseIDs(fence, fence, fence) {
		return RegionalRuntimeCapacityReceipt{}, ErrInvalidArgument
	}
	p := source.Permit
	app := p.Grant.Binding.RegionalAppID
	return withRegionalCapacityTx(ctx, s, app, func(tx pgx.Tx, q *sqlc.Queries) (RegionalRuntimeCapacityReceipt, error) {
		launch, err := readRegionalLaunchSource(ctx, tx, app, p.ID)
		if err != nil {
			return RegionalRuntimeCapacityReceipt{}, err
		}
		old, err := readRegionalCapacity(ctx, tx, app, p.ID)
		if err != nil {
			return RegionalRuntimeCapacityReceipt{}, err
		}
		if launch.Source != source || old.Permit != p || old.IssuedAt == nil {
			return RegionalRuntimeCapacityReceipt{}, ErrConflict
		}
		matches, err := q.LockRegionalRuntimeAttempt(ctx, tx, sqlc.LockRegionalRuntimeAttemptParams{InstanceID: uuidToPgtype(p.InstanceID), AppID: uuidToPgtype(app), DeploymentID: uuidToPgtype(p.DeploymentID), NodeID: uuidToPgtype(p.NodeID), RunID: uuidToPgtype(p.RunID)})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return RegionalRuntimeCapacityReceipt{}, err
		}
		if err == nil && !matches {
			return RegionalRuntimeCapacityReceipt{}, ErrConflict
		}
		resumed, err := q.RegionalRuntimeLaunchWasResumed(ctx, tx, sqlc.RegionalRuntimeLaunchWasResumedParams{AppID: uuidToPgtype(app), PermitID: uuidToPgtype(p.ID)})
		if err != nil {
			return RegionalRuntimeCapacityReceipt{}, err
		}
		if resumed {
			return RegionalRuntimeCapacityReceipt{}, ErrConflict
		}
		r, err := beginRegionalRetirement(old, p, fence)
		if err != nil || old.FenceID != "" {
			return r, err
		}
		row, err := q.FenceRegionalRuntimeCapacity(ctx, tx, sqlc.FenceRegionalRuntimeCapacityParams{AppID: uuidToPgtype(app), PermitID: uuidToPgtype(p.ID), FenceID: uuidToPgtype(r.FenceID)})
		if err != nil {
			return RegionalRuntimeCapacityReceipt{}, err
		}
		return regionalCapacityRow(row)
	})
}

func (s *PgStore) FailRegionalRuntimeAttempt(ctx context.Context, source runtimequota.LaunchSource, expected string) error {
	next := RegionalRuntimeFailureState(State(expected))
	if source.Validate() != nil || next == "" {
		return ErrInvalidArgument
	}
	p := source.Permit
	_, err := withRegionalCapacityTx(ctx, s, p.Grant.Binding.RegionalAppID, func(tx pgx.Tx, q *sqlc.Queries) (struct{}, error) {
		launch, err := readRegionalLaunchSource(ctx, tx, p.Grant.Binding.RegionalAppID, p.ID)
		if err != nil {
			return struct{}{}, err
		}
		receipt, err := readRegionalCapacity(ctx, tx, p.Grant.Binding.RegionalAppID, p.ID)
		if err != nil {
			return struct{}{}, err
		}
		if err := checkRegionalAttemptFailure(source, launch, receipt); err != nil {
			return struct{}{}, err
		}
		n, err := q.FailOriginalRegionalRuntimeAttempt(ctx, tx, sqlc.FailOriginalRegionalRuntimeAttemptParams{InstanceID: uuidToPgtype(p.InstanceID), AppID: uuidToPgtype(p.Grant.Binding.RegionalAppID), DeploymentID: uuidToPgtype(p.DeploymentID), NodeID: uuidToPgtype(p.NodeID), RunID: uuidToPgtype(p.RunID), ExpectedState: expected, NextState: string(next)})
		if err == nil && n != 1 {
			err = ErrConflict
		}
		if err == nil {
			err = q.ReleaseOriginalRegionalRuntimeHostPorts(ctx, tx, sqlc.ReleaseOriginalRegionalRuntimeHostPortsParams{InstanceID: uuidToPgtype(p.InstanceID), NodeID: uuidToPgtype(p.NodeID)})
		}
		return struct{}{}, err
	})
	return err
}
