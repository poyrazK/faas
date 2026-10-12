package state

// adr: 801
import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ RegionalRuntimeFloorHistoryStore = (*PgStore)(nil)

func (s *PgStore) RegionalRuntimeFloorHistoryStart(ctx context.Context) (time.Time, error) {
	row, err := (&sqlc.Queries{}).RegionalRuntimeFloorHistoryStart(ctx, s.pool)
	return row.Time, regionalCapacityError(err)
}

func (s *PgStore) ListRegionalRuntimeFloorIntervals(ctx context.Context, app string, minute time.Time, after int64, limit int) ([]RegionalRuntimeFloorInterval, error) {
	if !validRegionalBillingHistoryPage(app, minute, after, limit) {
		return nil, ErrInvalidArgument
	}
	rows, err := (&sqlc.Queries{}).ListRegionalRuntimeFloorIntervals(ctx, s.pool, sqlc.ListRegionalRuntimeFloorIntervalsParams{AppID: uuidToPgtype(app), AfterID: after, BatchLimit: int32(limit), MinuteStart: pgtype.Timestamptz{Time: minute.UTC(), Valid: true}, MinuteEnd: pgtype.Timestamptz{Time: minute.Add(time.Minute).UTC(), Valid: true}})
	if err != nil {
		return nil, regionalCapacityError(err)
	}
	out := make([]RegionalRuntimeFloorInterval, 0, len(rows))
	for _, row := range rows {
		r, err := regionalRuntimeFloorDecode(row.ID, pgUUIDString(row.AppID), pgUUIDString(row.ObjectID), row.Kind, row.StartedAt.Time, exclusiveTime(row.EndedAt), row.StateClaims)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}
