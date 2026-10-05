package state

// adr: 626
import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func regionalServingResumeRow(row sqlc.RegionalRuntimeServingResume) (RegionalRuntimeServingResumeReceipt, error) {
	r := RegionalRuntimeServingResumeReceipt{CreatedAt: row.CreatedAt.Time}
	if err := json.Unmarshal(row.IntentClaims, &r.Intent); err != nil {
		return RegionalRuntimeServingResumeReceipt{}, regionalCapacityError(err)
	}
	if r.Intent.Validate() != nil {
		return RegionalRuntimeServingResumeReceipt{}, ErrInvalidArgument
	}
	return r, nil
}

func (s *PgStore) RegionalRuntimeServingResume(ctx context.Context, app, id string) (RegionalRuntimeServingResumeReceipt, error) {
	if !validRegionalReleaseIDs(app, id, id) {
		return RegionalRuntimeServingResumeReceipt{}, ErrInvalidArgument
	}
	row, err := (&sqlc.Queries{}).RegionalRuntimeServingResume(ctx, s.pool, sqlc.RegionalRuntimeServingResumeParams{AppID: uuidToPgtype(app), ResumeID: uuidToPgtype(id)})
	if err != nil {
		return RegionalRuntimeServingResumeReceipt{}, regionalCapacityError(err)
	}
	return regionalServingResumeRow(row)
}

func (s *PgStore) IssueRegionalRuntimeServingResume(ctx context.Context, i runtimequota.ServingResumeIntent) (RegionalRuntimeServingResumeReceipt, error) {
	if i.Validate() != nil {
		return RegionalRuntimeServingResumeReceipt{}, ErrInvalidArgument
	}
	p := i.Seat.Permit
	app := p.Grant.Binding.RegionalAppID
	return withRegionalCapacityTx(ctx, s, app, func(tx pgx.Tx, q *sqlc.Queries) (RegionalRuntimeServingResumeReceipt, error) {
		if err := checkCurrentRegionalCapacity(ctx, tx, q, p); err != nil {
			return RegionalRuntimeServingResumeReceipt{}, err
		}
		physical, err := readRegionalCapacity(ctx, tx, app, p.ID)
		if errors.Is(err, ErrNotFound) {
			err = ErrRegionalRuntimePlacementUnavailable
		}
		if err != nil {
			return RegionalRuntimeServingResumeReceipt{}, err
		}
		if err = checkIssuedRegionalPhysical(p, physical); err != nil {
			return RegionalRuntimeServingResumeReceipt{}, err
		}
		row, err := q.RegionalRuntimeServingResume(ctx, tx, sqlc.RegionalRuntimeServingResumeParams{AppID: uuidToPgtype(app), ResumeID: uuidToPgtype(i.ID)})
		if errors.Is(err, pgx.ErrNoRows) {
			row, err = q.HeldRegionalRuntimeServingResume(ctx, tx, sqlc.HeldRegionalRuntimeServingResumeParams{AppID: uuidToPgtype(app), PermitID: uuidToPgtype(p.ID)})
		}
		if err == nil {
			r, loadErr := regionalServingResumeRow(row)
			if loadErr != nil {
				return RegionalRuntimeServingResumeReceipt{}, loadErr
			}
			if err = checkResumeRetry(i, r.Intent); err != nil {
				return RegionalRuntimeServingResumeReceipt{}, err
			}
			seat, seatErr := readRegionalServingSeat(ctx, tx, app, r.Intent.Seat.ID)
			if seatErr != nil {
				return RegionalRuntimeServingResumeReceipt{}, seatErr
			}
			return r, checkIssuedRegionalSeat(r.Intent.Seat, seat)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return RegionalRuntimeServingResumeReceipt{}, err
		}
		seat, err := prepareRegionalSeat(ctx, tx, q, i.Seat)
		if err != nil {
			return RegionalRuntimeServingResumeReceipt{}, err
		}
		if seat.IssuedAt == nil {
			if _, err = q.IssueRegionalRuntimeServingSeat(ctx, tx, sqlc.IssueRegionalRuntimeServingSeatParams{AppID: uuidToPgtype(app), SeatID: uuidToPgtype(i.Seat.ID)}); err != nil {
				return RegionalRuntimeServingResumeReceipt{}, err
			}
		}
		raw, err := json.Marshal(i)
		if err != nil {
			return RegionalRuntimeServingResumeReceipt{}, err
		}
		row, err = q.InsertRegionalRuntimeServingResume(ctx, tx, sqlc.InsertRegionalRuntimeServingResumeParams{AppID: uuidToPgtype(app), ResumeID: uuidToPgtype(i.ID), PermitID: uuidToPgtype(p.ID), SeatID: uuidToPgtype(i.Seat.ID), IntentClaims: raw})
		if err != nil {
			return RegionalRuntimeServingResumeReceipt{}, err
		}
		return regionalServingResumeRow(row)
	})
}
