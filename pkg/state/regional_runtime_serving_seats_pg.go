package state

// adr: 492
import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func regionalServingSeatRow(row sqlc.RegionalRuntimeServingSeat) (RegionalRuntimeServingSeatReceipt, error) {
	r := RegionalRuntimeServingSeatReceipt{CreatedAt: row.CreatedAt.Time, IssuedAt: exclusiveTime(row.IssuedAt), ReleasedAt: exclusiveTime(row.ReleasedAt), FenceID: exclusiveUUID(row.FenceID), ClosedByPhysicalStop: row.ClosedByPhysicalStop}
	for _, f := range []struct {
		raw   []byte
		value any
	}{{row.SeatClaims, &r.Seat}, {row.StopClaims, &r.Stop}, {row.StopEnvelope, &r.StopEnvelope}} {
		if len(f.raw) != 0 {
			if err := json.Unmarshal(f.raw, f.value); err != nil {
				return RegionalRuntimeServingSeatReceipt{}, regionalCapacityError(err)
			}
		}
	}
	return r, nil
}

func readRegionalServingSeat(ctx context.Context, db sqlc.DBTX, app, id string) (RegionalRuntimeServingSeatReceipt, error) {
	row, err := (&sqlc.Queries{}).RegionalRuntimeServingSeat(ctx, db, sqlc.RegionalRuntimeServingSeatParams{AppID: uuidToPgtype(app), SeatID: uuidToPgtype(id)})
	if err != nil {
		return RegionalRuntimeServingSeatReceipt{}, regionalCapacityError(err)
	}
	return regionalServingSeatRow(row)
}

func (s *PgStore) RegionalRuntimeServingSeat(ctx context.Context, app, id string) (RegionalRuntimeServingSeatReceipt, error) {
	if !validRegionalReleaseIDs(app, id, id) {
		return RegionalRuntimeServingSeatReceipt{}, ErrInvalidArgument
	}
	return readRegionalServingSeat(ctx, s.pool, app, id)
}

func (s *PgStore) ListRegionalRuntimeServingSeats(ctx context.Context, app, allocation, after string, limit int) ([]RegionalRuntimeServingSeatReceipt, error) {
	if !validCapacityPage(app, allocation, after, limit) {
		return nil, ErrInvalidArgument
	}
	rows, err := (&sqlc.Queries{}).ListRegionalRuntimeServingSeats(ctx, s.pool, sqlc.ListRegionalRuntimeServingSeatsParams{AppID: uuidToPgtype(app), AllocationID: uuidToPgtype(allocation), AfterID: uuidToPgtype(after), RowLimit: int32(limit)})
	if err != nil {
		return nil, regionalCapacityError(err)
	}
	out := make([]RegionalRuntimeServingSeatReceipt, 0, len(rows))
	for _, row := range rows {
		r, err := regionalServingSeatRow(row)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

func (s *PgStore) RegionalRuntimeServingSeatUsage(ctx context.Context, app, allocation string) (RegionalRuntimeServingSeatUsage, error) {
	if !validRegionalReleaseIDs(app, allocation, allocation) {
		return RegionalRuntimeServingSeatUsage{}, ErrInvalidArgument
	}
	row, err := (&sqlc.Queries{}).RegionalRuntimeServingSeatUsage(ctx, s.pool, sqlc.RegionalRuntimeServingSeatUsageParams{AppID: uuidToPgtype(app), AllocationID: uuidToPgtype(allocation)})
	return RegionalRuntimeServingSeatUsage{TotalHeld: row.TotalHeld, IssuedHeld: row.IssuedHeld, SteadyHeld: row.SteadyHeld, RolloutHeld: row.RolloutHeld}, regionalCapacityError(err)
}

func (s *PgStore) ReserveRegionalRuntimeServingSeat(ctx context.Context, seat runtimequota.ServingSeat) (RegionalRuntimeServingSeatReceipt, error) {
	if seat.Validate() != nil {
		return RegionalRuntimeServingSeatReceipt{}, ErrInvalidArgument
	}
	p := seat.Permit
	app := p.Grant.Binding.RegionalAppID
	return withRegionalCapacityTx(ctx, s, app, func(tx pgx.Tx, q *sqlc.Queries) (RegionalRuntimeServingSeatReceipt, error) {
		r, err := readRegionalServingSeat(ctx, tx, app, seat.ID)
		if err == nil {
			if r.Seat != seat {
				return RegionalRuntimeServingSeatReceipt{}, ErrConflict
			}
			return r, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return RegionalRuntimeServingSeatReceipt{}, err
		}
		physical, err := readRegionalCapacity(ctx, tx, app, p.ID)
		if err != nil {
			return RegionalRuntimeServingSeatReceipt{}, err
		}
		if physical.Permit != p || physical.FenceID != "" || physical.ReleasedAt != nil {
			return RegionalRuntimeServingSeatReceipt{}, ErrConflict
		}
		if err = checkCurrentRegionalCapacity(ctx, tx, q, p); err != nil {
			return RegionalRuntimeServingSeatReceipt{}, err
		}
		return insertRegionalServingSeat(ctx, tx, q, seat)
	})
}

func (s *PgStore) IssueRegionalRuntimeServingSeat(ctx context.Context, app, id string) (RegionalRuntimeServingSeatReceipt, error) {
	if !validRegionalReleaseIDs(app, id, id) {
		return RegionalRuntimeServingSeatReceipt{}, ErrInvalidArgument
	}
	return withRegionalCapacityTx(ctx, s, app, func(tx pgx.Tx, q *sqlc.Queries) (RegionalRuntimeServingSeatReceipt, error) {
		r, err := readRegionalServingSeat(ctx, tx, app, id)
		if err != nil || r.IssuedAt != nil {
			return r, err
		}
		if r.FenceID != "" || r.ReleasedAt != nil {
			return RegionalRuntimeServingSeatReceipt{}, ErrConflict
		}
		physical, err := readRegionalCapacity(ctx, tx, app, r.Seat.Permit.ID)
		if err != nil {
			return RegionalRuntimeServingSeatReceipt{}, err
		}
		if physical.Permit != r.Seat.Permit || physical.IssuedAt == nil || physical.FenceID != "" || physical.ReleasedAt != nil {
			return RegionalRuntimeServingSeatReceipt{}, ErrConflict
		}
		if err = checkCurrentRegionalCapacity(ctx, tx, q, r.Seat.Permit); err != nil {
			return RegionalRuntimeServingSeatReceipt{}, err
		}
		row, err := q.IssueRegionalRuntimeServingSeat(ctx, tx, sqlc.IssueRegionalRuntimeServingSeatParams{AppID: uuidToPgtype(app), SeatID: uuidToPgtype(id)})
		if err != nil {
			return RegionalRuntimeServingSeatReceipt{}, err
		}
		return regionalServingSeatRow(row)
	})
}

func (s *PgStore) FenceRegionalRuntimeServingSeat(ctx context.Context, app, id, fenceID string) (RegionalRuntimeServingSeatReceipt, error) {
	if !validRegionalReleaseIDs(app, id, fenceID) {
		return RegionalRuntimeServingSeatReceipt{}, ErrInvalidArgument
	}
	return withRegionalCapacityTx(ctx, s, app, func(tx pgx.Tx, q *sqlc.Queries) (RegionalRuntimeServingSeatReceipt, error) {
		r, err := readRegionalServingSeat(ctx, tx, app, id)
		if err != nil {
			return r, err
		}
		if _, err = fenceServingSeat(r, fenceID); err != nil {
			return RegionalRuntimeServingSeatReceipt{}, err
		}
		if r.FenceID != "" {
			return r, nil
		}
		row, err := q.FenceRegionalRuntimeServingSeat(ctx, tx, sqlc.FenceRegionalRuntimeServingSeatParams{AppID: uuidToPgtype(app), SeatID: uuidToPgtype(id), FenceID: uuidToPgtype(fenceID)})
		if err != nil {
			return RegionalRuntimeServingSeatReceipt{}, err
		}
		return regionalServingSeatRow(row)
	})
}

func (s *PgStore) applyRegionalRuntimeServingSeatStop(ctx context.Context, stop runtimequota.ServingSeatStop, e runtimequota.Envelope) (RegionalRuntimeServingSeatReceipt, error) {
	app := stop.Seat.Permit.Grant.Binding.RegionalAppID
	return withRegionalCapacityTx(ctx, s, app, func(tx pgx.Tx, q *sqlc.Queries) (RegionalRuntimeServingSeatReceipt, error) {
		old, err := readRegionalServingSeat(ctx, tx, app, stop.Seat.ID)
		if err != nil {
			return old, err
		}
		r, err := stopServingSeat(old, stop, e)
		if err != nil || old.Stop != nil || old.ClosedByPhysicalStop {
			return r, err
		}
		claims, err := json.Marshal(stop)
		if err != nil {
			return RegionalRuntimeServingSeatReceipt{}, err
		}
		envelope, err := json.Marshal(e)
		if err != nil {
			return RegionalRuntimeServingSeatReceipt{}, err
		}
		row, err := q.StopRegionalRuntimeServingSeat(ctx, tx, sqlc.StopRegionalRuntimeServingSeatParams{AppID: uuidToPgtype(app), SeatID: uuidToPgtype(stop.Seat.ID), FenceID: uuidToPgtype(stop.FenceID), StopClaims: claims, StopEnvelope: envelope})
		if err != nil {
			return RegionalRuntimeServingSeatReceipt{}, err
		}
		return regionalServingSeatRow(row)
	})
}

func insertRegionalServingSeat(ctx context.Context, tx pgx.Tx, q *sqlc.Queries, seat runtimequota.ServingSeat) (RegionalRuntimeServingSeatReceipt, error) {
	p := seat.Permit
	app := p.Grant.Binding.RegionalAppID
	claims, err := json.Marshal(seat)
	if err != nil {
		return RegionalRuntimeServingSeatReceipt{}, err
	}
	row, err := q.InsertRegionalRuntimeServingSeat(ctx, tx, sqlc.InsertRegionalRuntimeServingSeatParams{AppID: uuidToPgtype(app), AllocationID: uuidToPgtype(p.Grant.AllocationID), SeatID: uuidToPgtype(seat.ID), PermitID: uuidToPgtype(p.ID), Generation: p.Grant.Generation, DeploymentID: uuidToPgtype(p.DeploymentID), Rollout: seat.Rollout, SeatClaims: claims})
	if err != nil {
		return RegionalRuntimeServingSeatReceipt{}, err
	}
	return regionalServingSeatRow(row)
}
