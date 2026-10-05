package state

// adr: 623
import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) IssueRegionalRuntimePlacement(ctx context.Context, p runtimequota.Placement) error {
	return s.issueRegionalRuntimePlacement(ctx, p, nil)
}

func (s *PgStore) IssueRegionalRuntimeLaunch(ctx context.Context, p runtimequota.Placement, source runtimequota.LaunchSource) error {
	if source.Validate() != nil || source.Permit != p.Permit {
		return ErrInvalidArgument
	}
	return s.issueRegionalRuntimePlacement(ctx, p, &source)
}

func (s *PgStore) issueRegionalRuntimePlacement(ctx context.Context, p runtimequota.Placement, source *runtimequota.LaunchSource) error {
	if !validIssuedRegionalPlacement(p) {
		return ErrInvalidArgument
	}
	app := p.Permit.Grant.Binding.RegionalAppID
	_, err := withRegionalCapacityTx(ctx, s, app, func(tx pgx.Tx, q *sqlc.Queries) (struct{}, error) {
		if err := checkCurrentRegionalCapacity(ctx, tx, q, p.Permit); err != nil {
			return struct{}{}, err
		}
		physical, err := prepareRegionalPhysical(ctx, tx, q, p.Permit)
		if err != nil {
			return struct{}{}, err
		}
		var seat RegionalRuntimeServingSeatReceipt
		if p.Seat != nil {
			seat, err = prepareRegionalSeat(ctx, tx, q, *p.Seat)
			if err != nil {
				return struct{}{}, err
			}
		}
		if source != nil {
			if err := prepareRegionalLaunchSource(ctx, tx, q, *source, physical); err != nil {
				return struct{}{}, err
			}
		}
		if physical.IssuedAt == nil {
			_, err = q.IssueRegionalRuntimeCapacity(ctx, tx, sqlc.IssueRegionalRuntimeCapacityParams{AppID: uuidToPgtype(app), PermitID: uuidToPgtype(p.Permit.ID)})
			if err != nil {
				return struct{}{}, err
			}
		}
		if p.Seat != nil && seat.IssuedAt == nil {
			_, err = q.IssueRegionalRuntimeServingSeat(ctx, tx, sqlc.IssueRegionalRuntimeServingSeatParams{AppID: uuidToPgtype(app), SeatID: uuidToPgtype(p.Seat.ID)})
		}
		return struct{}{}, err
	})
	return err
}

func prepareRegionalPhysical(ctx context.Context, tx pgx.Tx, q *sqlc.Queries, p runtimequota.CapacityPermit) (RegionalRuntimeCapacityReceipt, error) {
	r, err := readRegionalCapacity(ctx, tx, p.Grant.Binding.RegionalAppID, p.ID)
	if errors.Is(err, ErrNotFound) {
		return insertRegionalCapacity(ctx, tx, q, p)
	}
	if err != nil {
		return RegionalRuntimeCapacityReceipt{}, err
	}
	return r, checkRegionalPhysicalForIssue(p, r)
}

func prepareRegionalSeat(ctx context.Context, tx pgx.Tx, q *sqlc.Queries, seat runtimequota.ServingSeat) (RegionalRuntimeServingSeatReceipt, error) {
	r, err := readRegionalServingSeat(ctx, tx, seat.Permit.Grant.Binding.RegionalAppID, seat.ID)
	if errors.Is(err, ErrNotFound) {
		return insertRegionalServingSeat(ctx, tx, q, seat)
	}
	if err != nil {
		return RegionalRuntimeServingSeatReceipt{}, err
	}
	return r, checkRegionalSeatForIssue(seat, r)
}
